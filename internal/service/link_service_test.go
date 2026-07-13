package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Siargio/linkShorteningService/internal/domain"
)

// fakeLinkRepository — тестовая реализация repository.LinkRepository.
//
// Она не подключается к PostgreSQL.
//
// Вместо настоящих SQL-запросов каждый метод вызывает
// функцию, которую мы задаём непосредственно внутри теста.
type fakeLinkRepository struct {
	// createFunc определяет поведение метода Create.
	createFunc func(
		ctx context.Context,
		link domain.Link,
	) (domain.Link, error)

	// getByCodeFunc определяет поведение метода GetByCode.
	getByCodeFunc func(
		ctx context.Context,
		code string,
	) (domain.Link, error)

	// incrementClicksFunc определяет поведение IncrementClicks.
	incrementClicksFunc func(
		ctx context.Context,
		code string,
	) error
}

// Create реализует метод интерфейса repository.LinkRepository.
func (f *fakeLinkRepository) Create(
	ctx context.Context,
	link domain.Link,
) (domain.Link, error) {
	// Если тест не настроил createFunc, это ошибка самого теста.
	if f.createFunc == nil {
		panic("fakeLinkRepository.createFunc is not configured")
	}

	return f.createFunc(ctx, link)
}

// GetByCode реализует метод интерфейса repository.LinkRepository.
func (f *fakeLinkRepository) GetByCode(
	ctx context.Context,
	code string,
) (domain.Link, error) {
	if f.getByCodeFunc == nil {
		panic("fakeLinkRepository.getByCodeFunc is not configured")
	}

	return f.getByCodeFunc(ctx, code)
}

// IncrementClicks реализует метод интерфейса repository.LinkRepository.
func (f *fakeLinkRepository) IncrementClicks(
	ctx context.Context,
	code string,
) error {
	if f.incrementClicksFunc == nil {
		panic("fakeLinkRepository.incrementClicksFunc is not configured")
	}

	return f.incrementClicksFunc(ctx, code)
}

func TestLinkService_Shorten_Success(t *testing.T) {
	// arrange — подготавливаем данные и зависимости.

	createdAt := time.Date(
		2026,
		time.July,
		13,
		12,
		0,
		0,
		0,
		time.UTC,
	)

	// Считаем количество вызовов repository.Create.
	createCalls := 0

	repo := &fakeLinkRepository{
		createFunc: func(
			ctx context.Context,
			link domain.Link,
		) (domain.Link, error) {
			createCalls++

			// Проверяем, что service передал ожидаемый короткий код.
			if link.ShortCode != "abc123" {
				t.Fatalf(
					"expected short code %q, got %q",
					"abc123",
					link.ShortCode,
				)
			}

			// Проверяем, что service удалил пробелы вокруг URL.
			if link.LongURL != "https://golang.org" {
				t.Fatalf(
					"expected long URL %q, got %q",
					"https://golang.org",
					link.LongURL,
				)
			}

			// Имитируем результат INSERT ... RETURNING.
			return domain.Link{
				ID:        1,
				ShortCode: link.ShortCode,
				LongURL:   link.LongURL,
				Clicks:    0,
				CreatedAt: createdAt,
			}, nil
		},
	}

	// Используем предсказуемый генератор.
	generator := func(length int) (string, error) {
		if length != 6 {
			t.Fatalf(
				"expected code length 6, got %d",
				length,
			)
		}

		return "abc123", nil
	}

	service := newLinkService(
		repo,
		generator,
		6,
		5,
	)

	// act — вызываем тестируемый метод.

	link, err := service.Shorten(
		context.Background(),
		"  https://golang.org  ",
	)

	// assert — проверяем результат.

	if err != nil {
		t.Fatalf("Shorten returned error: %v", err)
	}

	if createCalls != 1 {
		t.Fatalf(
			"expected Create to be called once, got %d",
			createCalls,
		)
	}

	if link.ID != 1 {
		t.Fatalf("expected ID 1, got %d", link.ID)
	}

	if link.ShortCode != "abc123" {
		t.Fatalf(
			"expected short code %q, got %q",
			"abc123",
			link.ShortCode,
		)
	}

	if link.Clicks != 0 {
		t.Fatalf(
			"expected clicks 0, got %d",
			link.Clicks,
		)
	}

	if !link.CreatedAt.Equal(createdAt) {
		t.Fatalf(
			"expected CreatedAt %v, got %v",
			createdAt,
			link.CreatedAt,
		)
	}
}

