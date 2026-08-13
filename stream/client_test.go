package stream

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	telemetryv1 "go-scada/gen/go/go_scada/telemetry/v1"

	"github.com/nats-io/nats.go/jetstream"
)

func TestLoadConfig(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(
		path,
		[]byte(
			"nats_url: nats://example.test:4222\n"+
				"stream_name: TEST_STREAM\n"+
				"system_name: ' test-system '\n",
		),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	config, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if config.NATSURL != "nats://example.test:4222" {
		t.Errorf("NATSURL = %q", config.NATSURL)
	}
	if config.StreamName != "TEST_STREAM" {
		t.Errorf("StreamName = %q", config.StreamName)
	}
	if config.SystemName != "test-system" {
		t.Errorf("SystemName = %q", config.SystemName)
	}
}

func TestLoadConfigValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
	}{
		{
			name:    "missing NATS URL",
			content: "stream_name: TEST_STREAM\nsystem_name: test-system\n",
		},
		{
			name:    "missing stream name",
			content: "nats_url: nats://localhost:4222\nsystem_name: test-system\n",
		},
		{
			name:    "missing system name",
			content: "nats_url: nats://localhost:4222\nstream_name: TEST_STREAM\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(test.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadConfig(path); err == nil {
				t.Fatal("LoadConfig() error = nil")
			}
		})
	}
}

func TestValidateOperation(t *testing.T) {
	t.Parallel()

	client := &Client{config: Config{SystemName: "test-system"}}
	subject, err := validateOperation(client, " point.value ")
	if err != nil {
		t.Fatal(err)
	}
	if subject != "test-system.point.value" {
		t.Errorf(
			"validateOperation() = %q, want %q",
			subject,
			"test-system.point.value",
		)
	}
	if _, err := validateOperation(client, " "); err == nil {
		t.Fatal("validateOperation() accepted an empty subject")
	}
	if _, err := validateOperation(nil, "point.value"); err == nil {
		t.Fatal("validateOperation() accepted a nil client")
	}
}

func TestRelativeSubject(t *testing.T) {
	t.Parallel()

	client := &Client{config: Config{SystemName: "test-system"}}
	subject, ok := client.relativeSubject("test-system.area.point.address")
	if !ok || subject != "area.point.address" {
		t.Fatalf("relativeSubject() = %q, %v", subject, ok)
	}
	if _, ok := client.relativeSubject("other.area.point.address"); ok {
		t.Fatal("relativeSubject() accepted another system")
	}
}

func TestValidatePrefix(t *testing.T) {
	t.Parallel()

	client := &Client{config: Config{SystemName: "test-system"}}
	for _, input := range []string{" area ", "area."} {
		prefix, filter, err := validatePrefix(client, input)
		if err != nil {
			t.Fatalf("validatePrefix(%q) error = %v", input, err)
		}
		if prefix != "area." {
			t.Errorf("validatePrefix(%q) prefix = %q, want %q", input, prefix, "area.")
		}
		if filter != "test-system.area.>" {
			t.Errorf(
				"validatePrefix(%q) filter = %q, want %q",
				input,
				filter,
				"test-system.area.>",
			)
		}
	}
	for _, input := range []string{"", " ", ".", ".area", "area..point", "area.*"} {
		if _, _, err := validatePrefix(client, input); err == nil {
			t.Errorf("validatePrefix(%q) error = nil", input)
		}
	}
	if _, _, err := validatePrefix(nil, "area"); err == nil {
		t.Error("validatePrefix(nil) error = nil")
	}
	if _, err := ListSubjectsPrefix(nil, "area"); err == nil {
		t.Error("ListSubjectsPrefix(nil) error = nil")
	}
	if _, err := ListSubjectsPrefix(client, " "); err == nil {
		t.Error("ListSubjectsPrefix(empty prefix) error = nil")
	}
	if _, err := SubscribePrefix[string](client, " ", func(string, string) error {
		return nil
	}); err == nil {
		t.Error("SubscribePrefix(empty prefix) error = nil")
	}
	if _, err := SubscribePrefix[string](client, "area", nil); err == nil {
		t.Error("SubscribePrefix(nil handler) error = nil")
	}
}

func TestLatestValueStreamConfig(t *testing.T) {
	t.Parallel()

	config := latestValueStreamConfig("TEST_STREAM", "test-system")
	if config.Name != "TEST_STREAM" {
		t.Errorf("Name = %q", config.Name)
	}
	if len(config.Subjects) != 1 ||
		config.Subjects[0] != "test-system.>" {
		t.Errorf("Subjects = %v", config.Subjects)
	}
	if config.MaxMsgsPerSubject != 1 {
		t.Errorf("MaxMsgsPerSubject = %d", config.MaxMsgsPerSubject)
	}
	if config.Discard != jetstream.DiscardOld {
		t.Errorf("Discard = %v", config.Discard)
	}
	if config.Storage != jetstream.FileStorage {
		t.Errorf("Storage = %v", config.Storage)
	}
	if !config.AllowDirect {
		t.Error("AllowDirect = false")
	}
	if config.NoAck {
		t.Error("NoAck = true")
	}
}

