package alert

import (
	"context"
	"errors"
	"io"
	"log"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"go-scada/stream"
)

func TestServiceEvaluatesTransitionsAndReconcilesUpdates(t *testing.T) {
	source := newFakeAlertStream()
	source.values["AlertProperties.Good"] = propertiesJSON(t, "green", 1, false)
	source.values["AlertProperties.Bad"] = propertiesJSON(t, "red", 10, true)
	source.values["Pump.trip.alert_config"] = binaryConfigJSON(t, true, false)
	service := newTestService(source)
	clock := newTestClock()
	service.now = clock.now
	service.newID = func() string { return "episode-1" }
	cancel, done := runAlertService(t, service)
	source.waitExact(t, "Pump.trip")

	source.emitBool(t, "Pump.trip", false)
	first := source.waitState(t, "Pump.trip.alert")
	if !first.Active || !first.Pending || first.Text != "bad" ||
		first.Property != "AlertProperties.Bad" {
		t.Fatalf("bad input state = %+v", first)
	}
	if !first.CameTime.Equal(clock.value) || first.EpisodeID != "episode-1" {
		t.Fatalf("new episode lifecycle = %+v", first)
	}

	source.emitString(
		t,
		"AlertProperties.Bad",
		propertiesJSON(t, "orange", 12, true),
	)
	updated := source.waitState(t, "Pump.trip.alert")
	if updated.Color != "orange" || updated.Priority != 12 ||
		!updated.Pending || !updated.CameTime.Equal(*first.CameTime) {
		t.Fatalf("property update state = %+v", updated)
	}

	clock.advance(time.Minute)
	source.emitBool(t, "Pump.trip", true)
	cleared := source.waitState(t, "Pump.trip.alert")
	if cleared.Active || !cleared.Pending || cleared.WentTime == nil ||
		!cleared.WentTime.Equal(clock.value) || cleared.Property != "" {
		t.Fatalf("cleared state did not latch = %+v", cleared)
	}

	source.emitString(
		t,
		"Pump.trip.alert_config",
		binaryConfigJSON(t, true, true),
	)
	source.waitExact(t, "Pump.trip")
	source.emitBool(t, "Pump.trip", true)
	changed := source.waitState(t, "Pump.trip.alert")
	if !changed.Active {
		t.Fatalf("updated bad_value was not applied: %+v", changed)
	}

	source.emitString(
		t,
		"Pump.trip.alert_config",
		binaryConfigJSON(t, false, true),
	)
	if source.exactActive("Pump.trip") {
		t.Fatal("input subscription remained active after disable")
	}

	cancel()
	waitRunDone(t, done)
	if source.activeSubscriptions() != 0 {
		t.Fatalf("active subscriptions after shutdown = %d", source.activeSubscriptions())
	}
}

func TestServiceRestoresStateAndHandlesAcknowledgementWrites(t *testing.T) {
	source := newFakeAlertStream()
	source.selfDeliver = true
	source.values["AlertProperties.Good"] = propertiesJSON(t, "green", 1, false)
	source.values["AlertProperties.Bad"] = propertiesJSON(t, "red", 10, true)
	source.values["Pump.trip.alert_config"] = binaryConfigJSON(t, true, false)

	firstService := newTestService(source)
	clock := newTestClock()
	firstService.now = clock.now
	firstService.newID = func() string { return "restored-episode" }
	cancel, done := runAlertService(t, firstService)
	source.waitExact(t, "Pump.trip")
	source.emitBool(t, "Pump.trip", false)
	original := source.waitState(t, "Pump.trip.alert")
	cancel()
	waitRunDone(t, done)

	source.resetPublications()
	clock.advance(time.Hour)
	secondService := newTestService(source)
	secondService.now = clock.now
	secondService.newID = func() string { return "must-not-be-used" }
	cancel, done = runAlertService(t, secondService)
	source.waitExact(t, "Pump.trip")
	source.emitBool(t, "Pump.trip", false)
	time.Sleep(10 * time.Millisecond)
	restored := source.currentState(t, "Pump.trip.alert")
	if restored.EpisodeID != original.EpisodeID ||
		!restored.CameTime.Equal(*original.CameTime) {
		t.Fatalf("state was not restored: got %+v, want episode %+v", restored, original)
	}

	request := restored
	request.Acknowledged = true
	request.Text = "tampered"
	request.Color = "purple"
	request.AckTime = nil
	requestJSON, err := encode(request, "acknowledgement request")
	if err != nil {
		t.Fatal(err)
	}
	source.emitString(t, "Pump.trip.alert", requestJSON)
	acknowledged := source.waitState(t, "Pump.trip.alert")
	if acknowledged.Pending || !acknowledged.Acknowledged ||
		acknowledged.Text != restored.Text ||
		acknowledged.Color != restored.Color ||
		acknowledged.AckTime == nil {
		t.Fatalf("acknowledgement did not preserve canonical fields: %+v", acknowledged)
	}

	stale := acknowledged
	stale.EpisodeID = "stale-episode"
	stale.Text = "stale"
	source.emitString(t, "Pump.trip.alert", stateJSON(t, stale))
	rewritten := source.waitState(t, "Pump.trip.alert")
	if !reflect.DeepEqual(rewritten, acknowledged) {
		t.Fatalf("stale write was not rewritten: got %+v, want %+v", rewritten, acknowledged)
	}

	before := source.publishCount()
	time.Sleep(20 * time.Millisecond)
	after := source.publishCount()
	if after != before {
		t.Fatalf("self-delivery caused publication loop: %d -> %d", before, after)
	}
	cancel()
	waitRunDone(t, done)
}

