// Package stream provides a protobuf-aware client for the application's
// shared NATS JetStream stream.
package stream

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	telemetryv1 "go-scada/gen/go/go_scada/telemetry/v1"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gopkg.in/yaml.v3"
)

const (
	DefaultConfigPath       = "config.yaml"
	defaultOperationTimeout = 30 * time.Second
	defaultValueTTL         = 10 * time.Second
)

// Config contains the application-wide stream connection settings.
type Config struct {
	NATSURL    string `yaml:"nats_url"`
	StreamName string `yaml:"stream_name"`
	SystemName string `yaml:"system_name"`
}

// Client owns a NATS connection and provides access to the shared stream.
type Client struct {
	connection   *nats.Conn
	jetStream    jetstream.JetStream
	config       Config
	sourceID     string
	instanceID   int64
	errorHandler func(error)

	sequenceMu sync.Mutex
	sequences  map[string]uint64
}

// Option configures a Client.
type Option func(*Client)

// Value is a Go value supported by the telemetry protobuf.
type Value interface {
	int64 | float64 | string | bool |
		[]byte | []int64 | []float64 | []string | []bool
}

// Subscription controls an active stream subscription.
type Subscription struct {
	consume jetstream.ConsumeContext
	cancel  context.CancelFunc
	once    sync.Once
}

// WithErrorHandler handles asynchronous consumer, decoding, and value-handler
// errors. The handler should return quickly so it does not delay consumption.
func WithErrorHandler(handler func(error)) Option {
	return func(client *Client) {
		client.errorHandler = handler
	}
}

// LoadConfig reads and validates stream configuration from a YAML file.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read stream config %q: %w", path, err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return Config{}, fmt.Errorf("decode stream config %q: %w", path, err)
	}

	config.NATSURL = strings.TrimSpace(config.NATSURL)
	config.StreamName = strings.TrimSpace(config.StreamName)
	config.SystemName = strings.TrimSpace(config.SystemName)
	if config.NATSURL == "" {
		return Config{}, errors.New("stream config nats_url is required")
	}
	if config.StreamName == "" {
		return Config{}, errors.New("stream config stream_name is required")
	}
	if config.SystemName == "" {
		return Config{}, errors.New("stream config system_name is required")
	}

	return config, nil
}

// New loads configuration, connects to NATS, and applies options.
func New(configPath string, options ...Option) (*Client, error) {
	config, err := LoadConfig(configPath)
	if err != nil {
		return nil, err
	}

	connection, err := nats.Connect(config.NATSURL)
	if err != nil {
		return nil, fmt.Errorf("connect to NATS at %q: %w", config.NATSURL, err)
	}

	js, err := jetstream.New(connection)
	if err != nil {
		connection.Close()
		return nil, fmt.Errorf("create JetStream client: %w", err)
	}

	sourceID, err := os.Hostname()
	if err != nil || strings.TrimSpace(sourceID) == "" {
		sourceID = "unknown"
	}

	client := &Client{
		connection: connection,
		jetStream:  js,
		config:     config,
		sourceID:   sourceID,
		instanceID: time.Now().UnixNano(),
		sequences:  make(map[string]uint64),
	}
	for _, option := range options {
		if option != nil {
			option(client)
		}
	}
	return client, nil
}

// Close closes the underlying NATS connection.
func (client *Client) Close() {
	if client != nil && client.connection != nil {
		client.connection.Close()
	}
}

// CreateStream creates or updates the configured latest-value stream.
func (client *Client) CreateStream() error {
	if client == nil {
		return errors.New("stream client is not initialized")
	}

	ctx, cancel := operationContext()
	defer cancel()
	_, err := client.jetStream.CreateOrUpdateStream(
		ctx,
		latestValueStreamConfig(
			client.config.StreamName,
			client.config.SystemName,
		),
	)
	if err != nil {
		return fmt.Errorf(
			"provision stream %q: %w",
			client.config.StreamName,
			err,
		)
	}
	return nil
}

