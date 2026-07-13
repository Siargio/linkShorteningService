package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Siargio/linkShorteningService/internal/domain"
	"github.com/Siargio/linkShorteningService/internal/repository"
)

const (
	// defaultCodeLength — длина автоматически генерируемого короткого кода.
	// Например: x7k2mN
	// В таблице PostgreSQL short_code имеет тип VARCHAR(10),
	// поэтому длина 6 безопасно укладывается в ограничение.
	defaultCodeLength = domain.DefaultShortCodeLength

	// defaultMaxCreateAttempts — максимальное количество попыток
	// создать ссылку при коллизиях короткого кода.
	// Бесконечно повторять генерацию нельзя, поэтому задаём лимит.
	defaultMaxCreateAttempts = 5
)

// LinkService описывает бизнес-операции сервиса сокращения ссылок.
//
// Handler будет зависеть от этого интерфейса, а не от конкретной реализации linkService.
// Благодаря интерфейсу в тестах HTTP-handler можно будет подставить
// фальшивую реализацию сервиса без запуска PostgreSQL.
type LinkService interface {
	// Shorten принимает длинную ссылку, проверяет её,
	// генерирует короткий код и сохраняет результат.
	Shorten(ctx context.Context, longURL string) (domain.Link, error)

	// Resolve получает длинный URL по короткому коду и увеличивает счётчик переходов.
	//
	// Этот метод будет использоваться endpoint:
	//	GET /{code}
	Resolve(ctx context.Context, code string) (string, error)

	// Stats получает информацию о ссылке без увеличения счётчика.
	//
	// Этот метод будет использоваться endpoint:
	//	GET /stats/{code}
	Stats(ctx context.Context, code string) (domain.Link, error)
}

// linkService — конкретная реализация бизнес-логики.
// Внешние пакеты должны создавать сервис через конструктор:
//
//	service.NewLinkService(repo)
type linkService struct {
	// repo — абстракция над хранилищем ссылок.
	// Сейчас реальной реализацией является PostgreSQL repository, но service об этом не знает.
	repo repository.LinkRepository

	// generateCode — функция генерации короткого кода.
	//
	// В production здесь используется domain.GenerateShortCode.
	//
	// Функция хранится как зависимость, чтобы в unit-тестах
	// мы могли возвращать заранее известные коды:
	//	abc123
	//	x7k2mN
	// Это делает тесты предсказуемыми.
	generateCode func(length int) (string, error)

	// codeLength — длина создаваемого короткого кода.
	codeLength int

	// maxCreateAttempts — максимальное количество попыток
	// сохранить ссылку при коллизиях short_code.
	maxCreateAttempts int
}

// Проверка во время компиляции.
//
// Если linkService перестанет реализовывать какой-либо метод
// интерфейса LinkService, проект не соберётся.
var _ LinkService = (*linkService)(nil)

// NewLinkService создаёт production-версию сервиса.
//
// Здесь используются стандартные настройки:
//
//   - генератор domain.GenerateShortCode;
//   - длина короткого кода 6 символов;
//   - максимум 5 попыток при коллизии.
func NewLinkService(
	repo repository.LinkRepository,
) LinkService {
	return newLinkService(
		repo,
		domain.GenerateShortCode,
		defaultCodeLength,
		defaultMaxCreateAttempts,
	)
}

// newLinkService — внутренний конструктор с возможностью передать собственные настройки.
//
// Он нужен преимущественно для unit-тестов.
// Например, в тесте мы можем передать генератор:
//
//	func(int) (string, error) {
//	    return "abc123", nil
//	}
//
// Тогда тест не зависит от случайных значений crypto/rand.
func newLinkService(
	repo repository.LinkRepository,
	generateCode func(length int) (string, error),
	codeLength int,
	maxCreateAttempts int,
) *linkService {
	return &linkService{
		repo:              repo,
		generateCode:      generateCode,
		codeLength:        codeLength,
		maxCreateAttempts: maxCreateAttempts,
	}
}

