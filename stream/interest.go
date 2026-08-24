package stream

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"sync"
	"time"

	telemetryv1 "go-scada/gen/go/go_scada/telemetry/v1"

	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"
)

const (
	// DefaultInterestWorkers is the number of per-consumer goroutines that
	// handle live messages. Messages for one subject always go to the same
	// worker: hash(subject) % DefaultInterestWorkers.
	DefaultInterestWorkers = 10
	interestWorkerQueue    = 64
	interestDummyToken     = "__interest.empty"
)

type exactJob struct {
	subject string
	value   any
}

// ExactSubscription is a durable pull consumer whose FilterSubjects list is
// grown with UpdateConsumer. Live messages are sharded onto a worker pool.
type ExactSubscription struct {
	client  *Client
	durable string
	dummy   string
	handler func(string, any) error

	mu       sync.Mutex
	watched  map[string]struct{}
	jsStream jetstream.Stream
	consumer jetstream.Consumer
	consume  jetstream.ConsumeContext
	ctx      context.Context
	cancel   context.CancelFunc

	workers []chan exactJob
	wg      sync.WaitGroup
	once    sync.Once
	closed  chan struct{}
}

// SubscribeExact starts a named durable consumer with a dummy filter so it can
// be updated as exact subjects are added. Handler subjects are system-relative.
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
	jsStream, err := client.jetStream.Stream(ctx, client.config.StreamName)
	if err != nil {
		cancel()
		return nil, fmt.Errorf(
			"open stream %q: %w",
			client.config.StreamName,
			err,
		)
	}

	subscription := &ExactSubscription{
		client:   client,
		durable:  fmt.Sprintf("%s-%d", name, client.instanceID),
		dummy:    client.config.SystemName + "." + interestDummyToken,
		handler:  handler,
		watched:  make(map[string]struct{}),
		jsStream: jsStream,
		ctx:      ctx,
		cancel:   cancel,
		workers:  make([]chan exactJob, DefaultInterestWorkers),
		closed:   make(chan struct{}),
	}
	for index := range subscription.workers {
		subscription.workers[index] = make(chan exactJob, interestWorkerQueue)
		subscription.wg.Add(1)
		go subscription.runWorker(subscription.workers[index])
	}

	consumer, err := jsStream.CreateOrUpdateConsumer(ctx, subscription.configLocked())
	if err != nil {
		subscription.Stop()
		return nil, fmt.Errorf(
			"create exact consumer %q: %w",
			subscription.durable,
			err,
		)
	}
	subscription.consumer = consumer
	consume, err := consumer.Consume(
		func(msg jetstream.Msg) {
			relative, ok := client.relativeSubject(msg.Subject())
			if !ok || relative == interestDummyToken {
				return
			}
			var message telemetryv1.Message
			if err := proto.Unmarshal(msg.Data(), &message); err != nil {
				client.report(fmt.Errorf(
					"decode subject %q from stream %q: %w",
					msg.Subject(),
					client.config.StreamName,
					err,
				))
				return
			}
			value, err := decodeAny(message.Value)
			if err != nil {
				client.report(fmt.Errorf(
					"decode value for subject %q from stream %q: %w",
					msg.Subject(),
					client.config.StreamName,
					err,
				))
				return
			}
			subscription.dispatch(relative, value)
		},
		jetstream.ConsumeErrHandler(func(_ jetstream.ConsumeContext, err error) {
			client.report(fmt.Errorf(
				"consume exact consumer %q: %w",
				subscription.durable,
				err,
			))
		}),
	)
	if err != nil {
		subscription.Stop()
		return nil, fmt.Errorf(
			"consume exact consumer %q: %w",
			subscription.durable,
			err,
		)
	}
	subscription.consume = consume
	return subscription, nil
}

