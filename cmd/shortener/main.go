package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/Siargio/linkShorteningService/internal/handler"
	"github.com/Siargio/linkShorteningService/internal/repository"
	"github.com/Siargio/linkShorteningService/internal/service"
	linkcache "github.com/Siargio/linkShorteningService/pkg/cache"
	"github.com/Siargio/linkShorteningService/pkg/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// databaseStartupTimeout ограничивает время,
	// которое приложение ждёт подключения к PostgreSQL во время запуска.
	databaseStartupTimeout = 5 * time.Second

	// redisStartupTimeout ограничивает начальную проверку Redis.
	// Ошибка Redis не остановит приложение, но мы не хотим долго ждать недоступный кеш.
	redisStartupTimeout = 3 * time.Second

	// readHeaderTimeout ограничивает время чтения HTTP-заголовков.
	// Это защищает сервер от клиентов, которые отправляют заголовки очень медленно.
	readHeaderTimeout = 5 * time.Second

	// readTimeout ограничивает общее время чтения запроса.
	readTimeout = 10 * time.Second

	// writeTimeout ограничивает время записи ответа клиенту.
	writeTimeout = 10 * time.Second

	// idleTimeout определяет, сколько сервер держит
	// неактивное keep-alive соединение.
	idleTimeout = 60 * time.Second

	// shutdownTimeout — максимальное время,
	// которое даём текущим HTTP-запросам завершиться
	// при остановке приложения.
	shutdownTimeout = 10 * time.Second
)

func main() {
	// Создаём структурированный текстовый logger
	logger := slog.New(
		slog.NewTextHandler(
			os.Stdout,
			&slog.HandlerOptions{
				Level: slog.LevelInfo,
			},
		),
	)

	// Устанавливаем logger как глобальный logger
	slog.SetDefault(logger)

	// run возвращает ошибку вместо прямого os.Exit.
	if err := run(logger); err != nil {
		logger.Error("application stopped with error", "error", err)

		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// ------------------------------------------------------------
	// PostgreSQL
	// ------------------------------------------------------------

	// Создаём отдельный контекст с таймаутом
	// для начального подключения к PostgreSQL.
	databaseContext, cancel := context.WithTimeout(context.Background(), databaseStartupTimeout)
	defer cancel()

	// Создаём пул подключений.
	// pgxpool будет переиспользовать соединения
	// между параллельными HTTP-запросами.
	postgresPool, err := pgxpool.New(databaseContext, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("create postgres pool: %w", err)
	}
	// Закрываем пул при завершении run.
	// defer выполнится и при нормальном завершении, и при возврате ошибки.
	defer postgresPool.Close()

	// pgxpool.New создаёт объект пула,
	// но реальное подключение может выполняться лениво.
	//
	// Ping позволяет убедиться, что PostgreSQL
	// действительно доступен до запуска HTTP-сервера.
	logger.Info("PostgreSQL connection established")

	// ------------------------------------------------------------
	// Redis
	// ------------------------------------------------------------

	//Создаём Redis-кеш.
	redisCache, err := linkcache.NewRedisCache(
		cfg.RedisAddress,
		cfg.RedisPassword,
		cfg.RedisDB,
		cfg.RedisTTL,
	)
	if err != nil {
		return fmt.Errorf("create redis cache: %w", err)
	}
	// Закрываем Redis-клиент при завершении приложения.
	defer func() {
		if closeErr := redisCache.Close(); closeErr != nil {
			logger.Warn("redis cache close", "error", closeErr)
		}
	}()

	// Проверяем Redis отдельным коротким контекстом.
	redisContext, cancelRedisContext := context.WithTimeout(context.Background(), redisStartupTimeout)

	redisPingError := redisCache.Ping(redisContext)

	cancelRedisContext()

	if redisPingError != nil {
		logger.Warn(
			"Redis is unavaible; PostgresSQL fallback will be used",
			"error",
			redisPingError,
		)
	} else {
		logger.Info("Redis connection established")
	}

	// ------------------------------------------------------------
	// Dependency injection
	// ------------------------------------------------------------

	// Repository работает с PostgreSQL через sqlc.
	linkRepository := repository.NewPostgresLinkRepository(postgresPool)

	// Service содержит бизнес-логику
	// и использует repository и Redis cache.
	linkService := service.NewLinkService(linkRepository, redisCache)

	// Handler преобразует HTTP-запросы
	// в вызовы методов service.
	linkHandler := handler.NewLinkHandler(linkService, cfg.BaseURL)

	// Регистрируем маршруты:
	//
	//	POST /shorten
	//	GET  /stats/{code}
	//	GET  /{code}
	router := handler.NewRouter(linkHandler)

	// ------------------------------------------------------------
	// HTTP server
	// ------------------------------------------------------------

	// Формируем адрес:	:8080
	serverAdress := ":" + strconv.Itoa(cfg.HTTPPort)

	httpServer := &http.Server{
		Addr:    serverAdress,
		Handler: router,

		// Ограничиваем время работы с соединениями.
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	// Канал получит результат завершения HTTP-сервера.
	//
	// Размер 1 позволяет горутине записать ошибку,
	// даже если основная горутина ещё не начала её читать.
	serverErrorChannel := make(chan error, 1)

	// Создаём контекст, который завершится при получении:
	//
	//   - Ctrl+C / SIGINT;
	//   - SIGTERM от Docker или операционной системы.
	signalContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	// Запускаем сервер в отдельной горутине.
	// Основная горутина ниже будет ожидать:
	//   - ошибку HTTP-сервера;
	//   - системный сигнал завершения.
	go func() {
		logger.Info("HTTp server started", "address", serverAdress, "baseURL", cfg.BaseURL)

		serverErrorChannel <- httpServer.ListenAndServe()
	}()

	// Ожидаем одно из двух событий.
	select {
	case serverError := <-serverErrorChannel:
		if serverError != nil &&
			!errors.Is(serverError, http.ErrServerClosed) {
			return fmt.Errorf("HTTP server failed: %w", serverError)
		}

		return nil

	case <-signalContext.Done():
		// Получен SIGINT или SIGTERM.
		logger.Info("shutdown signal received")
	}

	// ------------------------------------------------------------
	// Graceful shutdown
	// ------------------------------------------------------------

	// signalContext уже отменён,
	// поэтому для Shutdown создаём новый контекст
	// от context.Background().
	shutdownContext, cancelShutdownContext := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancelShutdownContext()

	logger.Info("Shutting down server...")

	// Shutdown:
	//
	//   - перестаёт принимать новые соединения;
	//   - ждёт завершения текущих запросов;
	//   - прекращает ожидание после shutdownTimeout.
	if err := httpServer.Shutdown(shutdownContext); err != nil {
		// Если graceful shutdown не успел завершиться,
		// принудительно закрываем соединения.
		_ = httpServer.Close()

		return fmt.Errorf("shutting HTTP server: %w", err)
	}

	// После Shutdown метод ListenAndServe должен завершиться
	// с http.ErrServerClosed.
	serverError := <-serverErrorChannel

	if serverError != nil && !errors.Is(serverError, http.ErrServerClosed) {
		return fmt.Errorf("HTTP server stopped with: %w", serverError)
	}

	logger.Info("application stopped successfully")

	return nil
}
