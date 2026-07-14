package cache

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// linkKeyPrefix — префикс всех ключей коротких ссылок в Redis.
	// Например, для короткого кода: abc123
	// в Redis будет создан ключ: link:abc123
	linkKeyPrefix = "link:"
)

var (
	// ErrCacheMiss означает, что запрошенного ключа нет в кеше.
	// Это не авария Redis.
	// В этом случае service должен обратиться к PostgreSQL.
	ErrCacheMiss = errors.New("cache miss")

	// ErrInvalidRedisAddress означает, что конструктору Redis-кеша
	// передали пустой адрес.
	ErrInvalidRedisAddress = errors.New("invalid Redis address")

	// ErrInvalidRedisTTL означает, что TTL имеет некорректное значение.
	// В нашем проекте значение должно быть больше нуля, например: 24h
	ErrInvalidRedisTTL = errors.New("invalid Redis TTL")
)

// LinkCache описывает операции кеширования коротких ссылок.
// Service зависит именно от этого интерфейса,
type LinkCache interface {
	// Get получает длинный URL по короткому коду.
	//
	// Возможные результаты:
	//   - longURL, nil — значение найдено;
	//   - "", ErrCacheMiss — значения в кеше нет;
	//   - "", другая ошибка — Redis недоступен или команда завершилась ошибкой.
	Get(ctx context.Context, code string) (string, error)

	// Set сохраняет соответствие короткого кода и длинного URL.
	//
	// Ключ будет автоматически сохранён с TTL,
	// переданным при создании RedisCache.
	Set(ctx context.Context, code, longURL string) error
}

// RedisCache — реализация LinkCache на базе Redis.
type RedisCache struct {
	// client — клиент библиотеки go-redis.
	// Он управляет подключениями к Redis
	// и содержит внутренний пул соединений.
	client *redis.Client

	// ttl — срок жизни одного ключа.
	// Например: 24 * time.Hour
	// После истечения TTL Redis автоматически удалит ключ.
	ttl time.Duration
}

// Проверка, что RedisCache реализует LinkCache.
var _ LinkCache = (*RedisCache)(nil)

// NewRedisCache создаёт Redis-кеш.
func NewRedisCache(
	address string,
	password string,
	db int,
	ttl time.Duration,
) (*RedisCache, error) {
	// Удаляем случайные пробелы вокруг адреса.
	address = strings.TrimSpace(address)

	// Redis-клиент не сможет работать без адреса.
	if address == "" {
		return nil, ErrInvalidRedisAddress
	}

	// Для нашего кеша всегда используем положительный TTL.
	//
	// Значение 0 в Redis означает хранение без срока действия,
	// но для коротких ссылок мы сознательно ограничиваем
	// время жизни кешированной записи.
	if ttl <= 0 {
		return nil, ErrInvalidRedisTTL
	}

	// Создаём Redis-клиент.
	client := redis.NewClient(&redis.Options{
		Addr:     address,
		Password: password,
		DB:       db,
	})

	return &RedisCache{
		client: client,
		ttl:    ttl,
	}, nil
}

// Ping проверяет, доступен ли Redis.
//
// Конструктор redis.NewClient не обязан сразу устанавливать
// физическое соединение, поэтому после создания клиента
// полезно выполнить команду PING.
//
// При успешном соединении Redis ответит PONG.
func (c *RedisCache) Ping(ctx context.Context) error {
	if err := c.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("ping Redis: %w", err)
	}

	return nil
}

// Get — получает длинную ссылку по короткому коду.
func (c *RedisCache) Get(
	ctx context.Context,
	code string,
) (string, error) {
	// Формируем Redis-ключ.
	key := buildLinkKey(code)

	// Выполняем Redis-команду:
	//	GET link:abc123
	// Result возвращает:
	//   - строковое значение;
	//   - ошибку выполнения команды.
	longURL, err := c.client.Get(ctx, key).Result()
	if err != nil {
		// redis.Nil — ключ не найден
		if errors.Is(err, redis.Nil) {
			return "", ErrCacheMiss
		}

		// Любая другая ошибка означает проблему обращения к Redis:
		//
		//   - Redis недоступен;
		//   - соединение разорвано;
		//   - context отменён;
		//   - команда завершилась ошибкой.
		return "", fmt.Errorf(
			"get link %q from Redis: %w",
			code,
			err,
		)
	}

	return longURL, nil
}

// Set — сохраняет ссылку в Redis с TTL.
func (c *RedisCache) Set(
	ctx context.Context,
	code string,
	longURL string,
) error {
	// Формируем ключ с единым префиксом.
	key := buildLinkKey(code)

	// Выполняем команду, эквивалентную:
	//
	//	SET link:abc123 https://golang.org EX <ttl>
	//
	// Благодаря TTL ключ не будет храниться бесконечно.
	if err := c.client.Set(
		ctx,
		key,
		longURL,
		c.ttl,
	).Err(); err != nil {
		return fmt.Errorf(
			"set link %q in Redis: %w",
			code,
			err,
		)
	}

	return nil
}

// Close — закрывает соединение с Redis.
func (c *RedisCache) Close() error {
	if err := c.client.Close(); err != nil {
		return fmt.Errorf("close Redis client: %w", err)
	}

	return nil
}

// buildLinkKey — формирует ключ для Redis.
func buildLinkKey(code string) string {
	return linkKeyPrefix + code
}
