package handler

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/Siargio/linkShorteningService/internal/domain"
	linkservice "github.com/Siargio/linkShorteningService/internal/service"
)

const (
	// maxRequestBodySize ограничивает размер тела POST-запроса.
	// 1 << 20 — это 1 048 576 байт, то есть 1 МБ.
	maxRequestBodySize = 1 << 20
)

var (
	// errMultipleJSONValues возвращается, если клиент
	// отправил несколько JSON-объектов в одном запросе.
	// Endpoint ожидает ровно один JSON-объект.
	errMultipleJSONValues = errors.New(
		"request body must contain only one JSON object",
	)
)

// LinkHandler содержит HTTP-обработчики сервиса ссылок.
//
// Handler отвечает только за HTTP-уровень:
//
//   - прочитать HTTP-запрос;
//   - декодировать JSON;
//   - получить параметр из URL;
//   - вызвать service;
//   - преобразовать ошибку service в HTTP-статус;
//   - сформировать JSON или redirect.
type LinkHandler struct {
	// service содержит бизнес-логику приложения.
	service linkservice.LinkService

	// baseURL используется для формирования полного короткого URL.
	// Например:
	//	baseURL  = "http://localhost:8080"
	//	code     = "abc123"
	//	shortURL = "http://localhost:8080/abc123"
	baseURL string
}

// NewLinkHandler создаёт HTTP-handler.
// strings.TrimRight удаляет слеши в конце BASE_URL.
func NewLinkHandler(
	service linkservice.LinkService,
	baseURL string,
) *LinkHandler {
	return &LinkHandler{
		service: service,
		baseURL: strings.TrimRight(baseURL, "/"),
	}
}

// NewRouter регистрирует все маршруты приложения.
func NewRouter(handler *LinkHandler) http.Handler {
	// Создаём отдельный маршрутизатор приложения.
	mux := http.NewServeMux()

	// Создание новой короткой ссылки.
	mux.HandleFunc("POST /shorten", handler.Shorten)

	// Получение статистики.
	// Значение {code} будет доступно через:
	//	r.PathValue("code")
	mux.HandleFunc("GET /stats/{code}", handler.Stats)

	// Переход по короткой ссылке.
	mux.HandleFunc("GET /{code}", handler.Redirect)

	return mux
}

// shortenRequest описывает JSON,
// который клиент отправляет в POST /shorten.
//
// Пример:
//
//	{
//	    "url": "https://golang.org"
//	}
type shortenRequest struct {
	URL string `json:"url"`
}

// shortenResponse описывает успешный ответ POST /shorten.
// Пример:
//
//	{
//	    "short_url": "http://localhost:8080/abc123"
//	}
type shortenResponse struct {
	ShortURL string `json:"short_url"`
}

// statsResponse описывает публичный ответ GET /stats/{code}.
type statsResponse struct {
	ShortCode string    `json:"short_code"`
	LongURL   string    `json:"long_url"`
	Clicks    int32     `json:"clicks"`
	CreatedAt time.Time `json:"created_at"`
}

// errorResponse задаёт единый формат HTTP-ошибок.
type errorResponse struct {
	Error string `json:"error"`
}

// Shorten обрабатывает:
//
//	POST /shorten
//
// Алгоритм:
//
//  1. Проверить Content-Type.
//  2. Прочитать JSON.
//  3. Передать URL в service.Shorten.
//  4. Сформировать полный короткий URL.
//  5. Вернуть HTTP 201 Created.
func (h *LinkHandler) Shorten(
	w http.ResponseWriter,
	r *http.Request,
) {
	// Проверяем Content-Type только если клиент его передал.
	contentType := r.Header.Get("Content-Type")

	if contentType != "" {
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || mediaType != "application/json" {
			writeJSON(
				w,
				http.StatusUnsupportedMediaType,
				errorResponse{
					Error: "Content-Type must be application/json",
				},
			)

			return
		}
	}

	// Структура, в которую декодируем JSON-запрос.
	var request shortenRequest

	// Декодируем тело запроса.
	//
	// decodeJSONBody дополнительно:
	//
	//   - ограничивает размер тела;
	//   - запрещает неизвестные поля;
	//   - запрещает несколько JSON-объектов.
	if err := decodeJSONBody(w, r, &request); err != nil {
		writeJSONDecodeError(w, err)
		return
	}

	// Передаём URL в бизнес-логику.
	//
	// Handler не валидирует сам URL.
	// Это делает domain/service.
	link, err := h.service.Shorten(r.Context(), request.URL)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	// Формируем полный короткий URL.
	// Например:
	//	h.baseURL     = "http://localhost:8080"
	//	link.ShortCode = "abc123"
	//
	// Результат:
	//	http://localhost:8080/abc123
	shortURL := h.baseURL + "/" + link.ShortCode

	// Возвращаем HTTP 201 Created
	writeJSON(w, http.StatusCreated, shortenResponse{ShortURL: shortURL})
}

