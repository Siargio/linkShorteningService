package config

import (
	"strings"
	"testing"
	"time"
)

// setValidEnvironment устанавливает корректное тестовое окружение.
//
// t.Setenv автоматически вернёт старые значения
// после завершения теста.
func setValidEnvironment(t *testing.T) {
	t.Helper()

	t.Setenv(
		"DATABASE_URL",
		"postgres://postgres:postgres@localhost:5432/shortener?sslmode=disable",
	)

	t.Setenv(
		"HTTP_PORT",
		"8080",
	)

	t.Setenv(
		"BASE_URL",
		"http://localhost:8080/",
	)

	t.Setenv(
		"REDIS_ADDR",
		"localhost:6379",
	)

	t.Setenv(
		"REDIS_PASSWORD",
		"",
	)

	t.Setenv(
		"REDIS_DB",
		"0",
	)

	t.Setenv(
		"REDIS_TTL",
		"24h",
	)

	t.Setenv(
		"KAFKA_BROKERS",
		"localhost:29092",
	)

	t.Setenv(
		"KAFKA_TOPIC",
		"link-events",
	)
}

func TestLoadFromEnvironment_Success(t *testing.T) {
	// Подготавливаем корректные переменные окружения.
	setValidEnvironment(t)

	// Загружаем конфигурацию без чтения .env.
	cfg, err := loadFromEnvironment()
	if err != nil {
		t.Fatalf(
			"loadFromEnvironment returned error: %v",
			err,
		)
	}

	if cfg.HTTPPort != 8080 {
		t.Fatalf(
			"expected HTTP port 8080, got %d",
			cfg.HTTPPort,
		)
	}

	// Завершающий слеш должен быть удалён.
	if cfg.BaseURL != "http://localhost:8080" {
		t.Fatalf(
			"unexpected BASE_URL: %q",
			cfg.BaseURL,
		)
	}

	if cfg.RedisAddress != "localhost:6379" {
		t.Fatalf(
			"unexpected Redis address: %q",
			cfg.RedisAddress,
		)
	}

	if cfg.RedisDB != 0 {
		t.Fatalf(
			"expected Redis DB 0, got %d",
			cfg.RedisDB,
		)
	}

	if cfg.RedisTTL != 24*time.Hour {
		t.Fatalf(
			"expected Redis TTL %v, got %v",
			24*time.Hour,
			cfg.RedisTTL,
		)
	}

	if len(cfg.KafkaBrokers) != 1 {
		t.Fatalf(
			"expected one Kafka broker, got %d",
			len(cfg.KafkaBrokers),
		)
	}

	if cfg.KafkaBrokers[0] != "localhost:29092" {
		t.Fatalf(
			"unexpected Kafka broker: %q",
			cfg.KafkaBrokers[0],
		)
	}

	if cfg.KafkaTopic != "link-events" {
		t.Fatalf(
			"unexpected Kafka topic: %q",
			cfg.KafkaTopic,
		)
	}
}

func TestLoadFromEnvironment_MissingDatabaseURL(
	t *testing.T,
) {
	setValidEnvironment(t)

	// Имитируем отсутствие обязательной переменной.
	t.Setenv("DATABASE_URL", "")

	_, err := loadFromEnvironment()
	if err == nil {
		t.Fatal(
			"expected an error, got nil",
		)
	}

	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf(
			"expected DATABASE_URL error, got %v",
			err,
		)
	}
}

func TestLoadFromEnvironment_InvalidHTTPPort(
	t *testing.T,
) {
	setValidEnvironment(t)

	t.Setenv(
		"HTTP_PORT",
		"not-a-number",
	)

	_, err := loadFromEnvironment()
	if err == nil {
		t.Fatal(
			"expected an error, got nil",
		)
	}

	if !strings.Contains(err.Error(), "HTTP_PORT") {
		t.Fatalf(
			"expected HTTP_PORT error, got %v",
			err,
		)
	}
}

func TestLoadFromEnvironment_InvalidRedisTTL(
	t *testing.T,
) {
	setValidEnvironment(t)

	t.Setenv(
		"REDIS_TTL",
		"tomorrow",
	)

	_, err := loadFromEnvironment()
	if err == nil {
		t.Fatal(
			"expected an error, got nil",
		)
	}

	if !strings.Contains(err.Error(), "REDIS_TTL") {
		t.Fatalf(
			"expected REDIS_TTL error, got %v",
			err,
		)
	}
}

func TestNormalizeBaseURL_InvalidScheme(
	t *testing.T,
) {
	_, err := normalizeBaseURL(
		"ftp://localhost:8080",
	)

	if err == nil {
		t.Fatal(
			"expected an error, got nil",
		)
	}
}