func TestServiceUsesTypedNumericSubscriptions(t *testing.T) {
	source := newFakeAlertStream()
	source.values["AlertProperties.Good"] = propertiesJSON(t, "green", 1, false)
	source.values["AlertProperties.Bad"] = propertiesJSON(t, "red", 10, false)
	source.values["Tank.level.alert_config"] = valueConfigJSON(t, ValueTypeInt64)
	source.values["Tank.temp.alert_config"] = valueConfigJSON(t, ValueTypeFloat64)
	service := newTestService(source)
	cancel, done := runAlertService(t, service)
	source.waitExact(t, "Tank.level")
	source.waitExact(t, "Tank.temp")

	source.emitInt64(t, "Tank.level", 11)
	level := source.waitState(t, "Tank.level.alert")
	if !level.Active || level.Text != "high" {
		t.Fatalf("int64 evaluation = %+v", level)
	}
	source.emitFloat64(t, "Tank.temp", 9.5)
	temp := source.waitState(t, "Tank.temp.alert")
	if temp.Active || temp.Text != "normal" {
		t.Fatalf("float64 evaluation = %+v", temp)
	}

	cancel()
	waitRunDone(t, done)
}

func TestServiceSummaryDominanceCycleAndFanout(t *testing.T) {
	source := newFakeAlertStream()
	source.values["AlertProperties.Low"] = propertiesJSON(t, "yellow", 5, true)
	source.values["AlertProperties.High"] = propertiesJSON(t, "red", 20, true)
	source.values["Low.alert_config"] = binaryMappedConfigJSON(
		t, "AlertProperties.Low",
	)
	source.values["High.alert_config"] = binaryMappedConfigJSON(
		t, "AlertProperties.High",
	)
	source.values["Area.alert_config"] = summaryConfigJSON(
		t, true, "Low.alert", "High.alert",
	)
	service := newTestService(source)
	clock := newTestClock()
	service.now = clock.now
	var id int
	service.newID = func() string {
		id++
		return "episode-" + string(rune('0'+id))
	}
	cancel, done := runAlertService(t, service)
	source.waitExact(t, "Low")
	source.waitExact(t, "High")
	source.emitBool(t, "Low", true)
	_ = source.waitState(t, "Low.alert")
	source.emitBool(t, "High", true)
	_ = source.waitState(t, "High.alert")
	summary := source.waitStateMatching(t, "Area.alert", func(state State) bool {
		return state.Dominant == "High.alert"
	})
	if !summary.Active || !summary.Pending || summary.Priority != 20 ||
		!reflect.DeepEqual(summary.Members, []string{"Low.alert", "High.alert"}) {
		t.Fatalf("summary dominance = %+v", summary)
	}

	request := summary
	request.Pending = false
	request.Acknowledged = true
	source.emitString(t, "Area.alert", stateJSON(t, request))
	low := source.waitStateMatching(t, "Low.alert", func(state State) bool {
		return state.Acknowledged
	})
	high := source.waitStateMatching(t, "High.alert", func(state State) bool {
		return state.Acknowledged
	})
	ackedSummary := source.waitStateMatching(t, "Area.alert", func(state State) bool {
		return state.Acknowledged
	})
	if low.Pending || high.Pending || ackedSummary.Pending {
		t.Fatalf("summary acknowledgement did not fan out: low=%+v high=%+v summary=%+v",
			low, high, ackedSummary)
	}

	source.emitString(
		t,
		"CycleA.alert_config",
		summaryConfigJSON(t, true, "CycleB.alert"),
	)
	source.emitString(
		t,
		"CycleB.alert_config",
		summaryConfigJSON(t, true, "CycleA.alert"),
	)
	service.reconcileMu.Lock()
	_, cycleBActive := service.definitions["CycleB.alert_config"]
	service.reconcileMu.Unlock()
	if cycleBActive {
		t.Fatal("cyclic summary remained active")
	}

	cancel()
	waitRunDone(t, done)
}

