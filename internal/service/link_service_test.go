package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Siargio/linkShorteningService/internal/domain"
	"github.com/Siargio/linkShorteningService/pkg/cache"
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

// fakeLinkCache — тестовая реализация cache.LinkCache.
//
// Она позволяет управлять поведением Redis без запуска
// настоящего Redis-контейнера.
type fakeLinkCache struct {
	// getFunc определяет поведение метода Get.
	getFunc func(
		ctx context.Context,
		code string,
	) (string, error)

	// setFunc определяет поведение метода Set.
	setFunc func(
		ctx context.Context,
		code string,
		longURL string,
	) error
}

// Get реализует cache.LinkCache.
//
// Если конкретный тест не настроил getFunc,
// по умолчанию считаем, что значения в кеше нет.
//
// Это удобно для большинства старых тестов:
// service автоматически пойдёт в repository.
func (f *fakeLinkCache) Get(
	ctx context.Context,
	code string,
) (string, error) {
	if f.getFunc == nil {
		return "", cache.ErrCacheMiss
	}

	return f.getFunc(ctx, code)
}

// Set реализует cache.LinkCache.
//
// Если конкретный тест не настроил setFunc,
// считаем, что запись в Redis прошла успешно.
func (f *fakeLinkCache) Set(
	ctx context.Context,
	code string,
	longURL string,
) error {
	if f.setFunc == nil {
		return nil
	}

	return f.setFunc(ctx, code, longURL)
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
		&fakeLinkCache{},
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
		&fakeLinkCache{},
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
		&fakeLinkCache{},
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
		&fakeLinkCache{},
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
		&fakeLinkCache{},
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
		&fakeLinkCache{},
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
		&fakeLinkCache{},
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
		&fakeLinkCache{},
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
		&fakeLinkCache{},
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
		&fakeLinkCache{},
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
		&fakeLinkCache{},
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
		&fakeLinkCache{},
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

func TestLinkService_Resolve_CacheHit(t *testing.T) {
	// Счётчики позволяют проверить,
	// какие зависимости реально вызывались.
	cacheGetCalls := 0
	cacheSetCalls := 0
	repositoryGetCalls := 0
	incrementCalls := 0

	// Имитируем Redis, в котором ссылка уже есть.
	linkCache := &fakeLinkCache{
		getFunc: func(
			ctx context.Context,
			code string,
		) (string, error) {
			cacheGetCalls++

			if code != "abc123" {
				t.Fatalf(
					"expected cache code %q, got %q",
					"abc123",
					code,
				)
			}

			return "https://golang.org", nil
		},

		setFunc: func(
			ctx context.Context,
			code string,
			longURL string,
		) error {
			cacheSetCalls++
			return nil
		},
	}

	repo := &fakeLinkRepository{
		getByCodeFunc: func(
			ctx context.Context,
			code string,
		) (domain.Link, error) {
			// При cache hit этот метод вызываться не должен.
			repositoryGetCalls++

			return domain.Link{}, errors.New(
				"GetByCode must not be called on cache hit",
			)
		},

		incrementClicksFunc: func(
			ctx context.Context,
			code string,
		) error {
			incrementCalls++

			if code != "abc123" {
				t.Fatalf(
					"expected increment code %q, got %q",
					"abc123",
					code,
				)
			}

			return nil
		},
	}

	service := newLinkService(
		repo,
		linkCache,
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

	if cacheGetCalls != 1 {
		t.Fatalf(
			"expected cache Get once, got %d",
			cacheGetCalls,
		)
	}

	// PostgreSQL не должен читать саму ссылку,
	// поскольку URL уже получен из Redis.
	if repositoryGetCalls != 0 {
		t.Fatalf(
			"expected repository GetByCode not to be called, got %d",
			repositoryGetCalls,
		)
	}

	// Но clicks всё равно должен обновиться в PostgreSQL.
	if incrementCalls != 1 {
		t.Fatalf(
			"expected IncrementClicks once, got %d",
			incrementCalls,
		)
	}

	// Повторно записывать уже найденное значение не нужно.
	if cacheSetCalls != 0 {
		t.Fatalf(
			"expected cache Set not to be called, got %d",
			cacheSetCalls,
		)
	}
}

func TestLinkService_Resolve_CacheMiss(t *testing.T) {
	cacheGetCalls := 0
	cacheSetCalls := 0
	repositoryGetCalls := 0
	incrementCalls := 0

	linkCache := &fakeLinkCache{
		getFunc: func(
			ctx context.Context,
			code string,
		) (string, error) {
			cacheGetCalls++

			// Имитируем отсутствие ключа в Redis.
			return "", cache.ErrCacheMiss
		},

		setFunc: func(
			ctx context.Context,
			code string,
			longURL string,
		) error {
			cacheSetCalls++

			if code != "abc123" {
				t.Fatalf(
					"expected cache code %q, got %q",
					"abc123",
					code,
				)
			}

			if longURL != "https://golang.org" {
				t.Fatalf(
					"expected cached URL %q, got %q",
					"https://golang.org",
					longURL,
				)
			}

			return nil
		},
	}

	repo := &fakeLinkRepository{
		getByCodeFunc: func(
			ctx context.Context,
			code string,
		) (domain.Link, error) {
			repositoryGetCalls++

			return domain.Link{
				ID:        1,
				ShortCode: "abc123",
				LongURL:   "https://golang.org",
				Clicks:    5,
				CreatedAt: time.Now(),
			}, nil
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
		linkCache,
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

	if cacheGetCalls != 1 {
		t.Fatalf(
			"expected cache Get once, got %d",
			cacheGetCalls,
		)
	}

	// При cache miss ссылка читается из PostgreSQL.
	if repositoryGetCalls != 1 {
		t.Fatalf(
			"expected repository GetByCode once, got %d",
			repositoryGetCalls,
		)
	}

	if incrementCalls != 1 {
		t.Fatalf(
			"expected IncrementClicks once, got %d",
			incrementCalls,
		)
	}

	// После чтения из PostgreSQL URL должен попасть в Redis.
	if cacheSetCalls != 1 {
		t.Fatalf(
			"expected cache Set once, got %d",
			cacheSetCalls,
		)
	}
}

func TestLinkService_Resolve_RedisErrorFallsBackToRepository(
	t *testing.T,
) {
	redisError := errors.New("Redis connection refused")

	repositoryGetCalls := 0
	incrementCalls := 0

	linkCache := &fakeLinkCache{
		getFunc: func(
			ctx context.Context,
			code string,
		) (string, error) {
			// Имитируем не cache miss,
			// а настоящую ошибку подключения к Redis.
			return "", redisError
		},
	}

	repo := &fakeLinkRepository{
		getByCodeFunc: func(
			ctx context.Context,
			code string,
		) (domain.Link, error) {
			repositoryGetCalls++

			return domain.Link{
				ID:        1,
				ShortCode: code,
				LongURL:   "https://golang.org",
				Clicks:    5,
				CreatedAt: time.Now(),
			}, nil
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
		linkCache,
		domain.GenerateShortCode,
		6,
		5,
	)

	longURL, err := service.Resolve(
		context.Background(),
		"abc123",
	)
	if err != nil {
		t.Fatalf(
			"expected fallback to PostgreSQL, got error: %v",
			err,
		)
	}

	if longURL != "https://golang.org" {
		t.Fatalf(
			"expected URL %q, got %q",
			"https://golang.org",
			longURL,
		)
	}

	if repositoryGetCalls != 1 {
		t.Fatalf(
			"expected repository fallback once, got %d",
			repositoryGetCalls,
		)
	}

	if incrementCalls != 1 {
		t.Fatalf(
			"expected IncrementClicks once, got %d",
			incrementCalls,
		)
	}
}

func TestLinkService_Resolve_CacheSetErrorDoesNotBreakRedirect(
	t *testing.T,
) {
	cacheSetError := errors.New("Redis SET failed")

	linkCache := &fakeLinkCache{
		getFunc: func(
			ctx context.Context,
			code string,
		) (string, error) {
			return "", cache.ErrCacheMiss
		},

		setFunc: func(
			ctx context.Context,
			code string,
			longURL string,
		) error {
			// Имитируем ошибку сохранения в Redis.
			return cacheSetError
		},
	}

	repo := &fakeLinkRepository{
		getByCodeFunc: func(
			ctx context.Context,
			code string,
		) (domain.Link, error) {
			return domain.Link{
				ID:        1,
				ShortCode: code,
				LongURL:   "https://golang.org",
				Clicks:    0,
				CreatedAt: time.Now(),
			}, nil
		},

		incrementClicksFunc: func(
			ctx context.Context,
			code string,
		) error {
			return nil
		},
	}

	service := newLinkService(
		repo,
		linkCache,
		domain.GenerateShortCode,
		6,
		5,
	)

	longURL, err := service.Resolve(
		context.Background(),
		"abc123",
	)

	// Ошибка Redis SET не должна стать ошибкой HTTP-перехода.
	if err != nil {
		t.Fatalf(
			"expected successful redirect despite cache error, got %v",
			err,
		)
	}

	if longURL != "https://golang.org" {
		t.Fatalf(
			"expected URL %q, got %q",
			"https://golang.org",
			longURL,
		)
	}
}
