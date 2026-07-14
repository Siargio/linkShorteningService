package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Siargio/linkShorteningService/internal/domain"
	eventbus "github.com/Siargio/linkShorteningService/internal/event"
	"github.com/Siargio/linkShorteningService/internal/repository"
	"github.com/Siargio/linkShorteningService/pkg/cache"
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
	// repo — основное постоянное хранилище.
	//
	// PostgreSQL является источником истины:
	//
	//   - там хранится каждая ссылка;
	//   - там хранится clicks;
	//   - там хранится дата создания.
	repo repository.LinkRepository

	// linkCache — быстрый кеш длинных URL.
	//
	// Сейчас это Redis.
	//
	// Кеш не является источником истины.
	// Если Redis недоступен, service обращается в PostgreSQL.
	linkCache cache.LinkCache

	// publisher отправляет доменные события в Kafka.
	publisher eventbus.Publisher

	// generateCode — функция генерации короткого кода.
	//
	// Она передаётся как зависимость для удобства тестирования.
	generateCode func(length int) (string, error)

	// codeLength — длина генерируемого кода.
	codeLength int

	// maxCreateAttempts — максимальное количество попыток
	// при коллизиях короткого кода.
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
	linkCache cache.LinkCache,
	publisher eventbus.Publisher,
) LinkService {
	return newLinkService(
		repo,
		linkCache,
		publisher,
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
	linkCache cache.LinkCache,
	publisher eventbus.Publisher,
	generateCode func(length int) (string, error),
	codeLength int,
	maxCreateAttempts int,
) *linkService {
	// Если publisher не передан,
	// используем безопасную пустую реализацию.
	if publisher == nil {
		publisher = eventbus.NopPublisher{}
	}

	return &linkService{
		repo:              repo,
		linkCache:         linkCache,
		publisher:         publisher,
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
			// PostgreSQL успешно сохранил ссылку.
			//
			// После основной операции публикуем доменное событие.
			s.publishBestEffort(
				ctx,
				eventbus.NewLinkCreatedEvent(
					createdLink,
				),
			)

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

// Resolve получает длинный URL по короткому коду
// и увеличивает счётчик переходов.
//
// Алгоритм:
//
//  1. Проверить короткий код.
//  2. Попытаться получить URL из Redis.
//  3. При cache miss обратиться в PostgreSQL.
//  4. Сохранить найденный URL в Redis.
//  5. Увеличить clicks в PostgreSQL.
//  6. Вернуть длинный URL.
func (s *linkService) Resolve(
	ctx context.Context,
	code string,
) (string, error) {
	// Проверяем short_code до внешних запросов.
	if err := domain.ValidateShortCode(code); err != nil {
		return "", fmt.Errorf(
			"validate short code: %w",
			err,
		)
	}

	var (
		longURL      string
		foundInCache bool
	)

	// Сначала пытаемся получить URL из Redis.
	cachedURL, cacheErr := s.linkCache.Get(ctx, code)

	if cacheErr == nil {
		// Redis cache hit.
		longURL = cachedURL
		foundInCache = true
	} else {
		// Redis cache miss или техническая ошибка Redis.
		//
		// PostgreSQL остаётся основным источником данных.
		link, err := s.repo.GetByCode(ctx, code)
		if err != nil {
			return "", fmt.Errorf("resolve link by code: %w", err)
		}

		longURL = link.LongURL
	}

	// Счётчик переходов всегда обновляем в PostgreSQL.
	if err := s.repo.IncrementClicks(ctx, code); err != nil {
		return "", fmt.Errorf(
			"increment clicks for code %q: %w",
			code,
			err,
		)
	}

	// Если URL был получен не из Redis,
	// пытаемся положить его в кеш.
	//
	// Ошибка Redis не должна ломать redirect.
	if !foundInCache {
		_ = s.linkCache.Set(ctx, code, longURL)
	}

	// Основная операция завершилась успешно:
	// ссылка найдена, clicks увеличен.
	//
	// Теперь публикуем Kafka-событие.
	s.publishBestEffort(ctx, eventbus.NewLinkVisitedEvent(code, longURL))

	return longURL, nil
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

// publishBestEffort пытается отправить событие,
// но не ломает основной пользовательский сценарий.
//
// Например, если Kafka временно недоступна:
//   - ссылка всё равно создаётся;
//   - redirect всё равно выполняется;
//   - ошибка Kafka записывается в лог.
func (s *linkService) publishBestEffort(
	ctx context.Context,
	linkEvent eventbus.LinkEvent,
) {
	if err := s.publisher.Publish(
		ctx,
		linkEvent,
	); err != nil {
		slog.Warn(
			"failed to publish Kafka event",
			"event_type",
			linkEvent.EventType,
			"short_code",
			linkEvent.ShortCode,
			"error",
			err,
		)
	}
}