// Add watches subject and invokes the handler with the retained value when one
// exists. UpdateConsumer does not replay that value.
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
	if _, exists := subscription.watched[subject]; !exists {
		return nil
	}
	delete(subscription.watched, subject)
	return subscription.updateLocked()
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
		if subscription.consume != nil {
			subscription.consume.Stop()
			<-subscription.consume.Closed()
		}
		for _, worker := range subscription.workers {
			close(worker)
		}
		subscription.wg.Wait()
		ctx, cancel := operationContext()
		defer cancel()
		if subscription.jsStream != nil && subscription.durable != "" {
			_ = subscription.jsStream.DeleteConsumer(ctx, subscription.durable)
		}
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

	subscription.mu.Lock()
	_, exists := subscription.watched[subject]
	if !exists {
		subscription.watched[subject] = struct{}{}
		if err := subscription.updateLocked(); err != nil {
			delete(subscription.watched, subject)
			subscription.mu.Unlock()
			return err
		}
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

func (subscription *ExactSubscription) updateLocked() error {
	ctx, cancel := operationContext()
	defer cancel()
	consumer, err := subscription.jsStream.UpdateConsumer(ctx, subscription.configLocked())
	if err != nil {
		return fmt.Errorf(
			"update exact consumer %q: %w",
			subscription.durable,
			err,
		)
	}
	subscription.consumer = consumer
	return nil
}

func (subscription *ExactSubscription) configLocked() jetstream.ConsumerConfig {
	return jetstream.ConsumerConfig{
		Name:              subscription.durable,
		Durable:           subscription.durable,
		FilterSubjects:    subscription.filtersLocked(),
		DeliverPolicy:     jetstream.DeliverNewPolicy,
		AckPolicy:         jetstream.AckNonePolicy,
		InactiveThreshold: 5 * time.Minute,
		MemoryStorage:     true,
		Replicas:          1,
	}
}

func (subscription *ExactSubscription) filtersLocked() []string {
	filters := make([]string, 0, len(subscription.watched)+1)
	filters = append(filters, subscription.dummy)
	full := make([]string, 0, len(subscription.watched))
	for relative := range subscription.watched {
		full = append(full, subscription.client.config.SystemName+"."+relative)
	}
	sort.Strings(full)
	return append(filters, full...)
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
				subscription.durable,
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

func GetAny(client *Client, subject string) (any, error) {
	subject, err := validateOperation(client, subject)
	if err != nil {
		return nil, err
	}
	ctx, cancel := operationContext()
	defer cancel()
	jsStream, err := client.jetStream.Stream(ctx, client.config.StreamName)
	if err != nil {
		return nil, fmt.Errorf(
			"open stream %q: %w",
			client.config.StreamName,
			err,
		)
	}
	raw, err := jsStream.GetLastMsgForSubject(ctx, subject)
	if err != nil {
		return nil, err
	}
	var message telemetryv1.Message
	if err := proto.Unmarshal(raw.Data, &message); err != nil {
		return nil, fmt.Errorf(
			"decode latest subject %q: %w",
			subject,
			err,
		)
	}
	return decodeAny(message.Value)
}

func decodeAny(value *telemetryv1.Value) (any, error) {
	if value == nil || value.Kind == nil {
		return nil, errors.New("telemetry value is missing")
	}
	switch kind := value.Kind.(type) {
	case *telemetryv1.Value_IntValue:
		return kind.IntValue, nil
	case *telemetryv1.Value_FloatValue:
		return kind.FloatValue, nil
	case *telemetryv1.Value_StringValue:
		return kind.StringValue, nil
	case *telemetryv1.Value_BoolValue:
		return kind.BoolValue, nil
	case *telemetryv1.Value_BytesValue:
		return append([]byte(nil), kind.BytesValue...), nil
	case *telemetryv1.Value_IntArray:
		return append([]int64(nil), kind.IntArray.GetValues()...), nil
	case *telemetryv1.Value_FloatArray:
		return append([]float64(nil), kind.FloatArray.GetValues()...), nil
	case *telemetryv1.Value_StringArray:
		return append([]string(nil), kind.StringArray.GetValues()...), nil
	case *telemetryv1.Value_BoolArray:
		return append([]bool(nil), kind.BoolArray.GetValues()...), nil
	default:
		return nil, fmt.Errorf("unsupported protobuf value type %T", value.Kind)
	}
}

func isMsgNotFound(err error) bool {
	return errors.Is(err, jetstream.ErrMsgNotFound)
}
