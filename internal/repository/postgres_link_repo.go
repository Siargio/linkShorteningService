package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/Siargio/linkShorteningService/internal/domain"
	db "github.com/Siargio/linkShorteningService/internal/repository/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// uniqueViolationSQLState — код ошибки PostgreSQL,
// который означает нарушение уникального ограничения.
//
// В нашей таблице поле short_code объявлено как UNIQUE:
//
//	short_code VARCHAR(10) NOT NULL UNIQUE
//
// Поэтому попытка сохранить уже существующий short_code
// приведёт к PostgreSQL-ошибке с кодом 23505.
const uniqueViolationSQLState = "23505"

// PostgresLinkRepository — реализация LinkRepository, которая работает с PostgreSQL.
// Внутри хранится интерфейс db.Querier.
// Этот интерфейс был сгенерирован sqlc, потому что в sqlc.yaml указано:
//
//	emit_interface: true
//
// db.Querier содержит методы:
//
//	CreateLink
//	GetLinkByCode
//	IncrementClicks
//
// Благодаря этому repository не пишет SQL вручную.
// Все SQL-запросы уже описаны в sql/queries/links.sql.
type PostgresLinkRepository struct {
	queries db.Querier
}

// Эта проверка выполняется во время компиляции.
//
// Она гарантирует, что *PostgresLinkRepository действительно
// реализует все методы интерфейса LinkRepository.
//
// Если мы забудем реализовать какой-либо метод или изменим его сигнатуру,
// проект перестанет компилироваться именно на этой строке.
var _ LinkRepository = (*PostgresLinkRepository)(nil)

// NewPostgresLinkRepository создаёт PostgreSQL-репозиторий.
//
// pool — пул соединений с PostgreSQL.
//
// db.New принимает объект, который умеет выполнять SQL-запросы.
// pgxpool.Pool подходит под этот интерфейс, поэтому его можно
// напрямую передать в сгенерированный sqlc-код.
//
// Конструктор только собирает зависимости.
// Он не открывает новое соединение и не выполняет SQL-запросы.
func NewPostgresLinkRepository(pool *pgxpool.Pool) *PostgresLinkRepository {
	// Создаём сгенерированный sqlc-объект Queries.
	// Именно он будет вызывать CreateLink, GetLinkByCode, IncrementClicks.
	queries := db.New(pool)

	// Возвращаем готовую реализацию repository.
	return &PostgresLinkRepository{
		queries: queries,
	}
}

// Create сохраняет новую короткую ссылку в PostgreSQL.
func (r *PostgresLinkRepository) Create(
	ctx context.Context,
	link domain.Link,
) (domain.Link, error) {
	// Вызываем сгенерированный sqlc-метод.
	//
	// В SQL этот метод соответствует запросу:
	//
	//	INSERT INTO links (short_code, long_url)
	//	VALUES ($1, $2)
	//	RETURNING id, short_code, long_url, clicks, created_at;
	//
	//   - ID мы не передаём — его создаёт PostgreSQL;
	//   - Clicks мы не передаём — используется DEFAULT 0;
	//   - CreatedAt мы не передаём — используется DEFAULT NOW().
	createdLink, err := r.queries.CreateLink(
		ctx,
		db.CreateLinkParams{
			ShortCode: link.ShortCode,

			// Это модель базы данных, сгенерированная автоматически.
			// В доменной модели мы используем правильное имя LongURL.
			LongUrl: link.LongURL,
		},
	)
	if err != nil {
		// Проверяем, не является ли ошибка нарушением уникальности short_code.
		// Такая ситуация возможна, если генератор случайно создал код, который уже существует в таблице.
		if isUniqueViolation(err) {
			// Преобразуем техническую PostgreSQL-ошибку в понятную бизнес-логике доменную ошибку.
			// Благодаря %w вызывающий код сможет проверить:
			//	errors.Is(err, domain.ErrShortCodeCollision)
			return domain.Link{}, fmt.Errorf(
				"%w: code %q already exists",
				domain.ErrShortCodeCollision,
				link.ShortCode,
			)
		}

		// Все остальные ошибки оборачиваем, добавляя информацию об операции.
		// Исходная ошибка сохраняется благодаря %w.
		// Для логирования и последующего анализа.
		return domain.Link{}, fmt.Errorf("create link in PostgreSQL: %w", err)
	}

	// sqlc вернул модель db.Link.
	// Service не должен зависеть от db.Link,
	// поэтому преобразуем её в domain.Link.
	return toDomainLink(createdLink), nil
}

