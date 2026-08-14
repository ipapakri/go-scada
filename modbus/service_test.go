package modbus

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go-scada/address"

	simonmodbus "github.com/simonvetter/modbus"
)

func TestServiceFollowsSharedConnectionLifecycle(t *testing.T) {
	source := newFakeConfigurationSource()
	connectionSubject := "Modbus.Modbus1.config"
	source.values["sensor1.value.address"] = pointDescriptorJSON(
		t,
		true,
		connectionSubject,
	)
	source.values["area.sensor2.value.address"] = pointDescriptorJSON(
		t,
		true,
		connectionSubject,
	)
	factory := &fakeClientFactory{}
	publisher := &fakePublisher{
		bools:  make(chan publishedValue[bool], 20),
		ints:   make(chan publishedValue[int64], 20),
		floats: make(chan publishedValue[float64], 20),
	}
	service := newService(
		source,
		publisher,
		factory,
		log.New(io.Discard, "", 0),
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- service.Run(ctx)
	}()
	source.waitSubscribed(t)

	select {
	case value := <-publisher.floats:
		t.Fatalf("point started without its connection: %+v", value)
	case <-time.After(30 * time.Millisecond):
	}

	source.emit(
		t,
		connectionSubject,
		connectionDescriptorJSON(t, true, "modbus", "tcp://first:502"),
	)
	first := receiveSubjectValues(t, publisher.floats, 2)
	if first["sensor1.value"] != 12.5 ||
		first["area.sensor2.value"] != 12.5 {
		t.Fatalf("first publications = %v", first)
	}
	if got := factory.opens.Load(); got != 1 {
		t.Fatalf("shared connection opens = %d, want 1", got)
	}

	drain(publisher.floats)
	source.emit(
		t,
		connectionSubject,
		connectionDescriptorJSON(t, true, "modbus", "tcp://second:502"),
	)
	second := receiveSubjectValues(t, publisher.floats, 2)
	if second["sensor1.value"] != 13.5 ||
		second["area.sensor2.value"] != 13.5 {
		t.Fatalf("updated publications = %v", second)
	}
	if got := factory.opens.Load(); got != 2 {
		t.Fatalf("connection opens after update = %d, want 2", got)
	}
	if factory.closes.Load() < 1 {
		t.Fatal("connection update did not close the old client")
	}

	drain(publisher.floats)
	source.emit(
		t,
		connectionSubject,
		connectionDescriptorJSON(t, false, "modbus", "tcp://second:502"),
	)
	select {
	case value := <-publisher.floats:
		t.Fatalf("publication after connection disable: %+v", value)
	case <-time.After(40 * time.Millisecond):
	}

	source.emit(
		t,
		connectionSubject,
		connectionDescriptorJSON(t, true, "opcua", "tcp://second:502"),
	)
	select {
	case value := <-publisher.floats:
		t.Fatalf("publication with mismatched connection driver: %+v", value)
	case <-time.After(30 * time.Millisecond):
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("service did not stop")
	}
}