func TestServiceEvaluatesOnlyAncestorSummaries(t *testing.T) {
	source := newFakeAlertStream()
	source.values["AlertProperties.High"] = propertiesJSON(t, "red", 20, true)
	source.values["Plant1.trip.alert_config"] = binaryMappedConfigJSON(
		t, "AlertProperties.High",
	)
	source.values["Plant2.trip.alert_config"] = binaryMappedConfigJSON(
		t, "AlertProperties.High",
	)
	source.values["Plant1.alert_config"] = summaryConfigJSON(
		t, true, "Plant1.trip.alert",
	)
	source.values["Plant2.alert_config"] = summaryConfigJSON(
		t, true, "Plant2.trip.alert",
	)
	service := newTestService(source)
	cancel, done := runAlertService(t, service)
	source.waitExact(t, "Plant1.trip")
	source.waitExact(t, "Plant2.trip")
	_ = source.waitState(t, "Plant1.alert")
	_ = source.waitState(t, "Plant2.alert")

	service.reconcileMu.Lock()
	if got := service.parents["Plant1.trip.alert"]; len(got) != 1 ||
		got[0] != "Plant1.alert_config" {
		service.reconcileMu.Unlock()
		t.Fatalf("plant1 parents = %v", got)
	}
	if got := service.parents["Plant2.trip.alert"]; len(got) != 1 ||
		got[0] != "Plant2.alert_config" {
		service.reconcileMu.Unlock()
		t.Fatalf("plant2 parents = %v", got)
	}
	service.reconcileMu.Unlock()

	source.resetPublications()
	source.emitBool(t, "Plant1.trip", true)
	_ = source.waitStateMatching(t, "Plant1.alert", func(state State) bool {
		return state.Active
	})
	time.Sleep(20 * time.Millisecond)
	for _, publication := range source.snapshotPublications() {
		if publication.subject == "Plant2.alert" ||
			publication.subject == "Plant2.trip.alert" {
			t.Fatalf("unrelated summary was published: %s", publication.subject)
		}
	}

	cancel()
	waitRunDone(t, done)
}

func TestServiceSummaryConsumesExternalMemberUpdates(t *testing.T) {
	source := newFakeAlertStream()
	inactive := testState("", 0, false, false)
	source.values["External.alert"] = stateJSON(t, inactive)
	source.values["Area.alert_config"] = summaryConfigJSON(
		t, true, "External.alert",
	)
	service := newTestService(source)
	cancel, done := runAlertService(t, service)
	source.waitSuffix(t, alertSuffix)

	active := testState("AlertProperties.Bad", 30, true, true)
	source.emitString(t, "External.alert", stateJSON(t, active))
	summary := source.waitStateMatching(t, "Area.alert", func(state State) bool {
		return state.Dominant == "External.alert"
	})
	if !summary.Active || !summary.Pending || summary.Priority != 30 {
		t.Fatalf("external member update was not consumed: %+v", summary)
	}

	cancel()
	waitRunDone(t, done)
}

type fakeAlertStream struct {
	mu             sync.Mutex
	values         map[string]string
	anys           map[string]any
	prefixHandlers map[string]func(string, string) error
	created        func(stream.SubjectCreated) error
	watches        map[string]*fakeExactWatch
	subscriptions  []*fakeAlertSubscription
	publications   []publishedAlert
	published      chan publishedAlert
	selfDeliver    bool
}

