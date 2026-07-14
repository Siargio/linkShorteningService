package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Siargio/linkShorteningService/internal/domain"
)

// fakeLinkService — тестовая реализация интерфейса LinkService.
//
// Она не запускает PostgreSQL, Redis и настоящий service.
//
// Каждый тест сам задаёт поведение нужного метода
// через функцию shortenFunc, resolveFunc или statsFunc.
type fakeLinkService struct {
	shortenFunc func(
		ctx context.Context,
		longURL string,
	) (domain.Link, error)

	resolveFunc func(
		ctx context.Context,
		code string,
	) (string, error)

	statsFunc func(
		ctx context.Context,
		code string,
	) (domain.Link, error)
}

// Shorten реализует service.LinkService.
func (f *fakeLinkService) Shorten(
	ctx context.Context,
	longURL string,
) (domain.Link, error) {
	if f.shortenFunc == nil {
		panic("fakeLinkService.shortenFunc is not configured")
	}

	return f.shortenFunc(ctx, longURL)
}

// Resolve реализует service.LinkService.
func (f *fakeLinkService) Resolve(
	ctx context.Context,
	code string,
) (string, error) {
	if f.resolveFunc == nil {
		panic("fakeLinkService.resolveFunc is not configured")
	}

	return f.resolveFunc(ctx, code)
}

// Stats реализует service.LinkService.
func (f *fakeLinkService) Stats(
	ctx context.Context,
	code string,
) (domain.Link, error) {
	if f.statsFunc == nil {
		panic("fakeLinkService.statsFunc is not configured")
	}

	return f.statsFunc(ctx, code)
}

func TestLinkHandler_Shorten_Success(t *testing.T) {
	// Счётчик нужен, чтобы проверить:
	// service был вызван ровно один раз.
	serviceCalls := 0

	fakeService := &fakeLinkService{
		shortenFunc: func(
			ctx context.Context,
			longURL string,
		) (domain.Link, error) {
			serviceCalls++

			// Проверяем, что handler передал в service
			// URL из JSON-запроса.
			if longURL != "https://golang.org" {
				t.Fatalf(
					"expected URL %q, got %q",
					"https://golang.org",
					longURL,
				)
			}

			// Имитируем успешно созданную ссылку.
			return domain.Link{
				ID:        1,
				ShortCode: "abc123",
				LongURL:   longURL,
				Clicks:    0,
				CreatedAt: time.Now(),
			}, nil
		},
	}

	// Создаём handler с тестовым service.
	handler := NewLinkHandler(
		fakeService,
		"http://localhost:8080",
	)

	// Создаём маршрутизатор со всеми маршрутами.
	router := NewRouter(handler)

	// Формируем JSON-тело запроса.
	requestBody := strings.NewReader(
		`{"url":"https://golang.org"}`,
	)

	// Создаём тестовый HTTP-запрос.
	request := httptest.NewRequest(
		http.MethodPost,
		"/shorten",
		requestBody,
	)

	request.Header.Set(
		"Content-Type",
		"application/json",
	)

	// ResponseRecorder сохраняет:
	//
	//   - статус;
	//   - заголовки;
	//   - тело ответа.
	responseRecorder := httptest.NewRecorder()

	// Передаём запрос в router.
	router.ServeHTTP(
		responseRecorder,
		request,
	)

	// Проверяем HTTP 201 Created.
	if responseRecorder.Code != http.StatusCreated {
		t.Fatalf(
			"expected status %d, got %d; body: %s",
			http.StatusCreated,
			responseRecorder.Code,
			responseRecorder.Body.String(),
		)
	}

	if serviceCalls != 1 {
		t.Fatalf(
			"expected service call once, got %d",
			serviceCalls,
		)
	}

	// Декодируем JSON-ответ.
	var response shortenResponse

	if err := json.NewDecoder(
		responseRecorder.Body,
	).Decode(&response); err != nil {
		t.Fatalf(
			"decode response body: %v",
			err,
		)
	}

	expectedShortURL := "http://localhost:8080/abc123"

	if response.ShortURL != expectedShortURL {
		t.Fatalf(
			"expected short URL %q, got %q",
			expectedShortURL,
			response.ShortURL,
		)
	}

	// Проверяем Content-Type ответа.
	contentType := responseRecorder.Header().Get(
		"Content-Type",
	)

	if contentType != "application/json; charset=utf-8" {
		t.Fatalf(
			"unexpected Content-Type: %q",
			contentType,
		)
	}
}

