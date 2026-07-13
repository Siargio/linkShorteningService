package repository

import (
	"context"

	"github.com/Siargio/linkShorteningService/internal/domain"
)

// LinkRepository описывает операции с хранилищем ссылок.
//
// Это интерфейс, а не конкретная реализация PostgreSQL.
// Благодаря этому сервисный слой не будет знать:
//
//   - какая база данных используется;
//   - используется ли pgx;
//   - используются ли запросы sqlc;
//   - как именно ссылки сохраняются и читаются.
//
// Позже в тестах сервиса мы сможем заменить настоящий PostgreSQL
// на тестовую реализацию LinkRepository.
type LinkRepository interface {
	// Create сохраняет новую ссылку в хранилище.
	//
	// Входной domain.Link должен содержать как минимум:
	//   - ShortCode — уже сгенерированный короткий код;
	//   - LongURL — исходный длинный URL.
	//
	// Поля ID, Clicks и CreatedAt заполняются PostgreSQL:
	//   - ID создаётся автоматически благодаря SERIAL;
	//   - Clicks получает значение DEFAULT 0;
	//   - CreatedAt получает значение DEFAULT NOW().
	//
	// Метод возвращает полностью заполненную ссылку.
	Create(ctx context.Context, link domain.Link) (domain.Link, error)

	// GetByCode ищет ссылку по короткому коду.
	//
	// Например, для запроса: GET /x7k2mN
	// в repository будет передан код: x7k2mN
	//
	// Если ссылка не найдена, реализация должна вернуть
	// доменную ошибку domain.ErrLinkNotFound.
	GetByCode(ctx context.Context, code string) (domain.Link, error)

	// IncrementClicks увеличивает счётчик переходов на единицу.
	//
	// Этот метод будет вызываться при успешном переходе пользователя
	// по короткой ссылке.
	//
	// Если ссылка не существует, реализация должна вернуть domain.ErrLinkNotFound.
	IncrementClicks(ctx context.Context, code string) error
}
