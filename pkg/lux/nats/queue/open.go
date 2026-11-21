package queue

import "github.com/light-speak/luxo/pkg/lux/env"

// NewFromEnv opens only NATS JetStream, never an in-process substitute.
func NewFromEnv(cfg Config) (Queue, error) {
	url, err := env.Required("NATS_URL")
	if err != nil {
		return nil, err
	}
	queue, err := NewNATSQueue(url, cfg)
	if err != nil {
		return nil, err
	}
	return queue, nil
}