type publishedAlert struct {
	subject string
	value   string
}

func newFakeAlertStream() *fakeAlertStream {
	return &fakeAlertStream{
		values:         make(map[string]string),
		anys:           make(map[string]any),
		prefixHandlers: make(map[string]func(string, string) error),
		watches:        make(map[string]*fakeExactWatch),
		published:      make(chan publishedAlert, 100),
	}
}

func (source *fakeAlertStream) ListSuffix(suffix string) ([]string, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	var result []string
	for subject := range source.values {
		if strings.HasSuffix(subject, suffix) {
			result = append(result, subject)
		}
	}
	sort.Strings(result)
	return result, nil
}

func (source *fakeAlertStream) ListPrefix(prefix string) ([]string, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	prefix = strings.TrimSuffix(prefix, ".") + "."
	var result []string
	for subject := range source.values {
		if strings.HasPrefix(subject, prefix) {
			result = append(result, subject)
		}
	}
	sort.Strings(result)
	return result, nil
}

func (source *fakeAlertStream) GetString(subject string) (string, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.values[subject], nil
}

func (source *fakeAlertStream) GetAny(subject string) (any, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	if value, ok := source.anys[subject]; ok {
		return value, nil
	}
	if value, ok := source.values[subject]; ok {
		return value, nil
	}
	return nil, errors.New("missing subject")
}

func (source *fakeAlertStream) SubscribePrefixString(
	prefix string,
	handler func(string, string) error,
) (serviceSubscription, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	sub := source.newSubscriptionLocked()
	source.prefixHandlers[strings.TrimSuffix(prefix, ".")+"."] = handler
	return sub, nil
}

func (source *fakeAlertStream) SubscribeCreated(
	handler func(stream.SubjectCreated) error,
) (serviceSubscription, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	sub := source.newSubscriptionLocked()
	source.created = handler
	return sub, nil
}

func (source *fakeAlertStream) WatchExact(
	name string,
	handler func(string, any) error,
) (exactWatch, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	sub := source.newSubscriptionLocked()
	watch := &fakeExactWatch{
		source:  source,
		name:    name,
		handler: handler,
		sub:     sub,
		watched: make(map[string]struct{}),
	}
	source.watches[name] = watch
	return watch, nil
}

func (source *fakeAlertStream) PublishString(subject, value string) error {
	source.mu.Lock()
	source.values[subject] = value
	publication := publishedAlert{subject, value}
	source.publications = append(source.publications, publication)
	source.published <- publication
	watch := source.watches[statesWatch]
	selfDeliver := source.selfDeliver
	source.mu.Unlock()
	if selfDeliver && watch != nil {
		go func() { _ = watch.handler(subject, value) }()
	}
	return nil
}

func (source *fakeAlertStream) newSubscriptionLocked() *fakeAlertSubscription {
	sub := &fakeAlertSubscription{closed: make(chan struct{})}
	source.subscriptions = append(source.subscriptions, sub)
	return sub
}

