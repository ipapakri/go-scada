package modbus

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"go-scada/address"
	"go-scada/stream"

	simonmodbus "github.com/simonvetter/modbus"
)

const (
	AddressSuffix    = ".address"
	ConnectionSuffix = ".config"
)

type configurationSubscription interface {
	Stop()
	Closed() <-chan struct{}
}

type configurationSource interface {
	List(suffix string) ([]string, error)
	Get(subject string) (string, error)
	Subscribe(
		suffix string,
		handler func(subject string, value string) error,
	) (configurationSubscription, error)
}

type publisher interface {
	PublishBool(subject string, value bool) error
	PublishInt64(subject string, value int64) error
	PublishFloat64(subject string, value float64) error
}

type deviceClient interface {
	deviceReader
	Close() error
}

type clientFactory interface {
	Open(connection Connection) (deviceClient, error)
}

type pointDefinition struct {
	raw        string
	descriptor address.Descriptor
}

type connectionDefinition struct {
	raw        string
	descriptor address.Connection
	connection Connection
}

// Service discovers shared connections and their dependent Modbus points.
type Service struct {
	source    configurationSource
	publisher publisher
	factory   clientFactory
	logger    *log.Logger

	reconcileMu sync.Mutex
	mu          sync.Mutex
	started     bool
	points      map[string]pointDefinition
	connections map[string]connectionDefinition
	workers     map[string]*worker
	clients     map[string]*managedClient
}

// NewService wires a Modbus polling service to the shared stream.
func NewService(client *stream.Client, logger *log.Logger) (*Service, error) {
	if client == nil {
		return nil, errors.New("stream client is required")
	}
	if logger == nil {
		logger = log.Default()
	}
	return newService(
		streamConfigurationSource{client: client},
		streamPublisher{client: client},
		tcpClientFactory{},
		logger,
	), nil
}

func newService(
	source configurationSource,
	publisher publisher,
	factory clientFactory,
	logger *log.Logger,
) *Service {
	return &Service{
		source:      source,
		publisher:   publisher,
		factory:     factory,
		logger:      logger,
		points:      make(map[string]pointDefinition),
		connections: make(map[string]connectionDefinition),
		workers:     make(map[string]*worker),
		clients:     make(map[string]*managedClient),
	}
}

// Run discovers definitions, follows updates, and blocks until ctx is canceled.
func (service *Service) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("service context is required")
	}
	connectionSubscription, err := service.source.Subscribe(
		ConnectionSuffix,
		func(subject string, value string) error {
			service.reconcileConnection(subject, value)
			return nil
		},
	)
	if err != nil {
		return fmt.Errorf("subscribe to Modbus connections: %w", err)
	}
	addressSubscription, err := service.source.Subscribe(
		AddressSuffix,
		func(subject string, value string) error {
			service.reconcileAddress(subject, value)
			return nil
		},
	)
	if err != nil {
		connectionSubscription.Stop()
		<-connectionSubscription.Closed()
		return fmt.Errorf("subscribe to Modbus addresses: %w", err)
	}
	defer func() {
		addressSubscription.Stop()
		connectionSubscription.Stop()
		<-addressSubscription.Closed()
		<-connectionSubscription.Closed()
		service.stop()
	}()

	if err := service.load(ConnectionSuffix, service.reconcileConnection); err != nil {
		return fmt.Errorf("load Modbus connections: %w", err)
	}
	if err := service.load(AddressSuffix, service.reconcileAddress); err != nil {
		return fmt.Errorf("load Modbus addresses: %w", err)
	}
	service.startWorkers()

	<-ctx.Done()
	return nil
}

func (service *Service) load(
	suffix string,
	reconcile func(subject string, value string),
) error {
	subjects, err := service.source.List(suffix)
	if err != nil {
		return err
	}
	for _, subject := range subjects {
		value, err := service.source.Get(subject)
		if err != nil {
			service.logger.Printf("Read configuration %s failed: %v", subject, err)
			continue
		}
		reconcile(subject, value)
	}
	return nil
}

func (service *Service) reconcileConnection(subject string, value string) {
	service.reconcileMu.Lock()
	defer service.reconcileMu.Unlock()

	descriptor, err := address.ParseConnection(value)
	if err != nil {
		service.invalidateConnection(subject, fmt.Errorf("invalid descriptor: %w", err))
		return
	}
	if descriptor.Driver != "modbus" {
		service.invalidateConnection(subject, nil)
		return
	}
	if !descriptor.Enabled {
		service.invalidateConnection(subject, nil)
		return
	}
	connection, err := ParseConnection(descriptor)
	if err != nil {
		service.invalidateConnection(subject, err)
		return
	}

	service.mu.Lock()
	existing, exists := service.connections[subject]
	if exists && existing.raw == value {
		service.mu.Unlock()
		return
	}
	service.connections[subject] = connectionDefinition{
		raw:        value,
		descriptor: descriptor,
		connection: connection,
	}
	oldClient := service.clients[subject]
	delete(service.clients, subject)
	service.mu.Unlock()

	if service.started {
		service.restartConnection(subject)
	}
	if oldClient != nil {
		oldClient.close()
	}
}