func TestLinkService_Shorten_InvalidURL(t *testing.T) {
	// Счётчик нужен, чтобы убедиться:
	// при невалидном URL repository вообще не вызывается.
	createCalls := 0
	generatorCalls := 0

	repo := &fakeLinkRepository{
		createFunc: func(
			ctx context.Context,
			link domain.Link,
		) (domain.Link, error) {
			createCalls++
			return domain.Link{}, nil
		},
	}

	generator := func(length int) (string, error) {
		generatorCalls++
		return "abc123", nil
	}

	service := newLinkService(
		repo,
		generator,
		6,
		5,
	)

	_, err := service.Shorten(
		context.Background(),
		"golang.org",
	)

	if !errors.Is(err, domain.ErrInvalidURL) {
		t.Fatalf(
			"expected ErrInvalidURL, got %v",
			err,
		)
	}

	if generatorCalls != 0 {
		t.Fatalf(
			"expected generator not to be called, got %d calls",
			generatorCalls,
		)
	}

	if createCalls != 0 {
		t.Fatalf(
			"expected Create not to be called, got %d calls",
			createCalls,
		)
	}
}

func TestLinkService_Shorten_RetriesAfterCollision(t *testing.T) {
	// Подготавливаем последовательность кодов:
	//
	//   1. abc123 — коллизия;
	//   2. xyz789 — успешное создание.
	generatedCodes := []string{
		"abc123",
		"xyz789",
	}

	generatorCalls := 0

	generator := func(length int) (string, error) {
		code := generatedCodes[generatorCalls]
		generatorCalls++

		return code, nil
	}

	createCalls := 0

	repo := &fakeLinkRepository{
		createFunc: func(
			ctx context.Context,
			link domain.Link,
		) (domain.Link, error) {
			createCalls++

			// Первая попытка завершается коллизией.
			if createCalls == 1 {
				if link.ShortCode != "abc123" {
					t.Fatalf(
						"expected first code abc123, got %q",
						link.ShortCode,
					)
				}

				return domain.Link{},
					domain.ErrShortCodeCollision
			}

			// Вторая попытка успешна.
			if link.ShortCode != "xyz789" {
				t.Fatalf(
					"expected second code xyz789, got %q",
					link.ShortCode,
				)
			}

			return domain.Link{
				ID:        2,
				ShortCode: link.ShortCode,
				LongURL:   link.LongURL,
				Clicks:    0,
				CreatedAt: time.Now(),
			}, nil
		},
	}

	service := newLinkService(
		repo,
		generator,
		6,
		5,
	)

	link, err := service.Shorten(
		context.Background(),
		"https://golang.org",
	)

	if err != nil {
		t.Fatalf("Shorten returned error: %v", err)
	}

	if generatorCalls != 2 {
		t.Fatalf(
			"expected generator to be called twice, got %d",
			generatorCalls,
		)
	}

	if createCalls != 2 {
		t.Fatalf(
			"expected Create to be called twice, got %d",
			createCalls,
		)
	}

	if link.ShortCode != "xyz789" {
		t.Fatalf(
			"expected code xyz789, got %q",
			link.ShortCode,
		)
	}
}

func TestLinkService_Shorten_AllAttemptsHaveCollisions(t *testing.T) {
	const maxAttempts = 3

	generatorCalls := 0
	createCalls := 0

	generator := func(length int) (string, error) {
		generatorCalls++
		return "abc123", nil
	}

	repo := &fakeLinkRepository{
		createFunc: func(
			ctx context.Context,
			link domain.Link,
		) (domain.Link, error) {
			createCalls++

			// Каждая попытка завершается коллизией.
			return domain.Link{},
				domain.ErrShortCodeCollision
		},
	}

	service := newLinkService(
		repo,
		generator,
		6,
		maxAttempts,
	)

	_, err := service.Shorten(
		context.Background(),
		"https://golang.org",
	)

	if !errors.Is(err, domain.ErrShortCodeCollision) {
		t.Fatalf(
			"expected ErrShortCodeCollision, got %v",
			err,
		)
	}

	if generatorCalls != maxAttempts {
		t.Fatalf(
			"expected %d generator calls, got %d",
			maxAttempts,
			generatorCalls,
		)
	}

	if createCalls != maxAttempts {
		t.Fatalf(
			"expected %d Create calls, got %d",
			maxAttempts,
			createCalls,
		)
	}
}