func (source *fakeAlertStream) emitString(
	t *testing.T,
	subject, value string,
) {
	t.Helper()
	source.mu.Lock()
	source.values[subject] = value
	var handler func(string, any) error
	for prefix, candidate := range source.prefixHandlers {
		if strings.HasPrefix(subject, prefix) {
			source.mu.Unlock()
			if err := candidate(subject, value); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	for _, watch := range source.watches {
		if _, watched := watch.watched[subject]; watched && !watch.sub.stopped() {
			handler = watch.handler
			break
		}
	}
	if handler == nil {
		if strings.HasSuffix(subject, configSuffix) {
			if watch := source.watches[configsWatch]; watch != nil && !watch.sub.stopped() {
				handler = watch.handler
			}
		} else if strings.HasSuffix(subject, alertSuffix) {
			if watch := source.watches[statesWatch]; watch != nil && !watch.sub.stopped() {
				handler = watch.handler
			}
		}
	}
	source.mu.Unlock()
	if handler == nil {
		t.Fatalf("no string handler for %s", subject)
	}
	if err := handler(subject, value); err != nil {
		t.Fatal(err)
	}
}

func (source *fakeAlertStream) emitTyped(
	t *testing.T,
	subject string,
	value any,
) {
	t.Helper()
	source.mu.Lock()
	source.anys[subject] = value
	watch := source.watches[inputsWatch]
	watched := watch != nil && !watch.sub.stopped()
	if watched {
		_, watched = watch.watched[subject]
	}
	var handler func(string, any) error
	if watched {
		handler = watch.handler
	}
	source.mu.Unlock()
	if handler == nil {
		t.Fatalf("no active input handler for %s", subject)
	}
	if err := handler(subject, value); err != nil {
		t.Fatal(err)
	}
}

func (source *fakeAlertStream) emitBool(
	t *testing.T,
	subject string,
	value bool,
) {
	t.Helper()
	source.emitTyped(t, subject, value)
}

func (source *fakeAlertStream) emitInt64(
	t *testing.T,
	subject string,
	value int64,
) {
	t.Helper()
	source.emitTyped(t, subject, value)
}

func (source *fakeAlertStream) emitFloat64(
	t *testing.T,
	subject string,
	value float64,
) {
	t.Helper()
	source.emitTyped(t, subject, value)
}

func (source *fakeAlertStream) waitExact(t *testing.T, subject string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if source.exactActive(subject) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("input %s was not subscribed", subject)
}

func (source *fakeAlertStream) waitSuffix(t *testing.T, suffix string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		source.mu.Lock()
		ready := false
		for _, watch := range source.watches {
			if watch.sub.stopped() {
				continue
			}
			if suffix == alertSuffix && watch.name == statesWatch {
				ready = true
			}
			if suffix == configSuffix && watch.name == configsWatch {
				ready = true
			}
			for subject := range watch.watched {
				if strings.HasSuffix(subject, suffix) {
					ready = true
				}
			}
		}
		source.mu.Unlock()
		if ready {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("suffix %s was not subscribed", suffix)
}

func (source *fakeAlertStream) exactActive(subject string) bool {
	source.mu.Lock()
	defer source.mu.Unlock()
	watch := source.watches[inputsWatch]
	if watch == nil || watch.sub.stopped() {
		return false
	}
	_, watched := watch.watched[subject]
	return watched
}

func (source *fakeAlertStream) waitState(t *testing.T, subject string) State {
	t.Helper()
	return source.waitStateMatching(t, subject, func(State) bool { return true })
}

func (source *fakeAlertStream) waitStateMatching(
	t *testing.T,
	subject string,
	match func(State) bool,
) State {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case publication := <-source.published:
			if publication.subject != subject {
				continue
			}
			state, err := ParseState(publication.value)
			if err != nil {
				t.Fatal(err)
			}
			if match(state) {
				return state
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for state %s", subject)
		}
	}
}

func (source *fakeAlertStream) currentState(t *testing.T, subject string) State {
	t.Helper()
	source.mu.Lock()
	value := source.values[subject]
	source.mu.Unlock()
	state, err := ParseState(value)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func (source *fakeAlertStream) resetPublications() {
	source.mu.Lock()
	source.publications = nil
	for len(source.published) > 0 {
		<-source.published
	}
	source.mu.Unlock()
}

func (source *fakeAlertStream) publishCount() int {
	source.mu.Lock()
	defer source.mu.Unlock()
	return len(source.publications)
}

func (source *fakeAlertStream) snapshotPublications() []publishedAlert {
	source.mu.Lock()
	defer source.mu.Unlock()
	return append([]publishedAlert(nil), source.publications...)
}

func (source *fakeAlertStream) activeSubscriptions() int {
	source.mu.Lock()
	defer source.mu.Unlock()
	var count int
	for _, sub := range source.subscriptions {
		if !sub.stopped() {
			count++
		}
	}
	return count
}

type fakeExactWatch struct {
	source  *fakeAlertStream
	name    string
	handler func(string, any) error
	sub     *fakeAlertSubscription
	watched map[string]struct{}
}

func (watch *fakeExactWatch) Add(subject string) error {
	return watch.ensure(subject, true)
}

func (watch *fakeExactWatch) Watch(subject string) error {
	return watch.ensure(subject, false)
}

func (watch *fakeExactWatch) Remove(subject string) error {
	watch.source.mu.Lock()
	defer watch.source.mu.Unlock()
	delete(watch.watched, subject)
	return nil
}

func (watch *fakeExactWatch) ensure(subject string, catchUp bool) error {
	watch.source.mu.Lock()
	watch.watched[subject] = struct{}{}
	var value any
	if catchUp {
		if stored, ok := watch.source.anys[subject]; ok {
			value = stored
		} else if stored, ok := watch.source.values[subject]; ok {
			value = stored
		}
	}
	handler := watch.handler
	watch.source.mu.Unlock()
	if catchUp && value != nil {
		return handler(subject, value)
	}
	return nil
}

func (watch *fakeExactWatch) Stop() {
	watch.sub.Stop()
}

func (watch *fakeExactWatch) Closed() <-chan struct{} {
	return watch.sub.Closed()
}

type fakeAlertSubscription struct {
	mu     sync.Mutex
	closed chan struct{}
	stop   bool
	once   sync.Once
}

func (subscription *fakeAlertSubscription) Stop() {
	subscription.once.Do(func() {
		subscription.mu.Lock()
		subscription.stop = true
		subscription.mu.Unlock()
		close(subscription.closed)
	})
}

func (subscription *fakeAlertSubscription) Closed() <-chan struct{} {
	return subscription.closed
}

func (subscription *fakeAlertSubscription) stopped() bool {
	subscription.mu.Lock()
	defer subscription.mu.Unlock()
	return subscription.stop
}

type testClock struct {
	mu    sync.Mutex
	value time.Time
}

func newTestClock() *testClock {
	return &testClock{value: time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)}
}

func (clock *testClock) now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.value
}

func (clock *testClock) advance(delta time.Duration) {
	clock.mu.Lock()
	clock.value = clock.value.Add(delta)
	clock.mu.Unlock()
}

func newTestService(source serviceStream) *Service {
	return newService(source, log.New(io.Discard, "", 0))
}

func runAlertService(
	t *testing.T,
	service *Service,
) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()
	return cancel, done
}