// Create initializes a subject with the zero value of V if it does not
// already have a value.
func Create[V Value](client *Client, subject string) error {
	subject, err := validateOperation(client, subject)
	if err != nil {
		return err
	}

	var value V
	message := client.newMessage(subject, encodeValue(value))
	data, err := proto.Marshal(message)
	if err != nil {
		return fmt.Errorf("marshal initial message for subject %q: %w", subject, err)
	}

	ctx, cancel := operationContext()
	defer cancel()
	_, err = client.jetStream.Publish(
		ctx,
		subject,
		data,
		jetstream.WithExpectStream(client.config.StreamName),
		jetstream.WithExpectLastSequencePerSubject(0),
	)
	if err == nil || isWrongLastSequence(err) {
		return nil
	}
	return fmt.Errorf(
		"initialize subject %q in stream %q: %w",
		subject,
		client.config.StreamName,
		err,
	)
}

// Set publishes a value as a telemetry message.
func Set[V Value](
	client *Client,
	subject string,
	value V,
) error {
	subject, err := validateOperation(client, subject)
	if err != nil {
		return err
	}

	message := client.newMessage(subject, encodeValue(value))
	data, err := proto.Marshal(message)
	if err != nil {
		return fmt.Errorf("marshal message for subject %q: %w", subject, err)
	}

	ctx, cancel := operationContext()
	defer cancel()
	if _, err := client.jetStream.Publish(
		ctx,
		subject,
		data,
		jetstream.WithExpectStream(client.config.StreamName),
	); err != nil {
		return fmt.Errorf(
			"publish subject %q to stream %q: %w",
			subject,
			client.config.StreamName,
			err,
		)
	}
	return nil
}

// Get retrieves and decodes the latest value for a subject.
func Get[V Value](
	client *Client,
	subject string,
) (V, error) {
	var zero V
	subject, err := validateOperation(client, subject)
	if err != nil {
		return zero, err
	}

	ctx, cancel := operationContext()
	defer cancel()
	jsStream, err := client.jetStream.Stream(ctx, client.config.StreamName)
	if err != nil {
		return zero, fmt.Errorf(
			"open stream %q: %w",
			client.config.StreamName,
			err,
		)
	}
	raw, err := jsStream.GetLastMsgForSubject(ctx, subject)
	if err != nil {
		return zero, fmt.Errorf(
			"get latest subject %q from stream %q: %w",
			subject,
			client.config.StreamName,
			err,
		)
	}

	var message telemetryv1.Message
	if err := proto.Unmarshal(raw.Data, &message); err != nil {
		return zero, fmt.Errorf(
			"decode latest subject %q from stream %q: %w",
			subject,
			client.config.StreamName,
			err,
		)
	}
	value, err := decodeValue[V](message.Value)
	if err != nil {
		return zero, fmt.Errorf("decode value for subject %q: %w", subject, err)
	}
	return value, nil
}

