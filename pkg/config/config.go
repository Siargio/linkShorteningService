package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

const (
	// Значения по умолчанию используются,
	// если соответствующая переменная окружения отсутствует.
	//
	// DATABASE_URL не имеет значения по умолчанию,
	// потому что приложение не может работать без PostgreSQL.
	defaultHTTPPort     = 8080
	defaultBaseURL      = "http://localhost:8080"
	defaultRedisAddress = "localhost:6379"
	defaultRedisDB      = 0
	defaultRedisTTL     = 24 * time.Hour

	// Номер TCP-порта должен находиться в диапазоне 1–65535.
	minPort = 1
	maxPort = 65535
)

// Config содержит все настройки приложения.
//
// Остальные пакеты не должны самостоятельно обращаться к os.Getenv.
// Конфигурация загружается в одном месте, валидируется
// и затем передаётся через зависимости.
type Config struct {
	// HTTPPort — порт, на котором будет запущен HTTP-сервер.
	// Например: 8080
	HTTPPort int

	// BaseURL — внешний адрес нашего сервиса.
	// Он используется при формировании короткой ссылки:
	//	http://localhost:8080/abc123
	BaseURL string

	// DatabaseURL — строка подключения к PostgreSQL.
	// Например:
	//	postgres://postgres:postgres@localhost:5432/shortener?sslmode=disable
	DatabaseURL string

	// RedisAddress — адрес Redis в формате host:port.
	// Например:
	//	localhost:6379
	RedisAddress string

	// RedisPassword — пароль Redis.
	// В локальном окружении пароль обычно отсутствует,
	// поэтому значение может быть пустой строкой.
	RedisPassword string

	// RedisDB — номер логической базы Redis.
	// По умолчанию используется база 0.
	RedisDB int

	// RedisTTL — срок хранения одной ссылки в кеше.
	// Например:
	//	24h
	RedisTTL time.Duration
}

// Load загружает и валидирует конфигурацию.
//
// Порядок:
//
//  1. Попытаться загрузить локальный файл .env.
//  2. Прочитать переменные окружения.
//  3. Преобразовать строки в int и time.Duration.
//  4. Проверить обязательные значения.
//  5. Вернуть готовую Config.
func Load() (Config, error) {
	// godotenv.Load загружает переменные из файла .env.
	// В локальной разработке это удобно:
	//	DATABASE_URL=...
	//	REDIS_ADDR=...
	//
	// В Docker или production файла .env внутри контейнера
	// может не быть — переменные передаются самим окружением.
	//
	// Поэтому отсутствие файла .env не считаем ошибкой.
	if err := godotenv.Load(); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf(
			"load .env file: %w",
			err,
		)
	}

	// После возможной загрузки .env читаем окружение.
	return loadFromEnvironment()
}

// loadFromEnvironment читает конфигурацию из переменных окружения.
//
// Функция отделена от Load, чтобы её было удобно тестировать
// без создания настоящего .env-файла.
func loadFromEnvironment() (Config, error) {
	// DATABASE_URL обязателен.
	//
	// Без PostgreSQL приложение не сможет:
	//   - создавать ссылки;
	//   - находить ссылки;
	//   - хранить статистику.
	databaseURL := strings.TrimSpace(
		os.Getenv("DATABASE_URL"),
	)

	if databaseURL == "" {
		return Config{}, errors.New(
			"DATABASE_URL environment variable is required",
		)
	}

	// Читаем HTTP_PORT и преобразуем его в int.
	httpPort, err := parseIntegerEnvironment(
		"HTTP_PORT",
		defaultHTTPPort,
		minPort,
		maxPort,
	)
	if err != nil {
		return Config{}, err
	}

	// Получаем и проверяем BASE_URL.
	baseURL, err := normalizeBaseURL(
		getEnvironmentOrDefault(
			"BASE_URL",
			defaultBaseURL,
		),
	)
	if err != nil {
		return Config{}, err
	}

	// Адрес Redis имеет безопасное локальное значение по умолчанию.
	redisAddress := strings.TrimSpace(
		getEnvironmentOrDefault(
			"REDIS_ADDR",
			defaultRedisAddress,
		),
	)

	if redisAddress == "" {
		return Config{}, errors.New(
			"REDIS_ADDR must not be empty",
		)
	}

	// Пароль может быть пустым.
	redisPassword := os.Getenv("REDIS_PASSWORD")

	// Читаем номер логической базы Redis.
	// Redis DB не может быть отрицательной.
	redisDB, err := parseIntegerEnvironment(
		"REDIS_DB",
		defaultRedisDB,
		0,
		int(^uint(0)>>1),
	)
	if err != nil {
		return Config{}, err
	}

	// Читаем TTL.
	// time.ParseDuration понимает значения:
	//
	//	30s
	//	15m
	//	24h
	redisTTL, err := parseDurationEnvironment(
		"REDIS_TTL",
		defaultRedisTTL,
	)
	if err != nil {
		return Config{}, err
	}

	return Config{
		HTTPPort:      httpPort,
		BaseURL:       baseURL,
		DatabaseURL:   databaseURL,
		RedisAddress:  redisAddress,
		RedisPassword: redisPassword,
		RedisDB:       redisDB,
		RedisTTL:      redisTTL,
	}, nil
}