// GetByCode получает ссылку по короткому коду.
func (r *PostgresLinkRepository) GetByCode(
	ctx context.Context,
	code string,
) (domain.Link, error) {
	// Вызываем сгенерированный запрос:
	//
	//	SELECT
	//	    id,
	//	    short_code,
	//	    long_url,
	//	    clicks,
	//	    created_at
	//	FROM links
	//	WHERE short_code = $1;
	link, err := r.queries.GetLinkByCode(ctx, code)
	if err != nil {
		// Для запроса с аннотацией :one sqlc ожидает одну строку.
		// Если PostgreSQL не нашёл строку, pgx возвращает pgx.ErrNoRows.
		if errors.Is(err, pgx.ErrNoRows) {
			// Не передаём pgx.ErrNoRows в service.
			// Service должен работать с доменной ошибкой
			// domain.ErrLinkNotFound и не знать ничего о pgx.
			return domain.Link{}, fmt.Errorf(
				"%w: code %q",
				domain.ErrLinkNotFound,
				code,
			)
		}

		// Любую другую ошибку считаем инфраструктурной:
		//
		//   - PostgreSQL недоступен;
		//   - соединение было разорвано;
		//   - контекст завершился;
		//   - произошла внутренняя ошибка выполнения запроса.
		return domain.Link{}, fmt.Errorf(
			"get link by code from PostgreSQL: %w", err)
	}

	// Преобразуем модель PostgreSQL в доменную модель.
	return toDomainLink(link), nil
}

// IncrementClicks увеличивает количество переходов по ссылке.
func (r *PostgresLinkRepository) IncrementClicks(
	ctx context.Context,
	code string,
) error {
	// Метод IncrementClicks был создан из SQL-запроса
	// с аннотацией :execrows:
	//
	//	UPDATE links
	//	SET clicks = clicks + 1
	//	WHERE short_code = $1;
	//
	// Поэтому sqlc возвращает количество изменённых строк.
	affectedRows, err := r.queries.IncrementClicks(ctx, code)
	if err != nil {
		return fmt.Errorf("increment link clicks in PostgreSQL: %w", err)
	}

	// Если ссылка существует, UPDATE изменит одну строку:
	//
	//	affectedRows == 1
	//
	// Если ссылки с таким кодом нет, WHERE ничего не найдёт:
	//
	//	affectedRows == 0
	if affectedRows == 0 {
		return fmt.Errorf("%w: code %q",
			domain.ErrLinkNotFound,
			code,
		)
	}

	// Ошибки нет, счётчик успешно увеличен.
	return nil
}

// toDomainLink преобразует сгенерированную sqlc-модель
// в модель доменного слоя.
//
// db.Link относится к инфраструктуре PostgreSQL.
// domain.Link относится к бизнес-логике приложения.
//
// Такое преобразование защищает service от зависимости
// от структуры таблицы и сгенерированного sqlc-кода.
func toDomainLink(link db.Link) domain.Link {
	return domain.Link{
		ID:        link.ID,
		ShortCode: link.ShortCode,
		LongURL:   link.LongUrl,
		Clicks:    link.Clicks,
		CreatedAt: link.CreatedAt,
	}
}

// isUniqueViolation проверяет, является ли ошибка
// PostgreSQL-ошибкой нарушения уникального ограничения.
//
// PostgreSQL возвращает структурированную ошибку *pgconn.PgError.
// Нас интересует её поле Code.
//
// Используем errors.As, потому что ошибка могла быть дополнительно
// обёрнута через fmt.Errorf с %w.
func isUniqueViolation(err error) bool {
	// pgError будет заполнен, если внутри цепочки ошибок
	// существует ошибка типа *pgconn.PgError.
	var pgError *pgconn.PgError

	// errors.As проходит по всей цепочке обёрнутых ошибок.
	// Если ошибка PostgreSQL найдена, дополнительно проверяем,
	// что её SQLSTATE равен 23505.
	return errors.As(err, &pgError) &&
		pgError.Code == uniqueViolationSQLState
}
