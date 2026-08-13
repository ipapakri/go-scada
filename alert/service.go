package alert

import (
	"context"
	"errors"
	"fmt"
	"log"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go-scada/stream"
)

type serviceSubscription interface {
	Stop()
	Closed() <-chan struct{}
}

// serviceStream deliberately has typed subscription methods: a value alert
// never accepts a numerically-convertible value of the wrong stream type.
type serviceStream interface {
	ListSuffix(string) ([]string, error)
	ListPrefix(string) ([]string, error)
	GetString(string) (string, error)
	SubscribeSuffixString(string, func(string, string) error) (serviceSubscription, error)
	SubscribePrefixString(string, func(string, string) error) (serviceSubscription, error)
	SubscribeBool(string, func(string, bool) error) (serviceSubscription, error)
	SubscribeInt64(string, func(string, int64) error) (serviceSubscription, error)
	SubscribeFloat64(string, func(string, float64) error) (serviceSubscription, error)
	PublishString(string, string) error
}

type streamService struct{ client *stream.Client }

func (value streamService) ListSuffix(suffix string) ([]string, error) {
	return stream.ListSubjects(value.client, suffix)
}
func (value streamService) ListPrefix(prefix string) ([]string, error) {
	return stream.ListSubjectsPrefix(value.client, prefix)
}
func (value streamService) GetString(subject string) (string, error) {
	return stream.Get[string](value.client, subject)
}
func (value streamService) SubscribeSuffixString(
	suffix string,
	handler func(string, string) error,
) (serviceSubscription, error) {
	return stream.SubscribeSuffix(value.client, suffix, handler)
}
func (value streamService) SubscribePrefixString(
	prefix string,
	handler func(string, string) error,
) (serviceSubscription, error) {
	return stream.SubscribePrefix(value.client, prefix, handler)
}
func (value streamService) SubscribeBool(
	subject string,
	handler func(string, bool) error,
) (serviceSubscription, error) {
	return stream.Subscribe(value.client, subject, handler)
}
func (value streamService) SubscribeInt64(
	subject string,
	handler func(string, int64) error,
) (serviceSubscription, error) {
	return stream.Subscribe(value.client, subject, handler)
}
func (value streamService) SubscribeFloat64(
	subject string,
	handler func(string, float64) error,
) (serviceSubscription, error) {
	return stream.Subscribe(value.client, subject, handler)
}
func (value streamService) PublishString(subject, state string) error {
	return stream.Set(value.client, subject, state)
}

type alertDefinition struct {
	raw    string
	config Config
	output string
	source string
	input  any
	sub    serviceSubscription
}

// Service discovers alert definitions and maintains their canonical states.
type Service struct {
	stream serviceStream
	logger *log.Logger
	now    func() time.Time
	newID  func() string

	reconcileMu sync.Mutex
	properties  map[string]Properties
	definitions map[string]*alertDefinition
	byOutput    map[string]string
	states      map[string]State
	published   map[string]string
	dirty       map[string]bool
	globals     []serviceSubscription
	running     bool
}

var episodeSequence atomic.Uint64

// NewService wires an alert service to the shared stream.
func NewService(client *stream.Client, logger *log.Logger) (*Service, error) {
	if client == nil {
		return nil, errors.New("stream client is required")
	}
	if logger == nil {
		logger = log.Default()
	}
	return newService(streamService{client: client}, logger), nil
}

func newService(source serviceStream, logger *log.Logger) *Service {
	if logger == nil {
		logger = log.Default()
	}
	return &Service{
		stream:      source,
		logger:      logger,
		now:         time.Now,
		newID:       defaultEpisodeID,
		properties:  make(map[string]Properties),
		definitions: make(map[string]*alertDefinition),
		byOutput:    make(map[string]string),
		states:      make(map[string]State),
		published:   make(map[string]string),
		dirty:       make(map[string]bool),
	}
}

func defaultEpisodeID() string {
	return fmt.Sprintf(
		"%d-%d",
		time.Now().UTC().UnixNano(),
		episodeSequence.Add(1),
	)
}

// Run loads retained definitions and state, follows updates, and blocks until
// ctx is canceled.
func (service *Service) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("service context is required")
	}
	service.reconcileMu.Lock()
	if service.running {
		service.reconcileMu.Unlock()
		return errors.New("alert service is already running")
	}
	service.running = true
	service.reconcileMu.Unlock()

	// Load retained state before live consumers start mutating it. Ordered
	// consumers use DeliverLastPolicy, so updates racing this load are still
	// delivered after the subscriptions are established.
	if err := service.loadInitial(); err != nil {
		service.stop()
		return err
	}
	if err := service.subscribeGlobals(); err != nil {
		service.stop()
		return err
	}
	<-ctx.Done()
	service.stop()
	return nil
}