// ListSubjects returns the system-relative subjects whose names end in suffix.
func ListSubjects(client *Client, suffix string) ([]string, error) {
	if client == nil {
		return nil, errors.New("stream client is not initialized")
	}
	suffix = strings.TrimSpace(suffix)
	if suffix == "" {
		return nil, errors.New("subject suffix is required")
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
	info, err := jsStream.Info(
		ctx,
		jetstream.WithSubjectFilter(client.config.SystemName+".>"),
	)
	if err != nil {
		return nil, fmt.Errorf(
			"list subjects in stream %q: %w",
			client.config.StreamName,
			err,
		)
	}

	subjects := make([]string, 0)
	for fullSubject := range info.State.Subjects {
		relative, ok := client.relativeSubject(fullSubject)
		if ok && strings.HasSuffix(relative, suffix) {
			subjects = append(subjects, relative)
		}
	}
	sort.Strings(subjects)
	return subjects, nil
}

// ListSubjectsPrefix returns sorted system-relative subjects below prefix.
// Prefixes are matched at a subject-token boundary, so "area" and "area."
// both match "area.point" but not "area2.point".
func ListSubjectsPrefix(client *Client, prefix string) ([]string, error) {
	prefix, filter, err := validatePrefix(client, prefix)
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
	info, err := jsStream.Info(ctx, jetstream.WithSubjectFilter(filter))
	if err != nil {
		return nil, fmt.Errorf(
			"list subjects with prefix %q in stream %q: %w",
			prefix,
			client.config.StreamName,
			err,
		)
	}

	subjects := make([]string, 0)
	for fullSubject := range info.State.Subjects {
		relative, ok := client.relativeSubject(fullSubject)
		if ok && strings.HasPrefix(relative, prefix) {
			subjects = append(subjects, relative)
		}
	}
	sort.Strings(subjects)
	return subjects, nil
}

// Subscribe delivers the latest subject value, followed by future values.
func Subscribe[V Value](
	client *Client,
	subject string,
	handler func(subject string, value V) error,
) (*Subscription, error) {
	subject, err := validateOperation(client, subject)
	if err != nil {
		return nil, err
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
	consumer, err := jsStream.OrderedConsumer(ctx, jetstream.OrderedConsumerConfig{
		FilterSubjects: []string{subject},
		DeliverPolicy:  jetstream.DeliverLastPolicy,
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf(
			"create ordered consumer for subject %q on stream %q: %w",
			subject,
			client.config.StreamName,
			err,
		)
	}

	subscription := &Subscription{
		cancel: cancel,
	}
	consume, err := consumer.Consume(
		func(msg jetstream.Msg) {
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
			value, err := decodeValue[V](message.Value)
			if err != nil {
				client.report(fmt.Errorf(
					"decode value for subject %q from stream %q: %w",
					msg.Subject(),
					client.config.StreamName,
					err,
				))
				return
			}
			if err := handler(msg.Subject(), value); err != nil {
				client.report(fmt.Errorf(
					"handle subject %q from stream %q: %w",
					msg.Subject(),
					client.config.StreamName,
					err,
				))
			}
		},
		jetstream.ConsumeErrHandler(func(_ jetstream.ConsumeContext, err error) {
			client.report(fmt.Errorf(
				"consume subject %q from stream %q: %w",
				subject,
				client.config.StreamName,
				err,
			))
		}),
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf(
			"consume subject %q from stream %q: %w",
			subject,
			client.config.StreamName,
			err,
		)
	}
	subscription.consume = consume

	go func() {
		<-consume.Closed()
		cancel()
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
	filter := client.config.SystemName + ".>"
	consumer, err := jsStream.OrderedConsumer(ctx, jetstream.OrderedConsumerConfig{
		FilterSubjects: []string{filter},
		DeliverPolicy:  jetstream.DeliverLastPolicy,
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf(
			"create ordered consumer for suffix %q on stream %q: %w",
			suffix,
			client.config.StreamName,
			err,
		)
	}

	subscription := &Subscription{cancel: cancel}
	consume, err := consumer.Consume(
		func(msg jetstream.Msg) {
			relative, ok := client.relativeSubject(msg.Subject())
			if !ok || !strings.HasSuffix(relative, suffix) {
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
			value, err := decodeValue[V](message.Value)
			if err != nil {
				client.report(fmt.Errorf(
					"decode value for subject %q from stream %q: %w",
					msg.Subject(),
					client.config.StreamName,
					err,
				))
				return
			}
			if err := handler(relative, value); err != nil {
				client.report(fmt.Errorf(
					"handle subject %q from stream %q: %w",
					msg.Subject(),
					client.config.StreamName,
					err,
				))
			}
		},
		jetstream.ConsumeErrHandler(func(_ jetstream.ConsumeContext, err error) {
			client.report(fmt.Errorf(
				"consume suffix %q from stream %q: %w",
				suffix,
				client.config.StreamName,
				err,
			))
		}),
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf(
			"consume suffix %q from stream %q: %w",
			suffix,
			client.config.StreamName,
			err,
		)
	}
	subscription.consume = consume
	go func() {
		<-consume.Closed()
		cancel()
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
	consumer, err := jsStream.OrderedConsumer(ctx, jetstream.OrderedConsumerConfig{
		FilterSubjects: []string{filter},
		DeliverPolicy:  jetstream.DeliverLastPolicy,
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf(
			"create ordered consumer for prefix %q on stream %q: %w",
			prefix,
			client.config.StreamName,
			err,
		)
	}

	subscription := &Subscription{cancel: cancel}
	consume, err := consumer.Consume(
		func(msg jetstream.Msg) {
			relative, ok := client.relativeSubject(msg.Subject())
			if !ok || !strings.HasPrefix(relative, prefix) {
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
			value, err := decodeValue[V](message.Value)
			if err != nil {
				client.report(fmt.Errorf(
					"decode value for subject %q from stream %q: %w",
					msg.Subject(),
					client.config.StreamName,
					err,
				))
				return
			}
			if err := handler(relative, value); err != nil {
				client.report(fmt.Errorf(
					"handle subject %q from stream %q: %w",
					msg.Subject(),
					client.config.StreamName,
					err,
				))
			}
		},
		jetstream.ConsumeErrHandler(func(_ jetstream.ConsumeContext, err error) {
			client.report(fmt.Errorf(
				"consume prefix %q from stream %q: %w",
				prefix,
				client.config.StreamName,
				err,
			))
		}),
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf(
			"consume prefix %q from stream %q: %w",
			prefix,
			client.config.StreamName,
			err,
		)
	}
	subscription.consume = consume
	go func() {
		<-consume.Closed()
		cancel()
	}()
	return subscription, nil
}

// Stop immediately stops the subscription.
func (subscription *Subscription) Stop() {
	if subscription == nil || subscription.consume == nil {
		return
	}
	subscription.once.Do(func() {
		if subscription.cancel != nil {
			subscription.cancel()
		}
		subscription.consume.Stop()
	})
}

// Drain stops fetching and processes already-buffered messages.
func (subscription *Subscription) Drain() {
	if subscription == nil || subscription.consume == nil {
		return
	}
	subscription.once.Do(subscription.consume.Drain)
}

// Closed is closed after the subscription has fully stopped.
func (subscription *Subscription) Closed() <-chan struct{} {
	if subscription == nil || subscription.consume == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return subscription.consume.Closed()
}

func latestValueStreamConfig(name string, systemName string) jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:              name,
		Subjects:          append([]string(nil), systemName+".>"),
		MaxMsgsPerSubject: 1,
		Discard:           jetstream.DiscardOld,
		Storage:           jetstream.FileStorage,
		AllowDirect:       true,
	}
}

func (client *Client) report(err error) {
	if client != nil && client.errorHandler != nil {
		client.errorHandler(err)
	}
}

func (client *Client) relativeSubject(subject string) (string, bool) {
	prefix := client.config.SystemName + "."
	relative, ok := strings.CutPrefix(subject, prefix)
	return relative, ok && relative != ""
}

func validatePrefix(client *Client, prefix string) (string, string, error) {
	if client == nil {
		return "", "", errors.New("stream client is not initialized")
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return "", "", errors.New("subject prefix is required")
	}
	prefix = strings.TrimSuffix(prefix, ".")
	if prefix == "" ||
		strings.HasPrefix(prefix, ".") ||
		strings.HasSuffix(prefix, ".") ||
		strings.Contains(prefix, "..") ||
		strings.ContainsAny(prefix, "*> \t\r\n") {
		return "", "", fmt.Errorf("subject prefix %q is invalid", prefix)
	}
	prefix += "."
	return prefix, client.config.SystemName + "." + prefix + ">", nil
}

func validateOperation(
	client *Client,
	subject string,
) (string, error) {
	if client == nil {
		return "", errors.New("stream client is not initialized")
	}
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return "", errors.New("subject is required")
	}
	subject = client.config.SystemName + "." + subject
	return subject, nil
}

func isWrongLastSequence(err error) bool {
	var apiError *jetstream.APIError
	return errors.As(err, &apiError) &&
		apiError.ErrorCode == jetstream.JSErrCodeStreamWrongLastSequence
}

func (client *Client) newMessage(
	subject string,
	value *telemetryv1.Value,
) *telemetryv1.Message {
	now := time.Now()
	sequence := client.nextSequence(subject)
	return &telemetryv1.Message{
		Sequence:   sequence,
		Value:      value,
		Quality:    telemetryv1.Quality_QUALITY_GOOD,
		ObservedAt: timestamppb.New(now),
		Source: &telemetryv1.Source{
			Id: client.sourceID,
		},
		MessageId: fmt.Sprintf(
			"%s-%d-%s-%d",
			client.sourceID,
			client.instanceID,
			subject,
			sequence,
		),
		ExpiresAt: timestamppb.New(now.Add(defaultValueTTL)),
	}
}

func (client *Client) nextSequence(subject string) uint64 {
	client.sequenceMu.Lock()
	defer client.sequenceMu.Unlock()
	client.sequences[subject]++
	return client.sequences[subject]
}

func encodeValue[V Value](value V) *telemetryv1.Value {
	encoded := new(telemetryv1.Value)
	switch value := any(value).(type) {
	case int64:
		encoded.Kind = &telemetryv1.Value_IntValue{IntValue: value}
	case float64:
		encoded.Kind = &telemetryv1.Value_FloatValue{FloatValue: value}
	case string:
		encoded.Kind = &telemetryv1.Value_StringValue{StringValue: value}
	case bool:
		encoded.Kind = &telemetryv1.Value_BoolValue{BoolValue: value}
	case []byte:
		encoded.Kind = &telemetryv1.Value_BytesValue{
			BytesValue: append([]byte(nil), value...),
		}
	case []int64:
		encoded.Kind = &telemetryv1.Value_IntArray{
			IntArray: &telemetryv1.IntArray{
				Values: append([]int64(nil), value...),
			},
		}
	case []float64:
		encoded.Kind = &telemetryv1.Value_FloatArray{
			FloatArray: &telemetryv1.DoubleArray{
				Values: append([]float64(nil), value...),
			},
		}
	case []string:
		encoded.Kind = &telemetryv1.Value_StringArray{
			StringArray: &telemetryv1.StringArray{
				Values: append([]string(nil), value...),
			},
		}
	case []bool:
		encoded.Kind = &telemetryv1.Value_BoolArray{
			BoolArray: &telemetryv1.BoolArray{
				Values: append([]bool(nil), value...),
			},
		}
	}
	return encoded
}

func decodeValue[V Value](value *telemetryv1.Value) (V, error) {
	var zero V
	if value == nil || value.Kind == nil {
		return zero, errors.New("telemetry value is missing")
	}

	var decoded any
	switch kind := value.Kind.(type) {
	case *telemetryv1.Value_IntValue:
		decoded = kind.IntValue
	case *telemetryv1.Value_FloatValue:
		decoded = kind.FloatValue
	case *telemetryv1.Value_StringValue:
		decoded = kind.StringValue
	case *telemetryv1.Value_BoolValue:
		decoded = kind.BoolValue
	case *telemetryv1.Value_BytesValue:
		decoded = append([]byte(nil), kind.BytesValue...)
	case *telemetryv1.Value_IntArray:
		decoded = append([]int64(nil), kind.IntArray.GetValues()...)
	case *telemetryv1.Value_FloatArray:
		decoded = append([]float64(nil), kind.FloatArray.GetValues()...)
	case *telemetryv1.Value_StringArray:
		decoded = append([]string(nil), kind.StringArray.GetValues()...)
	case *telemetryv1.Value_BoolArray:
		decoded = append([]bool(nil), kind.BoolArray.GetValues()...)
	default:
		return zero, fmt.Errorf("unsupported protobuf value type %T", value.Kind)
	}

	typed, ok := decoded.(V)
	if !ok {
		return zero, fmt.Errorf(
			"stored value has type %T, requested %T",
			decoded,
			zero,
		)
	}
	return typed, nil
}

func operationContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), defaultOperationTimeout)
}
