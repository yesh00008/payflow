// Package messaging provides RabbitMQ publish/subscribe primitives.
package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// EventBus wraps a RabbitMQ connection for publishing and consuming events.
type EventBus struct {
	conn     *amqp.Connection
	exchange string
}

// Event represents a domain event on the bus.
type Event struct {
	Type          string      `json:"event_type"`
	Payload       interface{} `json:"payload"`
	Timestamp     time.Time   `json:"timestamp"`
	CorrelationID string      `json:"correlation_id,omitempty"`
}

// NewEventBus creates an EventBus from a RabbitMQ DSN.
func NewEventBus(amqpURL, exchange string) (*EventBus, error) {
	conn, err := amqp.Dial(amqpURL)
	if err != nil {
		return nil, fmt.Errorf("dial rabbitmq: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("open channel: %w", err)
	}
	defer ch.Close()

	// Declare the topic exchange
	if err := ch.ExchangeDeclare(exchange, "topic", true, false, false, false, nil); err != nil {
		conn.Close()
		return nil, fmt.Errorf("declare exchange: %w", err)
	}

	return &EventBus{conn: conn, exchange: exchange}, nil
}

// Publish sends an event to the exchange with the given routing key.
func (eb *EventBus) Publish(ctx context.Context, routingKey string, evt Event) error {
	ch, err := eb.conn.Channel()
	if err != nil {
		return fmt.Errorf("open channel: %w", err)
	}
	defer ch.Close()

	body, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	return ch.PublishWithContext(ctx, eb.exchange, routingKey, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Timestamp:    time.Now(),
		Body:         body,
	})
}

// Handler is a callback for consumed messages.
type Handler func(ctx context.Context, evt Event) error

// Subscribe creates a durable queue bound to routingKey and consumes messages.
func (eb *EventBus) Subscribe(queueName, routingKey string, handler Handler) error {
	ch, err := eb.conn.Channel()
	if err != nil {
		return fmt.Errorf("open channel: %w", err)
	}

	q, err := ch.QueueDeclare(queueName, true, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("declare queue: %w", err)
	}

	if err := ch.QueueBind(q.Name, routingKey, eb.exchange, false, nil); err != nil {
		return fmt.Errorf("bind queue: %w", err)
	}

	msgs, err := ch.Consume(q.Name, "", false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("consume: %w", err)
	}

	go func() {
		for d := range msgs {
			var evt Event
			if err := json.Unmarshal(d.Body, &evt); err != nil {
				log.Printf("[messaging] unmarshal error: %v", err)
				d.Nack(false, false)
				continue
			}
			if err := handler(context.Background(), evt); err != nil {
				log.Printf("[messaging] handler error for %s: %v", routingKey, err)
				d.Nack(false, true) // requeue
				continue
			}
			d.Ack(false)
		}
	}()

	return nil
}

// Close shuts down the connection.
func (eb *EventBus) Close() error {
	return eb.conn.Close()
}