func (service *Service) subscribeGlobals() error {
	propertySub, err := service.stream.SubscribePrefixString(
		strings.TrimSuffix(propertiesPrefix, "."),
		func(subject, value string) error {
			service.reconcileMu.Lock()
			defer service.reconcileMu.Unlock()
			service.reconcilePropertyLocked(subject, value)
			return nil
		},
	)
	if err != nil {
		return fmt.Errorf("subscribe to alert properties: %w", err)
	}
	configSub, err := service.stream.SubscribeSuffixString(
		configSuffix,
		func(subject, value string) error {
			service.reconcileMu.Lock()
			defer service.reconcileMu.Unlock()
			service.reconcileConfigLocked(subject, value)
			return nil
		},
	)
	if err != nil {
		propertySub.Stop()
		return fmt.Errorf("subscribe to alert configs: %w", err)
	}
	stateSub, err := service.stream.SubscribeSuffixString(
		alertSuffix,
		func(subject, value string) error {
			service.reconcileMu.Lock()
			defer service.reconcileMu.Unlock()
			service.reconcileStateLocked(subject, value)
			return nil
		},
	)
	if err != nil {
		propertySub.Stop()
		configSub.Stop()
		return fmt.Errorf("subscribe to alert states: %w", err)
	}
	service.reconcileMu.Lock()
	service.globals = []serviceSubscription{propertySub, configSub, stateSub}
	service.reconcileMu.Unlock()
	return nil
}

func (service *Service) loadInitial() error {
	if err := service.load(
		func() ([]string, error) { return service.stream.ListSuffix(alertSuffix) },
		func(subject, value string) {
			state, err := ParseState(value)
			if err != nil {
				service.logger.Printf("Invalid retained alert state %s: %v", subject, err)
				return
			}
			service.states[subject] = state
		},
	); err != nil {
		return fmt.Errorf("load alert states: %w", err)
	}
	if err := service.load(
		func() ([]string, error) {
			return service.stream.ListPrefix(strings.TrimSuffix(propertiesPrefix, "."))
		},
		service.reconcilePropertyLocked,
	); err != nil {
		return fmt.Errorf("load alert properties: %w", err)
	}
	if err := service.load(
		func() ([]string, error) { return service.stream.ListSuffix(configSuffix) },
		service.reconcileConfigLocked,
	); err != nil {
		return fmt.Errorf("load alert configs: %w", err)
	}
	service.reconcileMu.Lock()
	defer service.reconcileMu.Unlock()
	service.evaluateAllSummariesLocked()
	return nil
}

func (service *Service) load(
	list func() ([]string, error),
	apply func(string, string),
) error {
	subjects, err := list()
	if err != nil {
		return err
	}
	sort.Strings(subjects)
	for _, subject := range subjects {
		value, err := service.stream.GetString(subject)
		if err != nil {
			service.logger.Printf("Read %s failed: %v", subject, err)
			continue
		}
		service.reconcileMu.Lock()
		apply(subject, value)
		service.reconcileMu.Unlock()
	}
	return nil
}

func (service *Service) reconcilePropertyLocked(subject, raw string) {
	properties, err := ParseProperties(raw)
	if err != nil {
		delete(service.properties, subject)
		service.logger.Printf("Invalid alert properties %s: %v", subject, err)
	} else {
		service.properties[subject] = properties
	}
	for configSubject, definition := range service.definitions {
		if definition.config.Type != TypeSummary &&
			definitionReferences(definition.config, subject) &&
			definition.input != nil {
			service.evaluateDefinitionLocked(configSubject)
		}
	}
	service.evaluateAllSummariesLocked()
}

func definitionReferences(config Config, property string) bool {
	if config.Binary != nil {
		return config.Binary.True.Property == property ||
			config.Binary.False.Property == property
	}
	if config.Value != nil {
		for _, interval := range config.Value.Intervals {
			if interval.Property == property {
				return true
			}
		}
	}
	return false
}