// getEnvironmentOrDefault возвращает значение переменной окружения.
// Если переменная отсутствует или содержит только пробелы, возвращается defaultValue.
func getEnvironmentOrDefault(name string, defaultValue string) string {
	value := strings.TrimSpace(
		os.Getenv(name),
	)

	if value == "" {
		return defaultValue
	}

	return value
}

// parseIntegerEnvironment читает целочисленную переменную окружения.
// Дополнительно проверяет минимальное и максимальное значение.
func parseIntegerEnvironment(
	name string,
	defaultValue int,
	minValue int,
	maxValue int,
) (int, error) {
	// Получаем строковое значение по умолчанию,
	// например "8080".
	rawValue := getEnvironmentOrDefault(
		name,
		strconv.Itoa(defaultValue),
	)

	// Преобразуем строку в int.
	value, err := strconv.Atoi(rawValue)
	if err != nil {
		return 0, fmt.Errorf(
			"%s must be an integer: %w",
			name,
			err,
		)
	}

	// Проверяем допустимый диапазон.
	if value < minValue || value > maxValue {
		return 0, fmt.Errorf(
			"%s must be between %d and %d",
			name,
			minValue,
			maxValue,
		)
	}

	return value, nil
}

// parseDurationEnvironment читает значение time.Duration.
// Например:
//
//	REDIS_TTL=24h
func parseDurationEnvironment(
	name string,
	defaultValue time.Duration,
) (time.Duration, error) {
	rawValue := getEnvironmentOrDefault(
		name,
		defaultValue.String(),
	)

	value, err := time.ParseDuration(rawValue)
	if err != nil {
		return 0, fmt.Errorf(
			"%s must be a valid duration: %w",
			name,
			err,
		)
	}

	// Нулевой или отрицательный TTL нам не подходит.
	if value <= 0 {
		return 0, fmt.Errorf(
			"%s must be greater than zero",
			name,
		)
	}

	return value, nil
}

// normalizeBaseURL проверяет внешний адрес сервиса и удаляет завершающий слеш.
func normalizeBaseURL(
	rawBaseURL string,
) (string, error) {
	// Удаляем пробелы и слеши справа.
	normalizedBaseURL := strings.TrimRight(
		strings.TrimSpace(rawBaseURL),
		"/",
	)

	if normalizedBaseURL == "" {
		return "", errors.New(
			"BASE_URL must not be empty",
		)
	}

	parsedURL, err := url.Parse(normalizedBaseURL)
	if err != nil {
		return "", fmt.Errorf(
			"parse BASE_URL: %w",
			err,
		)
	}

	// BASE_URL должен быть абсолютным URL.
	if parsedURL.Host == "" {
		return "", errors.New(
			"BASE_URL must contain a host",
		)
	}

	// Разрешаем только HTTP и HTTPS.
	switch strings.ToLower(parsedURL.Scheme) {
	case "http", "https":
		// Допустимые протоколы.
	default:
		return "", errors.New(
			"BASE_URL must use http or https",
		)
	}

	// Наш текущий router работает от корня сайта.
	// Поэтому значение вроде:
	//	https://example.com/shortener
	// пока не поддерживаем.
	if parsedURL.Path != "" && parsedURL.Path != "/" {
		return "", errors.New(
			"BASE_URL must not contain a path",
		)
	}

	if parsedURL.RawQuery != "" {
		return "", errors.New(
			"BASE_URL must not contain query parameters",
		)
	}

	if parsedURL.Fragment != "" {
		return "", errors.New(
			"BASE_URL must not contain a fragment",
		)
	}

	return normalizedBaseURL, nil
}
