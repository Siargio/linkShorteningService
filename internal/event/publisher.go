package event

import (
	"context"
	"time"

	"github.com/Siargio/linkShorteningService/internal/domain"
)

const (
	// LinkCreatedType — тип события, которое публикуется
	// после успешного создания короткой ссылки.
	LinkCreatedType = "link.created"

	// LinkVisitedType — тип события, которое публикуется
	// после успешного перехода по короткой ссылке.
	LinkVisitedType = "link.visited"

	// CurrentSchemaVersion — версия структуры Kafka-сообщения.
	//
	// Версия пригодится, если в будущем формат события изменится.
	// Consumer сможет понять, какую версию payload он получил.
	CurrentSchemaVersion = 1
)

// LinkEvent — универсальное событие домена коротких ссылок.
//
// В одном Kafka-топике link-events будут находиться
// события разных типов:
//
//   - link.created;
//   - link.visited.
//
// Поле EventType позволяет consumer определить,
// какое именно действие произошло.
type LinkEvent struct {
	// EventType описывает произошедшее действие.
	// Например:
	//	link.created
	//	link.visited
	EventType string `json:"event_type"`

	// SchemaVersion — версия JSON-контракта события.
	SchemaVersion int `json:"schema_version"`

	// ShortCode — короткий код ссылки.
	//
	// Он также будет использоваться как Kafka message key.
	// Благодаря одинаковому key события одной ссылки
	// будут попадать в одну партицию.
	ShortCode string `json:"short_code"`

	// LongURL — исходный длинный адрес.
	LongURL string `json:"long_url"`

	// OccurredAt — момент возникновения события.
	OccurredAt time.Time `json:"occurred_at"`
}

// Publisher описывает абстрактный механизм публикации событий.
//
// Service зависит от интерфейса и не знает:
//
//   - что используется Kafka;
//   - какая Kafka-библиотека используется;
//   - сколько брокеров находится в кластере;
//   - в какой топик отправляется сообщение.
//
// В unit-тестах вместо Kafka будет использоваться fakePublisher.
type Publisher interface {
	// Publish отправляет одно событие.
	Publish(
		ctx context.Context,
		linkEvent LinkEvent,
	) error
}

// NopPublisher — пустая реализация Publisher.
//
// Она ничего не публикует и всегда возвращает nil.
//
// Используется как безопасный fallback,
// если publisher не передан в service.
type NopPublisher struct{}

// Publish реализует интерфейс Publisher,
// но намеренно ничего не делает.
func (NopPublisher) Publish(
	ctx context.Context,
	linkEvent LinkEvent,
) error {
	return nil
}

// NewLinkCreatedEvent создаёт событие link.created.
func NewLinkCreatedEvent(
	link domain.Link,
) LinkEvent {
	return LinkEvent{
		EventType:     LinkCreatedType,
		SchemaVersion: CurrentSchemaVersion,
		ShortCode:     link.ShortCode,
		LongURL:       link.LongURL,
		OccurredAt:    time.Now().UTC(),
	}
}

// NewLinkVisitedEvent создаёт событие link.visited.
func NewLinkVisitedEvent(
	code string,
	longURL string,
) LinkEvent {
	return LinkEvent{
		EventType:     LinkVisitedType,
		SchemaVersion: CurrentSchemaVersion,
		ShortCode:     code,
		LongURL:       longURL,
		OccurredAt:    time.Now().UTC(),
	}
}
