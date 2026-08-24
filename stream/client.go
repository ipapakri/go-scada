// Package stream provides a protobuf-aware client for the application's
// shared NATS latest-value bus. Live traffic uses core NATS; Get and List
// read from the retain service.
package stream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	telemetryv1 "go-scada/gen/go/go_scada/telemetry/v1"
	"go-scada/retain"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gopkg.in/yaml.v3"
)

const (
	DefaultConfigPath       = "config.yaml"
	defaultOperationTimeout = 30 * time.Second
	defaultValueTTL         = 10 * time.Second
)

// ErrNotFound is returned when a subject has no retained value.
var ErrNotFound = errors.New("subject value not found")

// Config contains the application-wide stream connection settings.
type Config struct {
	NATSURL    string `yaml:"nats_url"`
	SystemName string `yaml:"system_name"`
}

// Client owns a NATS connection and provides access to the shared bus.
type Client struct {
	connection   *nats.Conn
	config       Config
	sourceID     string
	instanceID   int64
	errorHandler func(error)

	sequenceMu sync.Mutex
	sequences  map[string]uint64

	knownMu sync.Mutex
	known   map[string]struct{}
}

// Option configures a Client.
type Option func(*Client)

// Value is a Go value supported by the telemetry protobuf.
type Value interface {
	int64 | float64 | string | bool |
		[]byte | []int64 | []float64 | []string | []bool
}

// WithErrorHandler handles asynchronous subscription, decoding, and
// value-handler errors. The handler should return quickly so it does not
// delay consumption.
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
	config.SystemName = strings.TrimSpace(config.SystemName)
	if config.NATSURL == "" {
		return Config{}, errors.New("stream config nats_url is required")
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

	sourceID, err := os.Hostname()
	if err != nil || strings.TrimSpace(sourceID) == "" {
		sourceID = "unknown"
	}

	client := &Client{
		connection: connection,
		config:     config,
		sourceID:   sourceID,
		instanceID: time.Now().UnixNano(),
		sequences:  make(map[string]uint64),
		known:      make(map[string]struct{}),
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

// Create initializes a subject with the zero value of V if it does not
// already have a value.
func Create[V Value](client *Client, subject string) error {
	relative := strings.TrimSpace(subject)
	if _, err := Get[V](client, relative); err == nil {
		full, validateErr := validateOperation(client, relative)
		if validateErr == nil {
			client.markKnown(full)
		}
		return nil
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}

	var value V
	return Set(client, relative, value)
}

// Set publishes a value as a telemetry message.
func Set[V Value](
	client *Client,
	subject string,
	value V,
) error {
	relative := strings.TrimSpace(subject)
	subject, err := validateOperation(client, subject)
	if err != nil {
		return err
	}
	announce := client.shouldAnnounce(subject)

	message := client.newMessage(subject, encodeValue(value))
	data, err := proto.Marshal(message)
	if err != nil {
		return fmt.Errorf("marshal message for subject %q: %w", subject, err)
	}

	if err := client.connection.Publish(subject, data); err != nil {
		return fmt.Errorf("publish subject %q: %w", subject, err)
	}
	client.markKnown(subject)
	if announce {
		client.announceCreated(relative)
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

	raw, err := client.getPayload(subject)
	if err != nil {
		return zero, fmt.Errorf("get latest subject %q: %w", subject, err)
	}

	var message telemetryv1.Message
	if err := proto.Unmarshal(raw, &message); err != nil {
		return zero, fmt.Errorf("decode latest subject %q: %w", subject, err)
	}
	value, err := decodeValue[V](message.Value)
	if err != nil {
		return zero, fmt.Errorf("decode value for subject %q: %w", subject, err)
	}
	return value, nil
}

// GetAny retrieves the latest value without knowing its Go type.
func GetAny(client *Client, subject string) (any, error) {
	subject, err := validateOperation(client, subject)
	if err != nil {
		return nil, err
	}
	raw, err := client.getPayload(subject)
	if err != nil {
		return nil, err
	}
	var message telemetryv1.Message
	if err := proto.Unmarshal(raw, &message); err != nil {
		return nil, fmt.Errorf("decode latest subject %q: %w", subject, err)
	}
	return decodeAny(message.Value)
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
	return client.listSubjects(retain.ListRequest{Suffix: suffix})
}

// ListSubjectsPrefix returns sorted system-relative subjects below prefix.
// Prefixes are matched at a subject-token boundary, so "area" and "area."
// both match "area.point" but not "area2.point".
func ListSubjectsPrefix(client *Client, prefix string) ([]string, error) {
	prefix, _, err := validatePrefix(client, prefix)
	if err != nil {
		return nil, err
	}
	return client.listSubjects(retain.ListRequest{Prefix: prefix})
}

func (client *Client) getPayload(fullSubject string) ([]byte, error) {
	ctx, cancel := operationContext()
	defer cancel()
	return client.getPayloadContext(ctx, fullSubject)
}

func (client *Client) getPayloadContext(
	ctx context.Context,
	fullSubject string,
) ([]byte, error) {
	message, err := client.connection.RequestWithContext(
		ctx,
		retain.GetSubject(client.config.SystemName),
		[]byte(fullSubject),
	)
	if err != nil {
		return nil, err
	}
	if message.Header.Get(retain.HeaderError) == retain.ErrorNotFound {
		return nil, ErrNotFound
	}
	return message.Data, nil
}

func (client *Client) listSubjects(request retain.ListRequest) ([]string, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode list request: %w", err)
	}
	ctx, cancel := operationContext()
	defer cancel()
	message, err := client.connection.RequestWithContext(
		ctx,
		retain.ListSubject(client.config.SystemName),
		body,
	)
	if err != nil {
		return nil, fmt.Errorf("list subjects: %w", err)
	}
	var response retain.ListResponse
	if err := json.Unmarshal(message.Data, &response); err != nil {
		return nil, fmt.Errorf("decode list response: %w", err)
	}
	subjects := append([]string(nil), response.Subjects...)
	sort.Strings(subjects)
	return subjects, nil
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

func (client *Client) shouldAnnounce(fullSubject string) bool {
	if client == nil || fullSubject == "" {
		return false
	}
	relative, ok := client.relativeSubject(fullSubject)
	if !ok || relative == SubjectCreatedSubject {
		return false
	}

	client.knownMu.Lock()
	if _, known := client.known[fullSubject]; known {
		client.knownMu.Unlock()
		return false
	}
	client.knownMu.Unlock()

	_, err := client.getPayload(fullSubject)
	return errors.Is(err, ErrNotFound)
}

func (client *Client) markKnown(fullSubject string) {
	if client == nil || fullSubject == "" {
		return
	}
	client.knownMu.Lock()
	client.known[fullSubject] = struct{}{}
	client.knownMu.Unlock()
}

func (client *Client) announceCreated(relative string) {
	if client == nil || relative == "" || relative == SubjectCreatedSubject {
		return
	}
	payload, err := encodeSubjectCreated(relative)
	if err != nil {
		client.report(fmt.Errorf("encode subject created for %q: %w", relative, err))
		return
	}
	if err := Set(client, SubjectCreatedSubject, payload); err != nil {
		client.report(fmt.Errorf("announce subject created for %q: %w", relative, err))
	}
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
	decoded, err := decodeAny(value)
	if err != nil {
		return zero, err
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
	return errors.Is(err, ErrNotFound)
}

func operationContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), defaultOperationTimeout)
}