func TestValueRoundTrips(t *testing.T) {
	t.Parallel()

	assertValueRoundTrip(t, int64(-42))
	assertValueRoundTrip(t, 12.5)
	assertValueRoundTrip(t, "running")
	assertValueRoundTrip(t, true)
	assertValueRoundTrip(t, []byte{0, 1, 2})
	assertValueRoundTrip(t, []int64{1, 2, 3})
	assertValueRoundTrip(t, []float64{1.5, 2.5})
	assertValueRoundTrip(t, []string{"a", "b"})
	assertValueRoundTrip(t, []bool{true, false})
}

func TestDecodeValueRejectsTypeMismatch(t *testing.T) {
	t.Parallel()

	_, err := decodeValue[string](encodeValue(int64(42)))
	if err == nil {
		t.Fatal("decodeValue[string]() error = nil")
	}
	if !strings.Contains(err.Error(), "stored value has type int64") {
		t.Fatalf("decodeValue[string]() error = %v", err)
	}
}

func TestNewMessageGeneratesMetadata(t *testing.T) {
	t.Parallel()

	client := &Client{
		sourceID:   "test-source",
		instanceID: 123,
		sequences:  make(map[string]uint64),
	}
	first := client.newMessage("test.point", encodeValue(1.5))
	second := client.newMessage("test.point", encodeValue(2.5))

	if first.Sequence != 1 || second.Sequence != 2 {
		t.Fatalf("sequences = %d, %d; want 1, 2", first.Sequence, second.Sequence)
	}
	if first.Quality != telemetryv1.Quality_QUALITY_GOOD {
		t.Errorf("quality = %v", first.Quality)
	}
	if first.Source.GetId() != "test-source" {
		t.Errorf("source ID = %q", first.Source.GetId())
	}
	if first.ObservedAt == nil || first.ExpiresAt == nil {
		t.Fatal("generated timestamps are missing")
	}
	if !first.ExpiresAt.AsTime().After(first.ObservedAt.AsTime()) {
		t.Error("expiry does not follow observation time")
	}
	if first.MessageId == "" || first.MessageId == second.MessageId {
		t.Errorf("message IDs = %q, %q", first.MessageId, second.MessageId)
	}
}