func (service *Service) invalidateConnection(subject string, cause error) {
	service.mu.Lock()
	_, existed := service.connections[subject]
	delete(service.connections, subject)
	oldClient := service.clients[subject]
	delete(service.clients, subject)
	service.mu.Unlock()
	service.stopWorker(subject)
	if oldClient != nil {
		oldClient.close()
	}
	if cause != nil {
		service.logger.Printf("Invalid Modbus connection %s: %v", subject, cause)
	} else if existed {
		service.logger.Printf("Modbus connection %s is no longer active", subject)
	}
}

func (service *Service) reconcileAddress(subject string, value string) {
	service.reconcileMu.Lock()
	defer service.reconcileMu.Unlock()

	descriptor, err := address.Parse(value)
	if err != nil {
		service.removePoint(subject)
		service.logger.Printf("Invalid address %s: %v", subject, err)
		return
	}
	if descriptor.Driver != "modbus" || !descriptor.Enabled {
		service.removePoint(subject)
		return
	}
	service.mu.Lock()
	existing, exists := service.points[subject]
	if exists && existing.raw == value {
		service.mu.Unlock()
		return
	}
	previousConnection := ""
	if exists {
		previousConnection = existing.descriptor.Connection
	}
	service.points[subject] = pointDefinition{
		raw:        value,
		descriptor: descriptor,
	}
	service.mu.Unlock()
	if !service.started {
		return
	}
	if previousConnection != "" && previousConnection != descriptor.Connection {
		service.restartConnection(previousConnection)
	}
	service.restartConnection(descriptor.Connection)
}

func (service *Service) startWorkers() {
	service.reconcileMu.Lock()
	defer service.reconcileMu.Unlock()
	service.started = true
	service.mu.Lock()
	subjects := make([]string, 0, len(service.connections))
	for subject := range service.connections {
		subjects = append(subjects, subject)
	}
	service.mu.Unlock()
	for _, subject := range subjects {
		service.restartConnection(subject)
	}
}

func (service *Service) restartConnection(subject string) {
	service.stopWorker(subject)

	service.mu.Lock()
	connection, connectionExists := service.connections[subject]
	points := service.connectionPointsLocked(subject, connection)
	service.mu.Unlock()
	if !connectionExists || len(points) == 0 {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	next := &worker{cancel: cancel, done: make(chan struct{})}
	service.mu.Lock()
	service.workers[subject] = next
	service.mu.Unlock()
	go service.poll(ctx, next.done, subject, connection.connection, points)
}

func (service *Service) removePoint(subject string) {
	service.mu.Lock()
	definition, exists := service.points[subject]
	delete(service.points, subject)
	service.mu.Unlock()
	if exists && service.started {
		service.restartConnection(definition.descriptor.Connection)
	}
}

func (service *Service) connectionPointsLocked(
	connectionSubject string,
	connection connectionDefinition,
) []pollPoint {
	points := make([]pollPoint, 0)
	for subject, definition := range service.points {
		if definition.descriptor.Connection != connectionSubject {
			continue
		}
		if definition.descriptor.Driver != connection.descriptor.Driver {
			service.logger.Printf(
				"Address %s driver %q does not match connection %s driver %q",
				subject,
				definition.descriptor.Driver,
				connectionSubject,
				connection.descriptor.Driver,
			)
			continue
		}
		point, err := ParsePoint(definition.descriptor, connection.connection)
		if err != nil {
			service.logger.Printf("Invalid Modbus address %s: %v", subject, err)
			continue
		}
		target, ok := targetSubject(subject)
		if !ok {
			service.logger.Printf("Ignoring address with invalid subject %s", subject)
			continue
		}
		points = append(points, pollPoint{subject: target, point: point})
	}
	return points
}

func (service *Service) poll(
	ctx context.Context,
	done chan<- struct{},
	connectionSubject string,
	connection Connection,
	points []pollPoint,
) {
	defer close(done)
	groups := groupPoints(points)
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			client := service.clientFor(connectionSubject, connection)
			for _, group := range groups {
				values, err := client.readGroup(group)
				if err != nil {
					service.logger.Printf(
						"Read Modbus %s %d+%d failed: %v",
						group.register,
						group.address,
						group.quantity,
						err,
					)
					continue
				}
				for index, point := range group.points {
					if err := service.publish(
						point.subject,
						point.point.ValueType,
						values[index],
					); err != nil {
						service.logger.Printf(
							"Publish Modbus point %s failed: %v",
							point.subject,
							err,
						)
					}
				}
			}
			timer.Reset(connection.PollInterval)
		}
	}
}