func TestLinkService_Shorten_GeneratorError(t *testing.T) {
	generatorError := errors.New("random source unavailable")

	createCalls := 0

	repo := &fakeLinkRepository{
		createFunc: func(
			ctx context.Context,
			link domain.Link,
		) (domain.Link, error) {
			createCalls++
			return domain.Link{}, nil
		},
	}

	generator := func(length int) (string, error) {
		return "", generatorError
	}

	service := newLinkService(
		repo,
		generator,
		6,
		5,
	)

	_, err := service.Shorten(
		context.Background(),
		"https://golang.org",
	)

	if !errors.Is(err, generatorError) {
		t.Fatalf(
			"expected generator error, got %v",
			err,
		)
	}

	if createCalls != 0 {
		t.Fatalf(
			"expected Create not to be called, got %d calls",
			createCalls,
		)
	}
}

func TestLinkService_Shorten_RepositoryError(t *testing.T) {
	repositoryError := errors.New("database unavailable")

	repo := &fakeLinkRepository{
		createFunc: func(
			ctx context.Context,
			link domain.Link,
		) (domain.Link, error) {
			return domain.Link{}, repositoryError
		},
	}

	generatorCalls := 0

	generator := func(length int) (string, error) {
		generatorCalls++
		return "abc123", nil
	}

	service := newLinkService(
		repo,
		generator,
		6,
		5,
	)

	_, err := service.Shorten(
		context.Background(),
		"https://golang.org",
	)

	if !errors.Is(err, repositoryError) {
		t.Fatalf(
			"expected repository error, got %v",
			err,
		)
	}

	// При обычной ошибке БД повторных попыток быть не должно.
	if generatorCalls != 1 {
		t.Fatalf(
			"expected one generator call, got %d",
			generatorCalls,
		)
	}
}

func TestLinkService_Resolve_Success(t *testing.T) {
	getCalls := 0
	incrementCalls := 0

	repo := &fakeLinkRepository{
		getByCodeFunc: func(
			ctx context.Context,
			code string,
		) (domain.Link, error) {
			getCalls++

			if code != "abc123" {
				t.Fatalf(
					"expected code abc123, got %q",
					code,
				)
			}

			return domain.Link{
				ID:        1,
				ShortCode: "abc123",
				LongURL:   "https://golang.org",
				Clicks:    10,
				CreatedAt: time.Now(),
			}, nil
		},

		incrementClicksFunc: func(
			ctx context.Context,
			code string,
		) error {
			incrementCalls++

			if code != "abc123" {
				t.Fatalf(
					"expected code abc123, got %q",
					code,
				)
			}

			return nil
		},
	}

	service := newLinkService(
		repo,
		domain.GenerateShortCode,
		6,
		5,
	)

	longURL, err := service.Resolve(
		context.Background(),
		"abc123",
	)

	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}

	if longURL != "https://golang.org" {
		t.Fatalf(
			"expected URL %q, got %q",
			"https://golang.org",
			longURL,
		)
	}

	if getCalls != 1 {
		t.Fatalf(
			"expected GetByCode once, got %d",
			getCalls,
		)
	}

	if incrementCalls != 1 {
		t.Fatalf(
			"expected IncrementClicks once, got %d",
			incrementCalls,
		)
	}
}

func TestLinkService_Resolve_InvalidCode(t *testing.T) {
	getCalls := 0
	incrementCalls := 0

	repo := &fakeLinkRepository{
		getByCodeFunc: func(
			ctx context.Context,
			code string,
		) (domain.Link, error) {
			getCalls++
			return domain.Link{}, nil
		},

		incrementClicksFunc: func(
			ctx context.Context,
			code string,
		) error {
			incrementCalls++
			return nil
		},
	}

	service := newLinkService(
		repo,
		domain.GenerateShortCode,
		6,
		5,
	)

	_, err := service.Resolve(
		context.Background(),
		"invalid-code",
	)

	if !errors.Is(err, domain.ErrInvalidShortCode) {
		t.Fatalf(
			"expected ErrInvalidShortCode, got %v",
			err,
		)
	}

	if getCalls != 0 {
		t.Fatalf(
			"expected GetByCode not to be called, got %d",
			getCalls,
		)
	}

	if incrementCalls != 0 {
		t.Fatalf(
			"expected IncrementClicks not to be called, got %d",
			incrementCalls,
		)
	}
}