func TestClientErrorHandler(t *testing.T) {
	t.Parallel()

	reported := make(chan error, 1)
	client := new(Client)
	WithErrorHandler(func(err error) {
		reported <- err
	})(client)

	want := errors.New("subscription failed")
	client.report(want)

	select {
	case got := <-reported:
		if !errors.Is(got, want) {
			t.Fatalf("reported error = %v, want %v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("error handler was not called")
	}
}

func TestClientLatestValueAndSubscribe(t *testing.T) {
	natsURL := os.Getenv("NATS_TEST_URL")
	if natsURL == "" {
		t.Skip("set NATS_TEST_URL to run the JetStream integration test")
	}

	streamName := fmt.Sprintf("TEST_LATEST_%d", time.Now().UnixNano())
	path := filepath.Join(t.TempDir(), "config.yaml")
	config := fmt.Sprintf(
		"nats_url: %s\nstream_name: %s\nsystem_name: test-system\n",
		natsURL,
		streamName,
	)
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	reported := make(chan error, 1)
	client, err := New(path, WithErrorHandler(func(err error) {
		select {
		case reported <- err:
		default:
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	if _, err := client.jetStream.Stream(
		context.Background(),
		streamName,
	); !errors.Is(err, jetstream.ErrStreamNotFound) {
		t.Fatalf("stream before CreateStream() error = %v", err)
	}
	if err := client.CreateStream(); err != nil {
		t.Fatal(err)
	}
	if err := client.CreateStream(); err != nil {
		t.Fatalf("repeated CreateStream() error = %v", err)
	}
	defer client.jetStream.DeleteStream(context.Background(), streamName)

	if err := Create[string](client, "test.initial"); err != nil {
		t.Fatal(err)
	}
	initial, err := Get[string](client, "test.initial")
	if err != nil {
		t.Fatal(err)
	}
	if initial != "" {
		t.Fatalf("initial value = %q, want the string zero value", initial)
	}
	if err := Set(client, "test.initial", "existing"); err != nil {
		t.Fatal(err)
	}
	if err := Create[string](client, "test.initial"); err != nil {
		t.Fatal(err)
	}
	var createGroup sync.WaitGroup
	for range 10 {
		createGroup.Add(1)
		go func() {
			defer createGroup.Done()
			if err := Create[string](client, "test.initial"); err != nil {
				t.Errorf("concurrent Create() error = %v", err)
			}
		}()
	}
	createGroup.Wait()
	initial, err = Get[string](client, "test.initial")
	if err != nil {
		t.Fatal(err)
	}
	if initial != "existing" {
		t.Fatalf("initial value = %q, want %q", initial, "existing")
	}

	for _, value := range []string{"a1", "a2"} {
		if err := Set(client, "test.point.a", value); err != nil {
			t.Fatal(err)
		}
	}
	if err := Set(client, "test.point.b", "b1"); err != nil {
		t.Fatal(err)
	}

	latestA, err := Get[string](client, "test.point.a")
	if err != nil {
		t.Fatal(err)
	}
	if latestA != "a2" {
		t.Fatalf("latest A = %q, want %q", latestA, "a2")
	}

	latestB, err := Get[string](client, "test.point.b")
	if err != nil {
		t.Fatal(err)
	}
	if latestB != "b1" {
		t.Fatalf("latest B = %q, want %q", latestB, "b1")
	}

	if err := Set(client, "sensor1.value.address", `{"driver":"modbus"}`); err != nil {
		t.Fatal(err)
	}
	if err := Set(client, "area.sensor2.value.address", `{"driver":"modbus"}`); err != nil {
		t.Fatal(err)
	}
	if err := Set(client, "sensor1.value", 12.5); err != nil {
		t.Fatal(err)
	}
	addressSubjects, err := ListSubjects(client, ".address")
	if err != nil {
		t.Fatal(err)
	}
	wantSubjects := []string{
		"area.sensor2.value.address",
		"sensor1.value.address",
	}
	if !reflect.DeepEqual(addressSubjects, wantSubjects) {
		t.Fatalf("address subjects = %v, want %v", addressSubjects, wantSubjects)
	}

	addressUpdates := make(chan string, 4)
	addressSubscription, err := SubscribeSuffix(
		client,
		".address",
		func(subject string, value string) error {
			addressUpdates <- subject + "=" + value
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer addressSubscription.Stop()
	if err := Set(client, "deep.area.sensor3.value.address", "new-address"); err != nil {
		t.Fatal(err)
	}
	assertReceived(
		t,
		addressUpdates,
		"deep.area.sensor3.value.address=new-address",
	)

	if err := Set(client, "prefix.area.beta", "beta"); err != nil {
		t.Fatal(err)
	}
	if err := Set(client, "prefix.area.alpha", "alpha"); err != nil {
		t.Fatal(err)
	}
	if err := Set(client, "prefix.area2.outside", "outside"); err != nil {
		t.Fatal(err)
	}
	prefixSubjects, err := ListSubjectsPrefix(client, " prefix.area ")
	if err != nil {
		t.Fatal(err)
	}
	wantPrefixSubjects := []string{
		"prefix.area.alpha",
		"prefix.area.beta",
	}
	if !reflect.DeepEqual(prefixSubjects, wantPrefixSubjects) {
		t.Fatalf(
			"prefix subjects = %v, want %v",
			prefixSubjects,
			wantPrefixSubjects,
		)
	}

	prefixUpdates := make(chan string, 3)
	prefixSubscription, err := SubscribePrefix(
		client,
		"prefix.area.",
		func(subject string, value string) error {
			prefixUpdates <- subject + "=" + value
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer prefixSubscription.Stop()
	assertReceived(t, prefixUpdates, "prefix.area.alpha=alpha")
	if err := Set(client, "prefix.area.gamma", "gamma"); err != nil {
		t.Fatal(err)
	}
	assertReceived(t, prefixUpdates, "prefix.area.gamma=gamma")
	if err := Set(client, "prefix.area2.still-outside", "outside"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-prefixUpdates:
		t.Fatalf("received non-matching prefix update %q", got)
	case <-time.After(250 * time.Millisecond):
	}

	received := make(chan string, 2)
	subscription, err := Subscribe(
		client,
		"test.point.a",
		func(subject string, value string) error {
			if subject != "test-system.test.point.a" {
				t.Fatalf(
					"subject = %q, want %q",
					subject,
					"test-system.test.point.a",
				)
			}
			received <- value
			if value == "a3" {
				return errors.New("test handler failure")
			}
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Stop()

	assertReceived(t, received, "a2")
	if err := Set(client, "test.point.a", "a3"); err != nil {
		t.Fatal(err)
	}
	assertReceived(t, received, "a3")
	select {
	case got := <-reported:
		if !strings.Contains(got.Error(), "test handler failure") {
			t.Fatalf("reported error = %v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for handler error")
	}
}

func assertValueRoundTrip[V Value](t *testing.T, want V) {
	t.Helper()
	got, err := decodeValue[V](encodeValue(want))
	if err != nil {
		t.Fatalf("decodeValue() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip = %#v, want %#v", got, want)
	}
}

func assertReceived(t *testing.T, received <-chan string, want string) {
	t.Helper()
	select {
	case got := <-received:
		if got != want {
			t.Fatalf("received %q, want %q", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %q", want)
	}
}
