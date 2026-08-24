package stream

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
	"sync"

	telemetryv1 "go-scada/gen/go/go_scada/telemetry/v1"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
)

const (
	// DefaultInterestWorkers is the number of per-consumer goroutines that
	// handle live messages. Messages for one subject always go to the same
	// worker: hash(subject) % DefaultInterestWorkers.
	DefaultInterestWorkers = 10
	interestWorkerQueue    = 64
)

type exactJob struct {
	subject string
	value   any
}

// ExactSubscription watches a dynamic set of exact subjects. Live messages
// are sharded onto a worker pool.
type ExactSubscription struct {
	client  *Client
	name    string
	handler func(string, any) error

	mu      sync.Mutex
	watched map[string]*nats.Subscription
	ctx     context.Context
	cancel  context.CancelFunc

	workers []chan exactJob
	wg      sync.WaitGroup
	once    sync.Once
	closed  chan struct{}
}

// SubscribeExact starts a named exact-subject watcher. Handler subjects are
// system-relative.
func SubscribeExact(
	client *Client,
	name string,
	handler func(string, any) error,
) (*ExactSubscription, error) {
	if client == nil {
		return nil, errors.New("stream client is not initialized")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("consumer name is required")
	}
	if strings.ContainsAny(name, ". *>") {
		return nil, fmt.Errorf("consumer name %q is invalid", name)
	}
	if handler == nil {
		return nil, errors.New("value handler is required")
	}

	ctx, cancel := context.WithCancel(context.Background())
	subscription := &ExactSubscription{
		client:  client,
		name:    name,
		handler: handler,
		watched: make(map[string]*nats.Subscription),
		ctx:     ctx,
		cancel:  cancel,
		workers: make([]chan exactJob, DefaultInterestWorkers),
		closed:  make(chan struct{}),
	}
	for index := range subscription.workers {
		subscription.workers[index] = make(chan exactJob, interestWorkerQueue)
		subscription.wg.Add(1)
		go subscription.runWorker(subscription.workers[index])
	}
	return subscription, nil
}

// Add watches subject and invokes the handler with the retained value when one
// exists.
func (subscription *ExactSubscription) Add(subject string) error {
	return subscription.ensure(subject, true)
}

// Watch watches subject for future writes without reading the retained value.
func (subscription *ExactSubscription) Watch(subject string) error {
	return subscription.ensure(subject, false)
}

// Remove stops matching subject on the consumer.
func (subscription *ExactSubscription) Remove(subject string) error {
	if subscription == nil {
		return errors.New("exact subscription is not initialized")
	}
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return errors.New("subject is required")
	}

	subscription.mu.Lock()
	defer subscription.mu.Unlock()
	sub, exists := subscription.watched[subject]
	if !exists {
		return nil
	}
	delete(subscription.watched, subject)
	if sub != nil {
		_ = sub.Unsubscribe()
	}
	return nil
}

// Stop immediately stops the consumer and workers.
func (subscription *ExactSubscription) Stop() {
	if subscription == nil {
		return
	}
	subscription.once.Do(func() {
		if subscription.cancel != nil {
			subscription.cancel()
		}
		subscription.mu.Lock()
		for subject, sub := range subscription.watched {
			if sub != nil {
				_ = sub.Unsubscribe()
			}
			delete(subscription.watched, subject)
		}
		subscription.mu.Unlock()
		for _, worker := range subscription.workers {
			close(worker)
		}
		subscription.wg.Wait()
		close(subscription.closed)
	})
}

// Closed is closed after Stop has finished.
func (subscription *ExactSubscription) Closed() <-chan struct{} {
	if subscription == nil || subscription.closed == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return subscription.closed
}

func (subscription *ExactSubscription) ensure(subject string, catchUp bool) error {
	if subscription == nil {
		return errors.New("exact subscription is not initialized")
	}
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return errors.New("subject is required")
	}

	full, err := validateOperation(subscription.client, subject)
	if err != nil {
		return err
	}

	subscription.mu.Lock()
	_, exists := subscription.watched[subject]
	if !exists {
		sub, err := subscription.client.connection.Subscribe(full, func(message *nats.Msg) {
			relative, ok := subscription.client.relativeSubject(message.Subject)
			if !ok {
				return
			}
			value, err := decodeExact(message.Data)
			if err != nil {
				subscription.client.report(fmt.Errorf(
					"decode subject %q: %w",
					message.Subject,
					err,
				))
				return
			}
			subscription.dispatch(relative, value)
		})
		if err != nil {
			subscription.mu.Unlock()
			return fmt.Errorf("subscribe to %q: %w", full, err)
		}
		subscription.watched[subject] = sub
	}
	subscription.mu.Unlock()

	if !catchUp {
		return nil
	}
	value, err := GetAny(subscription.client, subject)
	if err != nil {
		if isMsgNotFound(err) {
			return nil
		}
		return err
	}
	return subscription.handler(subject, value)
}

func (subscription *ExactSubscription) dispatch(subject string, value any) {
	index := workerIndex(subject, len(subscription.workers))
	select {
	case <-subscription.ctx.Done():
	case subscription.workers[index] <- exactJob{subject: subject, value: value}:
	}
}

func (subscription *ExactSubscription) runWorker(jobs <-chan exactJob) {
	defer subscription.wg.Done()
	for job := range jobs {
		if err := subscription.handler(job.subject, job.value); err != nil {
			subscription.client.report(fmt.Errorf(
				"handle subject %q on %q: %w",
				job.subject,
				subscription.name,
				err,
			))
		}
	}
}

func workerIndex(subject string, workers int) int {
	if workers <= 0 {
		return 0
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(subject))
	return int(hash.Sum32() % uint32(workers))
}

func decodeExact(data []byte) (any, error) {
	var message telemetryv1.Message
	if err := proto.Unmarshal(data, &message); err != nil {
		return nil, err
	}
	return decodeAny(message.Value)
}
