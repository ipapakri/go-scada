package designer

import (
	"fmt"

	"go-scada/address"
	"go-scada/stream"
)

// Subscription is a live telemetry subscription.
type Subscription interface {
	Stop()
}

// Store is the configuration and telemetry surface used by the HTTP service.
type Store interface {
	ListSubjects(suffix string) ([]string, error)
	ListSubjectsPrefix(prefix string) ([]string, error)
	Get(subject string) (string, error)
	Set(subject string, value string) error
	SubscribeString(subject string, handler func(value string)) (Subscription, error)
	Subscribe(
		subject string,
		valueType address.ValueType,
		handler func(value any),
	) (Subscription, error)
}

// StreamStore adapts the shared JetStream client to Store.
type StreamStore struct {
	client *stream.Client
}

func NewStreamStore(client *stream.Client) *StreamStore {
	return &StreamStore{client: client}
}

func (store *StreamStore) ListSubjects(suffix string) ([]string, error) {
	return stream.ListSubjects(store.client, suffix)
}

func (store *StreamStore) ListSubjectsPrefix(prefix string) ([]string, error) {
	return stream.ListSubjectsPrefix(store.client, prefix)
}

func (store *StreamStore) Get(subject string) (string, error) {
	return stream.Get[string](store.client, subject)
}

func (store *StreamStore) Set(subject string, value string) error {
	return stream.Set(store.client, subject, value)
}

func (store *StreamStore) SubscribeString(
	subject string,
	handler func(value string),
) (Subscription, error) {
	return stream.Subscribe(store.client, subject, func(_ string, value string) error {
		handler(value)
		return nil
	})
}

func (store *StreamStore) Subscribe(
	subject string,
	valueType address.ValueType,
	handler func(value any),
) (Subscription, error) {
	switch valueType {
	case address.ValueTypeBool:
		return stream.Subscribe(store.client, subject, func(_ string, value bool) error {
			handler(value)
			return nil
		})
	case address.ValueTypeInt64:
		return stream.Subscribe(store.client, subject, func(_ string, value int64) error {
			handler(value)
			return nil
		})
	case address.ValueTypeFloat64:
		return stream.Subscribe(store.client, subject, func(_ string, value float64) error {
			handler(value)
			return nil
		})
	default:
		return nil, fmt.Errorf("unsupported live value type %q", valueType)
	}
}
