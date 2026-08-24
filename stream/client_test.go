package stream

import (
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
	"go-scada/retain"

	"github.com/nats-io/nats.go"
)

func TestLoadConfig(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(
		path,
		[]byte(
			"nats_url: nats://example.test:4222\n"+
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
			content: "system_name: test-system\n",
		},
		{
			name:    "missing system name",
			content: "nats_url: nats://localhost:4222\n",
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
		t.Skip("set NATS_TEST_URL to run the NATS integration test")
	}

	systemName := fmt.Sprintf("sys%d", time.Now().UnixNano())
	path := filepath.Join(t.TempDir(), "config.yaml")
	config := fmt.Sprintf(
		"nats_url: %s\nsystem_name: %s\n",
		natsURL,
		systemName,
	)
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := retain.Open(filepath.Join(t.TempDir(), "retain.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	connection, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	server, err := retain.Listen(connection, store, systemName)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

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

	if err := Create[string](client, "test.initial"); err != nil {
		t.Fatal(err)
	}
	initial := waitGet[string](t, client, "test.initial")
	if initial != "" {
		t.Fatalf("initial value = %q, want the string zero value", initial)
	}
	if err := Set(client, "test.initial", "existing"); err != nil {
		t.Fatal(err)
	}
	if got := waitGet[string](t, client, "test.initial"); got != "existing" {
		t.Fatalf("value after Set = %q, want %q", got, "existing")
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
	initial = waitGet[string](t, client, "test.initial")
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

	latestA := waitGet[string](t, client, "test.point.a")
	if latestA != "a2" {
		t.Fatalf("latest A = %q, want %q", latestA, "a2")
	}

	latestB := waitGet[string](t, client, "test.point.b")
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
	wantSubjects := []string{
		"area.sensor2.value.address",
		"sensor1.value.address",
	}
	waitUntil(t, func() error {
		addressSubjects, err := ListSubjects(client, ".address")
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(addressSubjects, wantSubjects) {
			return fmt.Errorf("address subjects = %v, want %v", addressSubjects, wantSubjects)
		}
		return nil
	})

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
	assertReceived(t, addressUpdates, `area.sensor2.value.address={"driver":"modbus"}`)
	assertReceived(t, addressUpdates, `sensor1.value.address={"driver":"modbus"}`)
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
	wantPrefixSubjects := []string{
		"prefix.area.alpha",
		"prefix.area.beta",
	}
	waitUntil(t, func() error {
		prefixSubjects, err := ListSubjectsPrefix(client, " prefix.area ")
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(prefixSubjects, wantPrefixSubjects) {
			return fmt.Errorf(
				"prefix subjects = %v, want %v",
				prefixSubjects,
				wantPrefixSubjects,
			)
		}
		return nil
	})

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
	assertReceived(t, prefixUpdates, "prefix.area.beta=beta")
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
			wantSubject := systemName + ".test.point.a"
			if subject != wantSubject {
				t.Fatalf(
					"subject = %q, want %q",
					subject,
					wantSubject,
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

func waitGet[V Value](t *testing.T, client *Client, subject string) V {
	t.Helper()
	var value V
	waitUntil(t, func() error {
		got, err := Get[V](client, subject)
		if err != nil {
			return err
		}
		value = got
		return nil
	})
	return value
}

func waitUntil(t *testing.T, check func() error) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		last = check()
		if last == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(last)
}