// Shorten создаёт новую короткую ссылку.
func (s *linkService) Shorten(
	ctx context.Context,
	longURL string,
) (domain.Link, error) {
	// Удаляем пробелы в начале и конце строки.
	//
	// Например: "  https://golang.org  "
	// превращается в: "https://golang.org"
	//
	// Пробелы внутри URL не изменяются.
	normalizedURL := strings.TrimSpace(longURL)

	// Проверяем, что URL:
	//   - не пустой;
	//   - содержит host;
	//   - использует http или https.
	if err := domain.ValidateLongURL(normalizedURL); err != nil {
		// Оборачиваем ошибку через %w.
		//
		// Handler позже сможет выполнить:
		//	errors.Is(err, domain.ErrInvalidURL)
		// и вернуть клиенту HTTP 400.
		return domain.Link{}, fmt.Errorf("validate long URL: %w", err)
	}

	// Несколько раз пытаемся создать ссылку.
	// Повторная попытка выполняется только при коллизии short_code.
	for attempt := 1; attempt <= s.maxCreateAttempts; attempt++ {
		// Генерируем новый короткий код.
		// В production здесь вызывается:
		//	domain.GenerateShortCode(6)
		code, err := s.generateCode(s.codeLength)
		if err != nil {
			// Если генерация не удалась, повторять запрос в repository
			// бессмысленно: короткого кода у нас нет.
			return domain.Link{}, fmt.Errorf("generate short code: %w", err)
		}

		// Создаём доменную сущность.
		//
		// ID, Clicks и CreatedAt пока не заполняем.
		// Эти значения установит PostgreSQL:
		//
		//   - ID — через SERIAL;
		//   - Clicks — через DEFAULT 0;
		//   - CreatedAt — через DEFAULT NOW().
		linkToCreate := domain.Link{
			ShortCode: code,
			LongURL:   normalizedURL,
		}

		// Пытаемся сохранить ссылку.
		createdLink, err := s.repo.Create(ctx, linkToCreate)
		if err == nil {
			// Ссылка успешно создана.
			//
			// createdLink уже содержит ID, Clicks и CreatedAt,
			// возвращённые PostgreSQL.
			return createdLink, nil
		}

		// Проверяем, является ли ошибка коллизией короткого кода.
		if errors.Is(err, domain.ErrShortCodeCollision) {
			// Если коллизия произошла не на последней попытке,
			// продолжаем цикл и генерируем новый код.
			if attempt < s.maxCreateAttempts {
				continue
			}

			// Если использовали все разрешённые попытки,
			// выйдем из цикла и вернём ошибку ниже.
			break
		}

		// Любая другая ошибка repository не должна приводить к повторной генерации кода.
		//
		// Например:
		//
		//   - PostgreSQL недоступен;
		//   - запрос отменён через context;
		//   - произошла сетевая ошибка;
		//   - нарушено другое ограничение таблицы.
		return domain.Link{}, fmt.Errorf("save shortened link: %w", err)
	}

	// если каждая попытка завершилась коллизией short_code.
	//
	// Возвращаем доменную ошибку, чтобы вызывающий код
	// мог определить её через errors.Is.
	return domain.Link{}, fmt.Errorf(
		"%w after %d attempts",
		domain.ErrShortCodeCollision,
		s.maxCreateAttempts,
	)
}

// Resolve находит длинный URL по короткому коду
// и увеличивает количество переходов.
func (s *linkService) Resolve(
	ctx context.Context,
	code string,
) (string, error) {
	// Проверяем короткий код до обращения к PostgreSQL.
	//
	// Это позволяет не выполнять бессмысленный SQL-запрос,
	// если код:
	//   - пустой;
	//   - длиннее 10 символов;
	//   - содержит запрещённые символы.
	if err := domain.ValidateShortCode(code); err != nil {
		return "", fmt.Errorf(
			"validate short code: %w",
			err,
		)
	}

	// Получаем ссылку из repository.
	link, err := s.repo.GetByCode(ctx, code)
	if err != nil {
		// Ошибка domain.ErrLinkNotFound сохраняется внутри цепочки.
		// Handler позже сможет вернуть HTTP 404.
		return "", fmt.Errorf(
			"resolve link by code: %w",
			err,
		)
	}

	// Увеличиваем счётчик только после того,
	// как ссылка была успешно найдена.
	//
	// UPDATE выполняется атомарно на стороне PostgreSQL:
	//
	//	SET clicks = clicks + 1
	//
	// Это безопаснее, чем:
	//   1. прочитать clicks;
	//   2. увеличить его в Go;
	//   3. записать новое значение.
	if err := s.repo.IncrementClicks(ctx, code); err != nil {
		return "", fmt.Errorf(
			"increment clicks for code %q: %w",
			code,
			err,
		)
	}

	// Возвращаем только длинный URL.
	// Handler использует его для HTTP redirect.
	return link.LongURL, nil
}

// Stats получает полную информацию о ссылке,
// не увеличивая количество переходов.
func (s *linkService) Stats(
	ctx context.Context,
	code string,
) (domain.Link, error) {
	// Проверяем код до обращения в хранилище.
	if err := domain.ValidateShortCode(code); err != nil {
		return domain.Link{}, fmt.Errorf(
			"validate short code: %w",
			err,
		)
	}

	// Получаем актуальные данные непосредственно из repository.
	//
	// Когда позже добавим Redis, статистику всё равно разумно
	// читать из PostgreSQL, потому что счётчик clicks хранится там.
	link, err := s.repo.GetByCode(ctx, code)
	if err != nil {
		return domain.Link{}, fmt.Errorf(
			"get link statistics: %w",
			err,
		)
	}

	// Возвращаем ссылку без изменения счётчика.
	return link, nil
}