func (service *Service) reconcileConfigLocked(subject, raw string) {
	service.removeDefinitionLocked(subject)
	config, err := ParseConfig(raw)
	if err == nil {
		err = config.ValidateForSubject(subject)
	}
	output, outputErr := OutputSubject(subject)
	source, sourceErr := InputSubject(subject)
	if err != nil || outputErr != nil || sourceErr != nil {
		if err == nil {
			err = errors.Join(outputErr, sourceErr)
		}
		service.logger.Printf("Invalid alert config %s: %v", subject, err)
		if outputErr == nil {
			service.makeInactiveLocked(output)
		}
		service.evaluateAllSummariesLocked()
		return
	}
	if !config.Enabled {
		service.makeInactiveLocked(output)
		service.evaluateAllSummariesLocked()
		return
	}

	definition := &alertDefinition{
		raw:    raw,
		config: config,
		output: output,
		source: source,
	}
	service.definitions[subject] = definition
	service.byOutput[output] = subject
	if config.Type == TypeSummary {
		if service.summaryCycleLocked(output) {
			service.logger.Printf("Invalid summary alert %s: dependency cycle", subject)
			service.removeDefinitionLocked(subject)
			service.makeInactiveLocked(output)
		} else {
			service.evaluateDefinitionLocked(subject)
		}
		service.evaluateAllSummariesLocked()
		return
	}

	var sub serviceSubscription
	switch config.Type {
	case TypeBinary:
		sub, err = service.stream.SubscribeBool(
			source,
			func(_ string, value bool) error {
				service.reconcileMu.Lock()
				defer service.reconcileMu.Unlock()
				service.acceptInputLocked(subject, definition, value)
				return nil
			},
		)
	case TypeValue:
		switch config.Value.ValueType {
		case ValueTypeInt64:
			sub, err = service.stream.SubscribeInt64(
				source,
				func(_ string, value int64) error {
					service.reconcileMu.Lock()
					defer service.reconcileMu.Unlock()
					service.acceptInputLocked(subject, definition, value)
					return nil
				},
			)
		case ValueTypeFloat64:
			sub, err = service.stream.SubscribeFloat64(
				source,
				func(_ string, value float64) error {
					service.reconcileMu.Lock()
					defer service.reconcileMu.Unlock()
					service.acceptInputLocked(subject, definition, value)
					return nil
				},
			)
		}
	}
	if err != nil {
		service.logger.Printf("Subscribe alert input for %s failed: %v", subject, err)
		service.removeDefinitionLocked(subject)
		service.makeInactiveLocked(output)
		return
	}
	definition.sub = sub
}

func (service *Service) removeDefinitionLocked(subject string) {
	definition := service.definitions[subject]
	if definition == nil {
		return
	}
	delete(service.definitions, subject)
	delete(service.byOutput, definition.output)
	if definition.sub != nil {
		definition.sub.Stop()
	}
}

func (service *Service) acceptInputLocked(
	subject string,
	expected *alertDefinition,
	value any,
) {
	definition := service.definitions[subject]
	if definition == nil || definition != expected {
		return
	}
	definition.input = value
	service.evaluateDefinitionLocked(subject)
	service.evaluateAllSummariesLocked()
}

func (service *Service) evaluateDefinitionLocked(subject string) {
	definition := service.definitions[subject]
	if definition == nil {
		return
	}
	var (
		evaluation Evaluation
		err        error
	)
	switch definition.config.Type {
	case TypeBinary:
		value, ok := definition.input.(bool)
		if !ok {
			return
		}
		evaluation, err = EvaluateBinary(
			*definition.config.Binary,
			value,
			service.properties,
		)
	case TypeValue:
		if definition.input == nil {
			return
		}
		evaluation, err = EvaluateValue(
			*definition.config.Value,
			definition.input,
			service.properties,
		)
	case TypeSummary:
		if service.summaryCycleLocked(definition.output) {
			err = errors.New("summary dependency cycle")
			break
		}
		members := make(map[string]State, len(definition.config.Summary.Members))
		for _, member := range definition.config.Summary.Members {
			state, exists := service.states[member]
			if !exists {
				err = fmt.Errorf("summary member %q has no runtime state", member)
				break
			}
			members[member] = state
		}
		if err == nil {
			evaluation, err = EvaluateSummary(*definition.config.Summary, members)
		}
	}
	if err != nil {
		service.logger.Printf("Evaluate alert %s failed: %v", subject, err)
		service.makeInactiveLocked(definition.output)
		return
	}
	service.applyLocked(definition.output, evaluation)
}

func (service *Service) applyLocked(output string, evaluation Evaluation) {
	var previous *State
	if state, exists := service.states[output]; exists {
		copy := state
		previous = &copy
	}
	next, err := ApplyEvaluation(
		previous,
		evaluation,
		service.now(),
		service.newID(),
	)
	if err != nil {
		service.logger.Printf("Apply alert evaluation %s failed: %v", output, err)
		return
	}
	service.storeAndPublishLocked(output, next)
}

func (service *Service) makeInactiveLocked(output string) {
	service.applyLocked(output, Evaluation{Acknowledged: true})
}

func (service *Service) storeAndPublishLocked(subject string, state State) {
	previous, exists := service.states[subject]
	if exists && reflect.DeepEqual(previous, state) && !service.dirty[subject] {
		return
	}
	encoded, err := MarshalState(state)
	if err != nil {
		service.logger.Printf("Encode alert state %s failed: %v", subject, err)
		return
	}
	service.states[subject] = state
	if err := service.stream.PublishString(subject, encoded); err != nil {
		delete(service.published, subject)
		service.dirty[subject] = true
		service.logger.Printf("Publish alert state %s failed: %v", subject, err)
		return
	}
	service.published[subject] = encoded
	delete(service.dirty, subject)
}