func waitRunDone(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("alert service did not stop")
	}
}

func propertiesJSON(
	t *testing.T,
	color string,
	priority int,
	requiresAck bool,
) string {
	t.Helper()
	value, err := MarshalProperties(Properties{
		Version:                 CurrentVersion,
		Color:                   color,
		Abbreviation:            strings.ToUpper(color),
		ShortSign:               "!",
		Priority:                priority,
		RequiresAcknowledgement: requiresAck,
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func binaryConfigJSON(
	t *testing.T,
	enabled bool,
	badValue bool,
) string {
	t.Helper()
	config := Config{
		Version: CurrentVersion,
		Enabled: enabled,
		Type:    TypeBinary,
		Binary: &BinaryConfig{
			BadValue: badValue,
			True: Mapping{
				Property: "AlertProperties.Bad",
				Text:     "bad",
			},
			False: Mapping{
				Text: "good",
			},
		},
	}
	if !badValue {
		config.Binary.True = Mapping{Text: "good"}
		config.Binary.False = Mapping{
			Property: "AlertProperties.Bad",
			Text:     "bad",
		}
	}
	value, err := MarshalConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func binaryMappedConfigJSON(
	t *testing.T,
	property string,
) string {
	t.Helper()
	config := Config{
		Version: CurrentVersion,
		Enabled: true,
		Type:    TypeBinary,
		Binary: &BinaryConfig{
			BadValue: true,
			True:     Mapping{Property: property, Text: "active"},
			False:    Mapping{Text: "inactive"},
		},
	}
	value, err := MarshalConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func summaryConfigJSON(
	t *testing.T,
	enabled bool,
	members ...string,
) string {
	t.Helper()
	value, err := MarshalConfig(Config{
		Version: CurrentVersion,
		Enabled: enabled,
		Type:    TypeSummary,
		Summary: &SummaryConfig{Members: members},
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func valueConfigJSON(
	t *testing.T,
	valueType ValueType,
) string {
	t.Helper()
	value, err := MarshalConfig(Config{
		Version: CurrentVersion,
		Enabled: true,
		Type:    TypeValue,
		Value: &ValueConfig{
			ValueType: valueType,
			Intervals: []Interval{
				{
					Max:  number("10"),
					Text: "normal",
				},
				{
					Min:      number("10"),
					Active:   true,
					Property: "AlertProperties.Bad",
					Text:     "high",
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func stateJSON(t *testing.T, state State) string {
	t.Helper()
	value, err := MarshalState(state)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