func TestLinkHandler_Shorten_InvalidJSON(t *testing.T) {
	serviceCalls := 0

	fakeService := &fakeLinkService{
		shortenFunc: func(
			ctx context.Context,
			longURL string,
		) (domain.Link, error) {
			serviceCalls++
			return domain.Link{}, nil
		},
	}

	handler := NewLinkHandler(
		fakeService,
		"http://localhost:8080",
	)

	router := NewRouter(handler)

	// JSON некорректен:
	// отсутствует закрывающая фигурная скобка.
	requestBody := strings.NewReader(
		`{"url":"https://golang.org"`,
	)

	request := httptest.NewRequest(
		http.MethodPost,
		"/shorten",
		requestBody,
	)

	request.Header.Set(
		"Content-Type",
		"application/json",
	)

	responseRecorder := httptest.NewRecorder()

	router.ServeHTTP(
		responseRecorder,
		request,
	)

	if responseRecorder.Code != http.StatusBadRequest {
		t.Fatalf(
			"expected status %d, got %d",
			http.StatusBadRequest,
			responseRecorder.Code,
		)
	}

	// Некорректный JSON не должен попасть в service.
	if serviceCalls != 0 {
		t.Fatalf(
			"expected service not to be called, got %d calls",
			serviceCalls,
		)
	}

	var response errorResponse

	if err := json.NewDecoder(
		responseRecorder.Body,
	).Decode(&response); err != nil {
		t.Fatalf(
			"decode error response: %v",
			err,
		)
	}

	if response.Error != "request body contains invalid JSON" {
		t.Fatalf(
			"unexpected error message: %q",
			response.Error,
		)
	}
}

func TestLinkHandler_Shorten_InvalidURL(t *testing.T) {
	fakeService := &fakeLinkService{
		shortenFunc: func(
			ctx context.Context,
			longURL string,
		) (domain.Link, error) {
			// Имитируем ошибку service с дополнительным контекстом.
			//
			// errors.Is всё равно должен найти
			// domain.ErrInvalidURL внутри цепочки.
			return domain.Link{}, fmt.Errorf(
				"validate URL: %w",
				domain.ErrInvalidURL,
			)
		},
	}

	handler := NewLinkHandler(
		fakeService,
		"http://localhost:8080",
	)

	router := NewRouter(handler)

	request := httptest.NewRequest(
		http.MethodPost,
		"/shorten",
		strings.NewReader(`{"url":"golang.org"}`),
	)

	request.Header.Set(
		"Content-Type",
		"application/json",
	)

	responseRecorder := httptest.NewRecorder()

	router.ServeHTTP(
		responseRecorder,
		request,
	)

	if responseRecorder.Code != http.StatusBadRequest {
		t.Fatalf(
			"expected status %d, got %d",
			http.StatusBadRequest,
			responseRecorder.Code,
		)
	}

	var response errorResponse

	if err := json.NewDecoder(
		responseRecorder.Body,
	).Decode(&response); err != nil {
		t.Fatalf(
			"decode error response: %v",
			err,
		)
	}

	if response.Error != "invalid URL" {
		t.Fatalf(
			"expected invalid URL error, got %q",
			response.Error,
		)
	}
}

func TestLinkHandler_Redirect_Success(t *testing.T) {
	resolveCalls := 0

	fakeService := &fakeLinkService{
		resolveFunc: func(
			ctx context.Context,
			code string,
		) (string, error) {
			resolveCalls++

			// Проверяем, что PathValue корректно извлёк code.
			if code != "abc123" {
				t.Fatalf(
					"expected code %q, got %q",
					"abc123",
					code,
				)
			}

			return "https://golang.org", nil
		},
	}

	handler := NewLinkHandler(
		fakeService,
		"http://localhost:8080",
	)

	router := NewRouter(handler)

	request := httptest.NewRequest(
		http.MethodGet,
		"/abc123",
		nil,
	)

	responseRecorder := httptest.NewRecorder()

	router.ServeHTTP(
		responseRecorder,
		request,
	)

	// Проверяем HTTP 302 Found.
	if responseRecorder.Code != http.StatusFound {
		t.Fatalf(
			"expected status %d, got %d; body: %s",
			http.StatusFound,
			responseRecorder.Code,
			responseRecorder.Body.String(),
		)
	}

	if resolveCalls != 1 {
		t.Fatalf(
			"expected Resolve once, got %d calls",
			resolveCalls,
		)
	}

	// http.Redirect записывает адрес перехода
	// в заголовок Location.
	location := responseRecorder.Header().Get("Location")

	if location != "https://golang.org" {
		t.Fatalf(
			"expected Location %q, got %q",
			"https://golang.org",
			location,
		)
	}
}