func (service *Service) reconcileStateLocked(subject, raw string) {
	incoming, err := parseStateUnchecked(raw)
	if err != nil {
		if _, owned := service.byOutput[subject]; owned {
			service.republishCanonicalLocked(subject)
		}
		return
	}
	canonical, exists := service.states[subject]
	_, serviceOwned := service.byOutput[subject]
	if !serviceOwned || !exists {
		if err := incoming.Validate(); err != nil {
			service.logger.Printf("Invalid external alert state %s: %v", subject, err)
			return
		}
		service.states[subject] = incoming
		service.evaluateAllSummariesLocked()
		return
	}
	if reflect.DeepEqual(canonical, incoming) {
		return
	}
	if incoming.Acknowledged && canonical.Pending &&
		incoming.EpisodeID == canonical.EpisodeID {
		if definition := service.definitionForOutputLocked(subject); definition != nil && definition.config.Type == TypeSummary {
			service.acknowledgeTreeLocked(subject, make(map[string]bool))
		} else {
			service.acknowledgeOneLocked(subject)
		}
		service.evaluateAllSummariesLocked()
		return
	}
	service.republishCanonicalLocked(subject)
}

func (service *Service) republishCanonicalLocked(subject string) {
	state, exists := service.states[subject]
	if !exists {
		return
	}
	encoded, err := MarshalState(state)
	if err != nil {
		return
	}
	if err := service.stream.PublishString(subject, encoded); err != nil {
		delete(service.published, subject)
		service.dirty[subject] = true
		service.logger.Printf("Restore canonical alert state %s failed: %v", subject, err)
		return
	}
	service.published[subject] = encoded
	delete(service.dirty, subject)
}

func (service *Service) acknowledgeOneLocked(subject string) {
	state, exists := service.states[subject]
	if !exists || !state.Pending {
		return
	}
	next, err := Acknowledge(state, state.EpisodeID, service.now())
	if err != nil {
		service.logger.Printf("Acknowledge alert %s failed: %v", subject, err)
		return
	}
	service.storeAndPublishLocked(subject, next)
}

func (service *Service) acknowledgeTreeLocked(
	subject string,
	visiting map[string]bool,
) {
	if visiting[subject] {
		return
	}
	visiting[subject] = true
	definition := service.definitionForOutputLocked(subject)
	if definition != nil && definition.config.Type == TypeSummary {
		for _, member := range definition.config.Summary.Members {
			state, exists := service.states[member]
			if exists && (state.Active || state.Pending) {
				service.acknowledgeTreeLocked(member, visiting)
			}
		}
	}
	service.acknowledgeOneLocked(subject)
	delete(visiting, subject)
}

func (service *Service) definitionForOutputLocked(
	output string,
) *alertDefinition {
	return service.definitions[service.byOutput[output]]
}

func (service *Service) evaluateAllSummariesLocked() {
	// A valid summary graph is acyclic. Repeated passes propagate nested
	// changes without requiring a separate topological data structure.
	for pass := 0; pass <= len(service.definitions); pass++ {
		changed := false
		for subject, definition := range service.definitions {
			if definition.config.Type != TypeSummary {
				continue
			}
			before := service.states[definition.output]
			service.evaluateDefinitionLocked(subject)
			after := service.states[definition.output]
			if !reflect.DeepEqual(before, after) {
				changed = true
			}
		}
		if !changed {
			return
		}
	}
}

func (service *Service) summaryCycleLocked(start string) bool {
	visiting := make(map[string]bool)
	visited := make(map[string]bool)
	var visit func(string) bool
	visit = func(output string) bool {
		if visiting[output] {
			return true
		}
		if visited[output] {
			return false
		}
		visited[output] = true
		visiting[output] = true
		definition := service.definitionForOutputLocked(output)
		if definition != nil && definition.config.Type == TypeSummary {
			for _, member := range definition.config.Summary.Members {
				if visit(member) {
					return true
				}
			}
		}
		delete(visiting, output)
		return false
	}
	return visit(start)
}

func (service *Service) stop() {
	service.reconcileMu.Lock()
	globals := append([]serviceSubscription(nil), service.globals...)
	service.globals = nil
	inputs := make([]serviceSubscription, 0, len(service.definitions))
	for _, definition := range service.definitions {
		if definition.sub != nil {
			inputs = append(inputs, definition.sub)
			definition.sub.Stop()
			definition.sub = nil
		}
	}
	for _, sub := range globals {
		sub.Stop()
	}
	service.running = false
	service.reconcileMu.Unlock()
	for _, sub := range append(globals, inputs...) {
		<-sub.Closed()
	}
}