func TestAddressDisableStopsOnlyItsWorker(t *testing.T) {
	source := newFakeConfigurationSource()
	connectionSubject := "Modbus.Modbus1.config"
	source.values[connectionSubject] =
		connectionDescriptorJSON(t, true, "modbus", "tcp://first:502")
	source.values["sensor1.value.address"] =
		pointDescriptorJSON(t, true, connectionSubject)
	factory := &fakeClientFactory{}
	publisher := &fakePublisher{
		bools:  make(chan publishedValue[bool], 10),
		ints:   make(chan publishedValue[int64], 10),
		floats: make(chan publishedValue[float64], 10),
	}
	service := newService(
		source,
		publisher,
		factory,
		log.New(io.Discard, "", 0),
	)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()
	source.waitSubscribed(t)
	_ = receiveWithin(t, publisher.floats)
	drain(publisher.floats)

	source.emit(
		t,
		"sensor1.value.address",
		pointDescriptorJSON(t, false, connectionSubject),
	)
	select {
	case value := <-publisher.floats:
		t.Fatalf("publication after point disable: %+v", value)
	case <-time.After(40 * time.Millisecond):
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestTargetSubject(t *testing.T) {
	t.Parallel()

	got, ok := targetSubject("area.sensor.value.address")
	if !ok || got != "area.sensor.value" {
		t.Fatalf("targetSubject() = %q, %v", got, ok)
	}
	if _, ok := targetSubject(".address"); ok {
		t.Fatal("targetSubject() accepted an empty target")
	}
}

func TestManagedClientReconnectsAfterReadFailure(t *testing.T) {
	t.Parallel()

	factory := &retryClientFactory{}
	client := &managedClient{
		factory: factory,
		connection: Connection{
			URL:     "tcp://device:502",
			Timeout: time.Second,
		},
	}
	group := pollGroup{
		register: RegisterHoldingRegister,
		address:  0,
		quantity: 1,
		points: []pollPoint{{
			point: Point{
				Register:  RegisterHoldingRegister,
				Encoding:  EncodingUint16,
				ByteOrder: OrderBig,
				WordOrder: OrderBig,
			},
		}},
	}
	if _, err := client.readGroup(group); err == nil {
		t.Fatal("first read succeeded, want transient failure")
	}
	values, err := client.readGroup(group)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0] != int64(42) {
		t.Fatalf("retried value = %v, want [42]", values)
	}
	if factory.opens.Load() != 2 {
		t.Fatalf("client opens = %d, want 2", factory.opens.Load())
	}
}

func TestServiceReadsGroupedAddressesOnce(t *testing.T) {
	source := newFakeConfigurationSource()
	connectionSubject := "Modbus.Modbus1.config"
	source.values[connectionSubject] =
		connectionDescriptorJSON(t, true, "modbus", "tcp://first:502")
	source.values["sensor1.value.address"] = pointDescriptorJSONAt(
		t, true, connectionSubject, 0,
	)
	source.values["sensor2.value.address"] = pointDescriptorJSONAt(
		t, true, connectionSubject, 4,
	)
	factory := &recordingClientFactory{}
	publisher := &fakePublisher{
		bools:  make(chan publishedValue[bool], 10),
		ints:   make(chan publishedValue[int64], 10),
		floats: make(chan publishedValue[float64], 10),
	}
	service := newService(
		source,
		publisher,
		factory,
		log.New(io.Discard, "", 0),
	)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()
	source.waitSubscribed(t)
	first := receiveSubjectValues(t, publisher.floats, 2)
	if first["sensor1.value"] != 12.5 || first["sensor2.value"] != 20 {
		t.Fatalf("publications = %v", first)
	}
	reads := factory.reads()
	if len(reads) == 0 {
		t.Fatal("expected a grouped register read")
	}
	for _, read := range reads {
		if read.addr != 0 || read.quantity != 12 {
			t.Fatalf("reads = %+v, want 12-byte blocks at 0", reads)
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestServiceSkipsUnchangedValuesWhenConfigured(t *testing.T) {
	source := newFakeConfigurationSource()
	connectionSubject := "Modbus.Modbus1.config"
	source.values[connectionSubject] =
		connectionDescriptorJSON(t, true, "modbus", "tcp://first:502")
	source.values["sensor1.value.address"] = pointDescriptorJSONOnChange(
		t, true, connectionSubject, true,
	)
	factory := &fakeClientFactory{}
	publisher := &fakePublisher{
		bools:  make(chan publishedValue[bool], 10),
		ints:   make(chan publishedValue[int64], 10),
		floats: make(chan publishedValue[float64], 10),
	}
	service := newService(
		source,
		publisher,
		factory,
		log.New(io.Discard, "", 0),
	)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()
	source.waitSubscribed(t)

	first := receiveWithin(t, publisher.floats)
	if first.subject != "sensor1.value" || first.value != 12.5 {
		t.Fatalf("first publication = %+v", first)
	}
	drain(publisher.floats)
	select {
	case value := <-publisher.floats:
		t.Fatalf("repeated publication of unchanged value: %+v", value)
	case <-time.After(40 * time.Millisecond):
	}

	source.emit(
		t,
		connectionSubject,
		connectionDescriptorJSON(t, true, "modbus", "tcp://second:502"),
	)
	changed := receiveWithin(t, publisher.floats)
	if changed.subject != "sensor1.value" || changed.value != 13.5 {
		t.Fatalf("changed publication = %+v", changed)
	}
	drain(publisher.floats)
	select {
	case value := <-publisher.floats:
		t.Fatalf("repeated publication after change: %+v", value)
	case <-time.After(40 * time.Millisecond):
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

type fakeConfigurationSource struct {
	mu         sync.Mutex
	values     map[string]string
	handlers   map[string]func(string, string) error
	subscribed chan struct{}
	count      int
}

func newFakeConfigurationSource() *fakeConfigurationSource {
	return &fakeConfigurationSource{
		values:     make(map[string]string),
		handlers:   make(map[string]func(string, string) error),
		subscribed: make(chan struct{}),
	}
}

func (source *fakeConfigurationSource) List(suffix string) ([]string, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	subjects := make([]string, 0)
	for subject := range source.values {
		if strings.HasSuffix(subject, suffix) {
			subjects = append(subjects, subject)
		}
	}
	return subjects, nil
}

func (source *fakeConfigurationSource) Get(subject string) (string, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.values[subject], nil
}

func (source *fakeConfigurationSource) Subscribe(
	suffix string,
	handler func(string, string) error,
) (configurationSubscription, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.handlers[suffix] = handler
	source.count++
	if source.count == 2 {
		close(source.subscribed)
	}
	return newFakeSubscription(), nil
}

func (source *fakeConfigurationSource) emit(
	t *testing.T,
	subject string,
	value string,
) {
	t.Helper()
	source.mu.Lock()
	source.values[subject] = value
	var handler func(string, string) error
	for suffix, candidate := range source.handlers {
		if strings.HasSuffix(subject, suffix) {
			handler = candidate
			break
		}
	}
	source.mu.Unlock()
	if handler == nil {
		t.Fatalf("no subscription for %s", subject)
	}
	if err := handler(subject, value); err != nil {
		t.Fatal(err)
	}
}

func (source *fakeConfigurationSource) waitSubscribed(t *testing.T) {
	t.Helper()
	select {
	case <-source.subscribed:
	case <-time.After(time.Second):
		t.Fatal("service did not subscribe")
	}
}

type fakeSubscription struct {
	closed chan struct{}
	once   sync.Once
}

func newFakeSubscription() *fakeSubscription {
	return &fakeSubscription{closed: make(chan struct{})}
}

func (subscription *fakeSubscription) Stop() {
	subscription.once.Do(func() { close(subscription.closed) })
}

func (subscription *fakeSubscription) Closed() <-chan struct{} {
	return subscription.closed
}

type publishedValue[T any] struct {
	subject string
	value   T
}

type fakePublisher struct {
	bools  chan publishedValue[bool]
	ints   chan publishedValue[int64]
	floats chan publishedValue[float64]
}

func (publisher *fakePublisher) PublishBool(subject string, value bool) error {
	publisher.bools <- publishedValue[bool]{subject, value}
	return nil
}

func (publisher *fakePublisher) PublishInt64(
	subject string,
	value int64,
) error {
	publisher.ints <- publishedValue[int64]{subject, value}
	return nil
}

func (publisher *fakePublisher) PublishFloat64(
	subject string,
	value float64,
) error {
	publisher.floats <- publishedValue[float64]{subject, value}
	return nil
}

type fakeClientFactory struct {
	opens  atomic.Int64
	closes atomic.Int64
}

func (factory *fakeClientFactory) Open(
	connection Connection,
) (deviceClient, error) {
	factory.opens.Add(1)
	value := float32(12.5)
	if strings.Contains(connection.URL, "second") {
		value = 13.5
	}
	bits := math.Float32bits(value)
	return &fakeDeviceClient{
		factory: factory,
		raw: []byte{
			byte(bits >> 24),
			byte(bits >> 16),
			byte(bits >> 8),
			byte(bits),
		},
	}, nil
}

type fakeDeviceClient struct {
	factory *fakeClientFactory
	raw     []byte
}

type retryClientFactory struct {
	opens atomic.Int64
}

func (factory *retryClientFactory) Open(Connection) (deviceClient, error) {
	attempt := factory.opens.Add(1)
	return &retryDeviceClient{fail: attempt == 1}, nil
}

type retryDeviceClient struct {
	fail bool
}

func (*retryDeviceClient) ReadCoils(uint16, uint16) ([]bool, error) {
	return []bool{false}, nil
}

func (*retryDeviceClient) ReadDiscreteInputs(uint16, uint16) ([]bool, error) {
	return []bool{false}, nil
}

func (client *retryDeviceClient) ReadRawBytes(
	uint16,
	uint16,
	simonmodbus.RegType,
) ([]byte, error) {
	if client.fail {
		return nil, errors.New("temporary read failure")
	}
	return []byte{0, 42}, nil
}

func (*retryDeviceClient) Close() error {
	return nil
}

func (*fakeDeviceClient) ReadCoils(uint16, uint16) ([]bool, error) {
	return []bool{false}, nil
}

func (*fakeDeviceClient) ReadDiscreteInputs(uint16, uint16) ([]bool, error) {
	return []bool{false}, nil
}

func (client *fakeDeviceClient) ReadRawBytes(
	uint16,
	uint16,
	simonmodbus.RegType,
) ([]byte, error) {
	return append([]byte(nil), client.raw...), nil
}

func (client *fakeDeviceClient) Close() error {
	client.factory.closes.Add(1)
	return nil
}

func connectionDescriptorJSON(
	t *testing.T,
	enabled bool,
	driver string,
	url string,
) string {
	t.Helper()
	value, err := address.MarshalConnection(address.Connection{
		Version: address.CurrentVersion,
		Driver:  driver,
		Enabled: enabled,
		Config: []byte(`{
			"url":"` + url + `",
			"unit_id":1,
			"byte_order":"big",
			"word_order":"big",
			"timeout":"100ms",
			"poll_interval":"10ms"
		}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func pointDescriptorJSON(
	t *testing.T,
	enabled bool,
	connection string,
) string {
	t.Helper()
	return pointDescriptorJSONAt(t, enabled, connection, 0)
}

func pointDescriptorJSONAt(
	t *testing.T,
	enabled bool,
	connection string,
	addressOffset uint16,
) string {
	t.Helper()
	return marshalPointDescriptor(t, enabled, connection, addressOffset, false)
}

func pointDescriptorJSONOnChange(
	t *testing.T,
	enabled bool,
	connection string,
	publishOnChange bool,
) string {
	t.Helper()
	return marshalPointDescriptor(t, enabled, connection, 0, publishOnChange)
}

func marshalPointDescriptor(
	t *testing.T,
	enabled bool,
	connection string,
	addressOffset uint16,
	publishOnChange bool,
) string {
	t.Helper()
	value, err := address.Marshal(address.Descriptor{
		Version:         address.CurrentVersion,
		Driver:          "modbus",
		ValueType:       address.ValueTypeFloat64,
		Enabled:         enabled,
		Connection:      connection,
		PublishOnChange: publishOnChange,
		Config: []byte(fmt.Sprintf(`{
			"register":"holding",
			"address":%d,
			"encoding":"float32"
		}`, addressOffset)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

type recordedRead struct {
	addr     uint16
	quantity uint16
}

type recordingClientFactory struct {
	mu    sync.Mutex
	calls []recordedRead
}

func (factory *recordingClientFactory) Open(Connection) (deviceClient, error) {
	return &recordingDeviceClient{factory: factory}, nil
}

func (factory *recordingClientFactory) reads() []recordedRead {
	factory.mu.Lock()
	defer factory.mu.Unlock()
	return append([]recordedRead(nil), factory.calls...)
}

type recordingDeviceClient struct {
	factory *recordingClientFactory
}

func (*recordingDeviceClient) ReadCoils(uint16, uint16) ([]bool, error) {
	return []bool{false}, nil
}

func (*recordingDeviceClient) ReadDiscreteInputs(uint16, uint16) ([]bool, error) {
	return []bool{false}, nil
}

func (client *recordingDeviceClient) ReadRawBytes(
	addr uint16,
	quantity uint16,
	_ simonmodbus.RegType,
) ([]byte, error) {
	client.factory.mu.Lock()
	client.factory.calls = append(client.factory.calls, recordedRead{
		addr:     addr,
		quantity: quantity,
	})
	client.factory.mu.Unlock()
	low := math.Float32bits(12.5)
	high := math.Float32bits(20)
	raw := make([]byte, quantity)
	copy(raw, []byte{
		byte(low >> 24), byte(low >> 16), byte(low >> 8), byte(low),
		0, 0, 0, 0,
		byte(high >> 24), byte(high >> 16), byte(high >> 8), byte(high),
	})
	return raw, nil
}

func (*recordingDeviceClient) Close() error {
	return nil
}

func receiveWithin[T any](t *testing.T, values <-chan T) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(time.Second):
		var zero T
		t.Fatal("timed out waiting for value")
		return zero
	}
}

func receiveSubjectValues(
	t *testing.T,
	values <-chan publishedValue[float64],
	count int,
) map[string]float64 {
	t.Helper()
	result := make(map[string]float64, count)
	for len(result) < count {
		value := receiveWithin(t, values)
		result[value.subject] = value.value
	}
	return result
}

func drain[T any](values <-chan T) {
	for {
		select {
		case <-values:
		default:
			return
		}
	}
}
