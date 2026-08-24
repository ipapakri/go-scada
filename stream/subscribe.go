package stream

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	telemetryv1 "go-scada/gen/go/go_scada/telemetry/v1"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
)

// Subscription controls an active stream subscription.
type Subscription struct {
	ctx         context.Context
	sub         *nats.Subscription
	cancel      context.CancelFunc
	catchUpDone chan struct{}
	done        chan struct{}
	once        sync.Once
}

type lastDelivered struct {
	id       string
	observed time.Time
}

type deliveryState struct {
	mu   sync.Mutex
	last map[string]lastDelivered
}

func newDeliveryState() *deliveryState {
	return &deliveryState{last: make(map[string]lastDelivered)}
}

func (state *deliveryState) accept(subject string, message *telemetryv1.Message) bool {
	if state == nil || message == nil {
		return true
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	previous, seen := state.last[subject]
	id := message.GetMessageId()
	if id != "" && seen && previous.id == id {
		return false
	}
	var observed time.Time
	if message.ObservedAt != nil {
		observed = message.ObservedAt.AsTime()
	}
	if seen && !previous.observed.IsZero() && !observed.IsZero() &&
		observed.Before(previous.observed) {
		return false
	}
	state.last[subject] = lastDelivered{id: id, observed: observed}
	return true
}

// Subscribe delivers the latest subject value, followed by future values.
func Subscribe[V Value](
	client *Client,
	subject string,
	handler func(subject string, value V) error,
) (*Subscription, error) {
	fullSubject, err := validateOperation(client, subject)
	if err != nil {
		return nil, err
	}
	if handler == nil {
		return nil, errors.New("value handler is required")
	}

	state := newDeliveryState()
	subscription, err := startSubscription(
		client,
		fullSubject,
		func(message *nats.Msg) {
			deliverValue(
				client,
				state,
				message.Subject,
				message.Subject,
				message.Data,
				handler,
			)
		},
	)
	if err != nil {
		return nil, err
	}
	go func() {
		defer close(subscription.catchUpDone)
		payload, err := client.getPayloadContext(subscription.ctx, fullSubject)
		if err != nil {
			if !ignoreCatchUpError(err) {
				client.report(fmt.Errorf(
					"catch up subject %q: %w",
					fullSubject,
					err,
				))
			}
			return
		}
		deliverValue(client, state, fullSubject, fullSubject, payload, handler)
	}()
	return subscription, nil
}

// SubscribeSuffix delivers latest and future values for subjects ending in
// suffix. Handler subjects are system-relative.
func SubscribeSuffix[V Value](
	client *Client,
	suffix string,
	handler func(subject string, value V) error,
) (*Subscription, error) {
	if client == nil {
		return nil, errors.New("stream client is not initialized")
	}
	suffix = strings.TrimSpace(suffix)
	if suffix == "" {
		return nil, errors.New("subject suffix is required")
	}
	if handler == nil {
		return nil, errors.New("value handler is required")
	}

	state := newDeliveryState()
	filter := client.config.SystemName + ".>"
	subscription, err := startSubscription(
		client,
		filter,
		func(message *nats.Msg) {
			relative, ok := client.relativeSubject(message.Subject)
			if !ok || !strings.HasSuffix(relative, suffix) {
				return
			}
			deliverValue(
				client,
				state,
				message.Subject,
				relative,
				message.Data,
				handler,
			)
		},
	)
	if err != nil {
		return nil, err
	}
	go func() {
		defer close(subscription.catchUpDone)
		subjects, err := ListSubjects(client, suffix)
		if err != nil {
			if !ignoreCatchUpError(err) {
				client.report(fmt.Errorf("catch up suffix %q: %w", suffix, err))
			}
			return
		}
		for _, relative := range subjects {
			if subscription.ctx.Err() != nil {
				return
			}
			full := client.config.SystemName + "." + relative
			payload, err := client.getPayloadContext(subscription.ctx, full)
			if err != nil {
				if !ignoreCatchUpError(err) {
					client.report(fmt.Errorf(
						"catch up subject %q: %w",
						full,
						err,
					))
				}
				continue
			}
			deliverValue(client, state, full, relative, payload, handler)
		}
	}()
	return subscription, nil
}

// SubscribePrefix delivers the latest and future values below prefix. Prefixes
// are matched at a subject-token boundary. Handler subjects are system-relative.
func SubscribePrefix[V Value](
	client *Client,
	prefix string,
	handler func(subject string, value V) error,
) (*Subscription, error) {
	prefix, filter, err := validatePrefix(client, prefix)
	if err != nil {
		return nil, err
	}
	if handler == nil {
		return nil, errors.New("value handler is required")
	}

	state := newDeliveryState()
	subscription, err := startSubscription(
		client,
		filter,
		func(message *nats.Msg) {
			relative, ok := client.relativeSubject(message.Subject)
			if !ok || !strings.HasPrefix(relative, prefix) {
				return
			}
			deliverValue(
				client,
				state,
				message.Subject,
				relative,
				message.Data,
				handler,
			)
		},
	)
	if err != nil {
		return nil, err
	}
	go func() {
		defer close(subscription.catchUpDone)
		subjects, err := ListSubjectsPrefix(client, prefix)
		if err != nil {
			if !ignoreCatchUpError(err) {
				client.report(fmt.Errorf("catch up prefix %q: %w", prefix, err))
			}
			return
		}
		for _, relative := range subjects {
			if subscription.ctx.Err() != nil {
				return
			}
			full := client.config.SystemName + "." + relative
			payload, err := client.getPayloadContext(subscription.ctx, full)
			if err != nil {
				if !ignoreCatchUpError(err) {
					client.report(fmt.Errorf(
						"catch up subject %q: %w",
						full,
						err,
					))
				}
				continue
			}
			deliverValue(client, state, full, relative, payload, handler)
		}
	}()
	return subscription, nil
}

// Stop immediately stops the subscription.
func (subscription *Subscription) Stop() {
	if subscription == nil {
		return
	}
	subscription.once.Do(func() {
		if subscription.cancel != nil {
			subscription.cancel()
		}
		if subscription.sub != nil {
			_ = subscription.sub.Unsubscribe()
		}
		if subscription.catchUpDone != nil {
			<-subscription.catchUpDone
		}
		if subscription.done != nil {
			close(subscription.done)
		}
	})
}

// Drain stops fetching and processes already-buffered messages.
func (subscription *Subscription) Drain() {
	if subscription == nil {
		return
	}
	subscription.once.Do(func() {
		if subscription.cancel != nil {
			subscription.cancel()
		}
		if subscription.sub != nil {
			_ = subscription.sub.Drain()
		}
		if subscription.catchUpDone != nil {
			<-subscription.catchUpDone
		}
		if subscription.done != nil {
			close(subscription.done)
		}
	})
}

// Closed is closed after the subscription has fully stopped.
func (subscription *Subscription) Closed() <-chan struct{} {
	if subscription == nil || subscription.done == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return subscription.done
}

func startSubscription(
	client *Client,
	subject string,
	handler nats.MsgHandler,
) (*Subscription, error) {
	ctx, cancel := context.WithCancel(context.Background())
	sub, err := client.connection.Subscribe(subject, handler)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("subscribe to %q: %w", subject, err)
	}
	return &Subscription{
		ctx:         ctx,
		sub:         sub,
		cancel:      cancel,
		catchUpDone: make(chan struct{}),
		done:        make(chan struct{}),
	}, nil
}

func deliverValue[V Value](
	client *Client,
	state *deliveryState,
	fullSubject string,
	handlerSubject string,
	data []byte,
	handler func(subject string, value V) error,
) {
	var message telemetryv1.Message
	if err := proto.Unmarshal(data, &message); err != nil {
		client.report(fmt.Errorf("decode subject %q: %w", fullSubject, err))
		return
	}
	if !state.accept(fullSubject, &message) {
		return
	}
	value, err := decodeValue[V](message.Value)
	if err != nil {
		client.report(fmt.Errorf(
			"decode value for subject %q: %w",
			fullSubject,
			err,
		))
		return
	}
	if err := handler(handlerSubject, value); err != nil {
		client.report(fmt.Errorf(
			"handle subject %q: %w",
			fullSubject,
			err,
		))
	}
}

func ignoreCatchUpError(err error) bool {
	return isMsgNotFound(err) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
}