// Redirect обрабатывает:
//
//	GET /{code}
//
// Например:
//
//	GET /abc123
//
// Алгоритм:
//  1. Получить code из пути.
//  2. Вызвать service.Resolve.
//  3. Service найдёт URL и увеличит clicks.
//  4. Вернуть HTTP 302 Redirect.
func (h *LinkHandler) Redirect(
	w http.ResponseWriter,
	r *http.Request,
) {
	// Получаем значение параметра {code}
	// из шаблона маршрута GET /{code}.
	code := r.PathValue("code")

	// Получаем длинный URL через service.
	//
	// Service самостоятельно решит:
	//
	//   - искать ли URL в Redis;
	//   - обращаться ли в PostgreSQL;
	//   - нужно ли заполнить кеш;
	//   - как увеличить clicks.
	longURL, err := h.service.Resolve(r.Context(), code)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	// Отправляем клиенту HTTP 302 Found.
	//
	// В заголовке Location будет указан длинный URL.
	//
	// Браузер или curl -L после этого перейдёт
	// по адресу longURL.
	http.Redirect(w, r, longURL, http.StatusFound)
}

// Stats обрабатывает:
//
//	GET /stats/{code}
//
// Например:
//
//	GET /stats/abc123
//
// Этот endpoint не увеличивает счётчик переходов.
func (h *LinkHandler) Stats(
	w http.ResponseWriter,
	r *http.Request,
) {
	// Получаем short_code из пути.
	code := r.PathValue("code")

	// Получаем актуальные данные из service.
	link, err := h.service.Stats(r.Context(), code)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	// Преобразуем domain.Link в публичный HTTP DTO.
	//
	// Это позволяет не отдавать domain-модель напрямую
	// и независимо изменять API и внутреннюю структуру приложения.
	response := statsResponse{
		ShortCode: link.ShortCode,
		LongURL:   link.LongURL,
		Clicks:    link.Clicks,
		CreatedAt: link.CreatedAt.UTC(),
	}

	writeJSON(w, http.StatusOK, response)
}

// decodeJSONBody безопасно декодирует JSON-запрос.
func decodeJSONBody(
	w http.ResponseWriter,
	r *http.Request,
	destination any,
) error {
	// Ограничиваем количество байтов,
	// которое handler может прочитать из тела.
	//
	// Если клиент отправит больше maxRequestBodySize,
	// чтение завершится ошибкой *http.MaxBytesError.
	r.Body = http.MaxBytesReader(
		w,
		r.Body,
		maxRequestBodySize,
	)

	// Создаём потоковый JSON-декодер.
	decoder := json.NewDecoder(r.Body)

	// Запрещаем поля, которых нет в структуре DTO.
	// Например, такой запрос будет отклонён:
	//	{
	//	    "url": "https://golang.org",
	//	    "unknown": true
	//	}
	//
	// Это помогает обнаруживать опечатки в API-запросах.
	decoder.DisallowUnknownFields()

	// Декодируем первый JSON-объект.
	if err := decoder.Decode(destination); err != nil {
		return err
	}

	// Проверяем, что после первого объекта
	// в теле запроса больше нет JSON-данных.
	var extraJSONValue any

	err := decoder.Decode(&extraJSONValue)

	// Если получили io.EOF, значит в теле был
	// ровно один JSON-объект — это корректный запрос.
	if errors.Is(err, io.EOF) {
		return nil
	}

	// Если ошибки нет, значит успешно прочитан
	// ещё один JSON-объект.
	if err == nil {
		return errMultipleJSONValues
	}

	// Возвращаем ошибку второго декодирования.
	return err
}

