package event

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	kafka "github.com/segmentio/kafka-go"
)

const (
	// kafkaWriteTimeout ограничивает время,
	// которое producer может ждать публикацию сообщения.
	kafkaWriteTimeout = 5 * time.Second

	// kafkaReadTimeout используется внутренними запросами
	// kafka-go к брокеру.
	kafkaReadTimeout = 5 * time.Second
)

var (
	// ErrKafkaBrokersEmpty возвращается,
	// если не указан ни один Kafka broker.
	ErrKafkaBrokersEmpty = errors.New(
		"Kafka brokers list is empty",
	)

	// ErrKafkaTopicEmpty возвращается,
	// если не указано имя Kafka topic.
	ErrKafkaTopicEmpty = errors.New(
		"Kafka topic is empty",
	)
)

// KafkaPublisher реализует Publisher через Apache Kafka.
type KafkaPublisher struct {
	// brokers сохраняем отдельно,
	// чтобы использовать их при Ping.
	brokers []string

	// topic — топик, в который публикуются события.
	topic string

	// writer — высокоуровневый Kafka producer.
	//
	// Writer самостоятельно:
	//
	//   - подключается к broker;
	//   - определяет leader партиции;
	//   - повторяет запросы при временных ошибках;
	//   - распределяет сообщения по партициям.
	writer *kafka.Writer
}

// Проверка реализации интерфейса во время компиляции.
var _ Publisher = (*KafkaPublisher)(nil)

// NewKafkaPublisher создаёт Kafka producer.
func NewKafkaPublisher(
	brokers []string,
	topic string,
) (*KafkaPublisher, error) {
	// Очищаем список брокеров от пустых значений
	// и случайных пробелов.
	cleanBrokers := make([]string, 0, len(brokers))

	for _, broker := range brokers {
		broker = strings.TrimSpace(broker)

		if broker == "" {
			continue
		}

		cleanBrokers = append(
			cleanBrokers,
			broker,
		)
	}

	if len(cleanBrokers) == 0 {
		return nil, ErrKafkaBrokersEmpty
	}

	topic = strings.TrimSpace(topic)

	if topic == "" {
		return nil, ErrKafkaTopicEmpty
	}

	writer := &kafka.Writer{
		// Kafka TCP принимает список брокеров.
		Addr: kafka.TCP(cleanBrokers...),

		// Все события отправляются в один топик.
		Topic: topic,

		// Hash использует Kafka message key.
		//
		// Поскольку key равен short_code,
		// события одной ссылки будут попадать
		// в одну и ту же партицию.
		Balancer: &kafka.Hash{},

		// Ждём подтверждение всех доступных реплик.
		//
		// В локальном single-node Kafka реплика одна.
		RequiredAcks: kafka.RequireAll,

		// Пишем синхронно.
		//
		// WriteMessages вернёт результат публикации
		// до завершения вызова.
		Async: false,

		WriteTimeout: kafkaWriteTimeout,
		ReadTimeout:  kafkaReadTimeout,

		// Topic создаётся отдельным kafka-init контейнером,
		// поэтому автоматическое создание отключаем.
		AllowAutoTopicCreation: false,
	}

	return &KafkaPublisher{
		brokers: cleanBrokers,
		topic:   topic,
		writer:  writer,
	}, nil
}

// Publish сериализует событие в JSON
// и отправляет его в Kafka.
func (p *KafkaPublisher) Publish(
	ctx context.Context,
	linkEvent LinkEvent,
) error {
	// Преобразуем Go-структуру в JSON.
	payload, err := json.Marshal(linkEvent)
	if err != nil {
		return fmt.Errorf(
			"marshal Kafka event: %w",
			err,
		)
	}

	// Отправляем одно Kafka-сообщение.
	err = p.writer.WriteMessages(
		ctx,
		kafka.Message{
			// Ключ используется Kafka balancer-ом.
			Key: []byte(linkEvent.ShortCode),

			// Value содержит JSON-событие.
			Value: payload,

			// Kafka также сохранит timestamp сообщения.
			Time: linkEvent.OccurredAt,

			// Header позволяет consumer узнать тип события,
			// не декодируя JSON payload.
			Headers: []kafka.Header{
				{
					Key: "event_type",
					Value: []byte(
						linkEvent.EventType,
					),
				},
			},
		},
	)
	if err != nil {
		return fmt.Errorf(
			"publish event %q to Kafka topic %q: %w",
			linkEvent.EventType,
			p.topic,
			err,
		)
	}

	return nil
}

// Ping проверяет, что хотя бы первый Kafka broker доступен.
//
// Сам kafka.Writer подключается лениво,
// поэтому после конструктора выполняем отдельную проверку.
func (p *KafkaPublisher) Ping(ctx context.Context) error {
	conn, err := kafka.DialContext(
		ctx,
		"tcp",
		p.brokers[0],
	)
	if err != nil {
		return fmt.Errorf(
			"connect to Kafka broker %q: %w",
			p.brokers[0],
			err,
		)
	}

	if err := conn.Close(); err != nil {
		return fmt.Errorf(
			"close Kafka ping connection: %w",
			err,
		)
	}

	return nil
}

// Close корректно закрывает Kafka writer.
//
// Для асинхронного Writer Close также дожидается
// отправки накопленных сообщений.
// У нас Writer синхронный, но закрывать его всё равно нужно.
func (p *KafkaPublisher) Close() error {
	if err := p.writer.Close(); err != nil {
		return fmt.Errorf(
			"close Kafka writer: %w",
			err,
		)
	}

	return nil
}