func TestLinkHandler_Redirect_NotFound(t *testing.T) {
	fakeService := &fakeLinkService{
		resolveFunc: func(
			ctx context.Context,
			code string,
		) (string, error) {
			return "", fmt.Errorf(
				"resolve code: %w",
				domain.ErrLinkNotFound,
			)
		},
	}

	handler := NewLinkHandler(
		fakeService,
		"http://localhost:8080",
	)

	router := NewRouter(handler)

	request := httptest.NewRequest(
		http.MethodGet,
		"/abc123",
		nil,
	)

	responseRecorder := httptest.NewRecorder()

	router.ServeHTTP(
		responseRecorder,
		request,
	)

	if responseRecorder.Code != http.StatusNotFound {
		t.Fatalf(
			"expected status %d, got %d",
			http.StatusNotFound,
			responseRecorder.Code,
		)
	}

	var response errorResponse

	if err := json.NewDecoder(
		responseRecorder.Body,
	).Decode(&response); err != nil {
		t.Fatalf(
			"decode error response: %v",
			err,
		)
	}

	if response.Error != "link not found" {
		t.Fatalf(
			"expected link not found, got %q",
			response.Error,
		)
	}
}

func TestLinkHandler_Stats_Success(t *testing.T) {
	createdAt := time.Date(
		2026,
		time.July,
		14,
		12,
		0,
		0,
		0,
		time.UTC,
	)

	fakeService := &fakeLinkService{
		statsFunc: func(
			ctx context.Context,
			code string,
		) (domain.Link, error) {
			if code != "abc123" {
				t.Fatalf(
					"expected code %q, got %q",
					"abc123",
					code,
				)
			}

			return domain.Link{
				ID:        10,
				ShortCode: "abc123",
				LongURL:   "https://golang.org",
				Clicks:    25,
				CreatedAt: createdAt,
			}, nil
		},
	}

	handler := NewLinkHandler(
		fakeService,
		"http://localhost:8080",
	)

	router := NewRouter(handler)

	request := httptest.NewRequest(
		http.MethodGet,
		"/stats/abc123",
		nil,
	)

	responseRecorder := httptest.NewRecorder()

	router.ServeHTTP(
		responseRecorder,
		request,
	)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf(
			"expected status %d, got %d; body: %s",
			http.StatusOK,
			responseRecorder.Code,
			responseRecorder.Body.String(),
		)
	}

	var response statsResponse

	if err := json.NewDecoder(
		responseRecorder.Body,
	).Decode(&response); err != nil {
		t.Fatalf(
			"decode stats response: %v",
			err,
		)
	}

	if response.ShortCode != "abc123" {
		t.Fatalf(
			"expected code abc123, got %q",
			response.ShortCode,
		)
	}

	if response.LongURL != "https://golang.org" {
		t.Fatalf(
			"unexpected long URL: %q",
			response.LongURL,
		)
	}

	if response.Clicks != 25 {
		t.Fatalf(
			"expected 25 clicks, got %d",
			response.Clicks,
		)
	}

	if !response.CreatedAt.Equal(createdAt) {
		t.Fatalf(
			"expected CreatedAt %v, got %v",
			createdAt,
			response.CreatedAt,
		)
	}
}

func TestLinkHandler_Shorten_RequestBodyTooLarge(t *testing.T) {
	serviceCalls := 0

	fakeService := &fakeLinkService{
		shortenFunc: func(
			ctx context.Context,
			longURL string,
		) (domain.Link, error) {
			serviceCalls++
			return domain.Link{}, nil
		},
	}

	handler := NewLinkHandler(
		fakeService,
		"http://localhost:8080",
	)

	router := NewRouter(handler)

	// Создаём тело больше установленного лимита.
	//
	// maxRequestBodySize + 1 гарантированно превышает 1 МБ.
	largeBody := bytes.Repeat(
		[]byte("a"),
		maxRequestBodySize+1,
	)

	request := httptest.NewRequest(
		http.MethodPost,
		"/shorten",
		bytes.NewReader(largeBody),
	)

	request.Header.Set(
		"Content-Type",
		"application/json",
	)

	responseRecorder := httptest.NewRecorder()

	router.ServeHTTP(
		responseRecorder,
		request,
	)

	if responseRecorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf(
			"expected status %d, got %d; body: %s",
			http.StatusRequestEntityTooLarge,
			responseRecorder.Code,
			responseRecorder.Body.String(),
		)
	}

	if serviceCalls != 0 {
		t.Fatalf(
			"expected service not to be called, got %d calls",
			serviceCalls,
		)
	}
}