func (service *Service) publish(
	subject string,
	valueType address.ValueType,
	value any,
) error {
	switch valueType {
	case address.ValueTypeBool:
		typed, ok := value.(bool)
		if !ok {
			return fmt.Errorf("decoded value has type %T, want bool", value)
		}
		return service.publisher.PublishBool(subject, typed)
	case address.ValueTypeInt64:
		typed, ok := value.(int64)
		if !ok {
			return fmt.Errorf("decoded value has type %T, want int64", value)
		}
		return service.publisher.PublishInt64(subject, typed)
	case address.ValueTypeFloat64:
		typed, ok := value.(float64)
		if !ok {
			return fmt.Errorf("decoded value has type %T, want float64", value)
		}
		return service.publisher.PublishFloat64(subject, typed)
	default:
		return fmt.Errorf("unsupported Modbus value type %q", valueType)
	}
}

func (service *Service) clientFor(
	subject string,
	connection Connection,
) *managedClient {
	service.mu.Lock()
	defer service.mu.Unlock()
	client := service.clients[subject]
	if client == nil {
		client = &managedClient{
			factory:    service.factory,
			connection: connection,
		}
		service.clients[subject] = client
	}
	return client
}

func (service *Service) stopWorker(subject string) {
	service.mu.Lock()
	current := service.workers[subject]
	delete(service.workers, subject)
	service.mu.Unlock()
	if current != nil {
		current.stop()
	}
}

func (service *Service) stop() {
	service.reconcileMu.Lock()
	defer service.reconcileMu.Unlock()
	service.started = false
	service.mu.Lock()
	workers := make([]*worker, 0, len(service.workers))
	for _, current := range service.workers {
		workers = append(workers, current)
	}
	service.workers = make(map[string]*worker)
	clients := make([]*managedClient, 0, len(service.clients))
	for _, client := range service.clients {
		clients = append(clients, client)
	}
	service.clients = make(map[string]*managedClient)
	service.mu.Unlock()
	for _, current := range workers {
		current.stop()
	}
	for _, client := range clients {
		client.close()
	}
}

type worker struct {
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

func (current *worker) stop() {
	current.once.Do(func() {
		current.cancel()
		<-current.done
	})
}

type managedClient struct {
	mu         sync.Mutex
	factory    clientFactory
	connection Connection
	client     deviceClient
}

func (client *managedClient) readGroup(group pollGroup) ([]any, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.client == nil {
		opened, err := client.factory.Open(client.connection)
		if err != nil {
			return nil, err
		}
		client.client = opened
	}
	values, err := readGroup(client.client, group)
	if err != nil {
		_ = client.client.Close()
		client.client = nil
	}
	return values, err
}

func (client *managedClient) close() {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.client != nil {
		_ = client.client.Close()
		client.client = nil
	}
}

func targetSubject(addressSubject string) (string, bool) {
	if len(addressSubject) <= len(AddressSuffix) ||
		addressSubject[len(addressSubject)-len(AddressSuffix):] != AddressSuffix {
		return "", false
	}
	return addressSubject[:len(addressSubject)-len(AddressSuffix)], true
}

type streamConfigurationSource struct {
	client *stream.Client
}

func (source streamConfigurationSource) List(suffix string) ([]string, error) {
	return stream.ListSubjects(source.client, suffix)
}

func (source streamConfigurationSource) Get(subject string) (string, error) {
	return stream.Get[string](source.client, subject)
}

func (source streamConfigurationSource) Subscribe(
	suffix string,
	handler func(subject string, value string) error,
) (configurationSubscription, error) {
	return stream.SubscribeSuffix(source.client, suffix, handler)
}

type streamPublisher struct {
	client *stream.Client
}

func (publisher streamPublisher) PublishBool(subject string, value bool) error {
	return stream.Set(publisher.client, subject, value)
}

func (publisher streamPublisher) PublishInt64(subject string, value int64) error {
	return stream.Set(publisher.client, subject, value)
}

func (publisher streamPublisher) PublishFloat64(
	subject string,
	value float64,
) error {
	return stream.Set(publisher.client, subject, value)
}

type tcpClientFactory struct{}

func (tcpClientFactory) Open(connection Connection) (deviceClient, error) {
	client, err := simonmodbus.NewClient(&simonmodbus.ClientConfiguration{
		URL:     connection.URL,
		Timeout: connection.Timeout,
	})
	if err != nil {
		return nil, fmt.Errorf(
			"configure Modbus client %s: %w",
			connection.URL,
			err,
		)
	}
	if err := client.SetUnitId(connection.UnitID); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf(
			"set Modbus unit ID %d: %w",
			connection.UnitID,
			err,
		)
	}
	if err := client.Open(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf(
			"connect to Modbus endpoint %s: %w",
			connection.URL,
			err,
		)
	}
	return client, nil
}