// writeJSONDecodeError преобразует ошибку JSON-декодирования
// в понятный HTTP-ответ.
func writeJSONDecodeError(
	w http.ResponseWriter,
	err error,
) {
	// Проверяем, что тело запроса превысило лимит.
	var maxBytesError *http.MaxBytesError

	if errors.As(err, &maxBytesError) {
		writeJSON(
			w,
			http.StatusRequestEntityTooLarge,
			errorResponse{
				Error: "request body is too large",
			},
		)

		return
	}

	// io.EOF при первом Decode означает,
	// что тело запроса вообще пустое.
	if errors.Is(err, io.EOF) {
		writeJSON(
			w,
			http.StatusBadRequest,
			errorResponse{
				Error: "request body must not be empty",
			},
		)

		return
	}

	// io.ErrUnexpectedEOF означает, что JSON начался,
	// но неожиданно закончился.
	//
	// Например:
	//
	//	{"url":"https://golang.org"
	//
	// В запросе отсутствует закрывающая фигурная скобка.
	// Для json.Decoder такая ошибка не обязательно имеет тип
	// *json.SyntaxError, поэтому проверяем её отдельно.
	if errors.Is(err, io.ErrUnexpectedEOF) {
		writeJSON(
			w,
			http.StatusBadRequest,
			errorResponse{
				Error: "request body contains invalid JSON",
			},
		)

		return
	}

	// Проверяем синтаксическую ошибку JSON.
	// Например:
	//
	//	{"url": "https://golang.org"
	//
	// Здесь отсутствует закрывающая фигурная скобка.
	var syntaxError *json.SyntaxError

	if errors.As(err, &syntaxError) {
		writeJSON(
			w,
			http.StatusBadRequest,
			errorResponse{
				Error: "request body contains invalid JSON",
			},
		)

		return
	}

	// Проверяем несовпадение типа JSON-поля.
	//
	// Например:
	//	{"url": 123}
	//
	// Но URL должен быть строкой.
	var unmarshalTypeError *json.UnmarshalTypeError

	if errors.As(err, &unmarshalTypeError) {
		writeJSON(
			w,
			http.StatusBadRequest,
			errorResponse{
				Error: "request JSON field has invalid type",
			},
		)

		return
	}

	// DisallowUnknownFields возвращает ошибку,
	// текст которой начинается с "json: unknown field".
	if strings.HasPrefix(
		err.Error(),
		"json: unknown field ",
	) {
		writeJSON(
			w,
			http.StatusBadRequest,
			errorResponse{
				Error: err.Error(),
			},
		)

		return
	}

	// Проверяем, что клиент отправил несколько объектов.
	if errors.Is(err, errMultipleJSONValues) {
		writeJSON(
			w,
			http.StatusBadRequest,
			errorResponse{
				Error: errMultipleJSONValues.Error(),
			},
		)

		return
	}

	// На все остальные ошибки декодирования
	// возвращаем универсальный ответ.
	writeJSON(
		w,
		http.StatusBadRequest,
		errorResponse{
			Error: "invalid request body",
		},
	)
}

// writeServiceError преобразует доменные ошибки
// в HTTP-статусы.
//
// Важно: клиент не должен получать внутренние ошибки:
//
//	dial tcp: connection refused
//	SQLSTATE 23505
//	context deadline exceeded
//
// Такие подробности позже будем записывать в лог,
// а клиенту возвращать безопасное сообщение.
func writeServiceError(
	w http.ResponseWriter,
	err error,
) {
	switch {
	// Некорректная длинная ссылка:
	//
	//	POST /shorten
	//	{"url":"golang.org"}
	case errors.Is(err, domain.ErrInvalidURL):
		writeJSON(
			w,
			http.StatusBadRequest,
			errorResponse{
				Error: "invalid URL",
			},
		)

	// Некорректный короткий код:
	//	GET /abc-123
	case errors.Is(err, domain.ErrInvalidShortCode):
		writeJSON(
			w,
			http.StatusBadRequest,
			errorResponse{
				Error: "invalid short code",
			},
		)

	// Ссылки с таким кодом нет.
	case errors.Is(err, domain.ErrLinkNotFound):
		writeJSON(
			w,
			http.StatusNotFound,
			errorResponse{
				Error: "link not found",
			},
		)

	// Все остальные ошибки считаются внутренними.
	//
	// Например:
	//
	//   - PostgreSQL недоступен;
	//   - не удалось увеличить clicks;
	//   - генератор случайных значений завершился ошибкой;
	//   - исчерпаны попытки генерации short_code.
	default:
		writeJSON(
			w,
			http.StatusInternalServerError,
			errorResponse{
				Error: "internal server error",
			},
		)
	}
}

// writeJSON записывает JSON-ответ.
//
// Все JSON-ответы приложения проходят через эту функцию,
// поэтому Content-Type и форматирование определены единообразно.
func writeJSON(
	w http.ResponseWriter,
	status int,
	data any,
) {
	// Заголовки необходимо установить
	// до вызова WriteHeader.
	w.Header().Set(
		"Content-Type",
		"application/json; charset=utf-8",
	)

	// Записываем HTTP-статус.
	w.WriteHeader(status)

	// Создаём JSON-кодировщик,
	// который пишет результат прямо в ResponseWriter.
	encoder := json.NewEncoder(w)

	// Не экранируем символы <, > и & как Unicode-последовательности.
	// Для URL это делает JSON немного более читаемым.
	encoder.SetEscapeHTML(false)

	// После WriteHeader мы уже не можем изменить HTTP-статус,
	// если Encode завершится ошибкой.
	//
	// Позже ошибку кодирования можно будет записывать в logger.
	_ = encoder.Encode(data)
}