func TestLinkService_Resolve_LinkNotFound(t *testing.T) {
	incrementCalls := 0

	repo := &fakeLinkRepository{
		getByCodeFunc: func(
			ctx context.Context,
			code string,
		) (domain.Link, error) {
			return domain.Link{}, domain.ErrLinkNotFound
		},

		incrementClicksFunc: func(
			ctx context.Context,
			code string,
		) error {
			incrementCalls++
			return nil
		},
	}

	service := newLinkService(
		repo,
		domain.GenerateShortCode,
		6,
		5,
	)

	_, err := service.Resolve(
		context.Background(),
		"abc123",
	)

	if !errors.Is(err, domain.ErrLinkNotFound) {
		t.Fatalf(
			"expected ErrLinkNotFound, got %v",
			err,
		)
	}

	// Счётчик не должен увеличиваться для несуществующей ссылки.
	if incrementCalls != 0 {
		t.Fatalf(
			"expected IncrementClicks not to be called, got %d",
			incrementCalls,
		)
	}
}

func TestLinkService_Resolve_IncrementError(t *testing.T) {
	incrementError := errors.New("update failed")

	repo := &fakeLinkRepository{
		getByCodeFunc: func(
			ctx context.Context,
			code string,
		) (domain.Link, error) {
			return domain.Link{
				ID:        1,
				ShortCode: "abc123",
				LongURL:   "https://golang.org",
			}, nil
		},

		incrementClicksFunc: func(
			ctx context.Context,
			code string,
		) error {
			return incrementError
		},
	}

	service := newLinkService(
		repo,
		domain.GenerateShortCode,
		6,
		5,
	)

	_, err := service.Resolve(
		context.Background(),
		"abc123",
	)

	if !errors.Is(err, incrementError) {
		t.Fatalf(
			"expected increment error, got %v",
			err,
		)
	}
}

func TestLinkService_Stats_Success(t *testing.T) {
	incrementCalls := 0

	expectedLink := domain.Link{
		ID:        1,
		ShortCode: "abc123",
		LongURL:   "https://golang.org",
		Clicks:    25,
		CreatedAt: time.Now(),
	}

	repo := &fakeLinkRepository{
		getByCodeFunc: func(
			ctx context.Context,
			code string,
		) (domain.Link, error) {
			return expectedLink, nil
		},

		incrementClicksFunc: func(
			ctx context.Context,
			code string,
		) error {
			incrementCalls++
			return nil
		},
	}

	service := newLinkService(
		repo,
		domain.GenerateShortCode,
		6,
		5,
	)

	link, err := service.Stats(
		context.Background(),
		"abc123",
	)

	if err != nil {
		t.Fatalf("Stats returned error: %v", err)
	}

	if link.ShortCode != expectedLink.ShortCode {
		t.Fatalf(
			"expected code %q, got %q",
			expectedLink.ShortCode,
			link.ShortCode,
		)
	}

	if link.Clicks != expectedLink.Clicks {
		t.Fatalf(
			"expected clicks %d, got %d",
			expectedLink.Clicks,
			link.Clicks,
		)
	}

	// Получение статистики не считается переходом.
	if incrementCalls != 0 {
		t.Fatalf(
			"expected IncrementClicks not to be called, got %d",
			incrementCalls,
		)
	}
}

func TestLinkService_Stats_InvalidCode(t *testing.T) {
	getCalls := 0

	repo := &fakeLinkRepository{
		getByCodeFunc: func(
			ctx context.Context,
			code string,
		) (domain.Link, error) {
			getCalls++
			return domain.Link{}, nil
		},
	}

	service := newLinkService(
		repo,
		domain.GenerateShortCode,
		6,
		5,
	)

	_, err := service.Stats(
		context.Background(),
		"",
	)

	if !errors.Is(err, domain.ErrInvalidShortCode) {
		t.Fatalf(
			"expected ErrInvalidShortCode, got %v",
			err,
		)
	}

	if getCalls != 0 {
		t.Fatalf(
			"expected GetByCode not to be called, got %d",
			getCalls,
		)
	}
}
