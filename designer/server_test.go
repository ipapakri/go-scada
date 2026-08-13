package designer

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"go-scada/address"
	"go-scada/alert"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type fakeStore struct {
	mu     sync.Mutex
	values map[string]string
	live   any
}

func newFakeStore() *fakeStore {
	return &fakeStore{values: make(map[string]string)}
}

func (store *fakeStore) ListSubjects(suffix string) ([]string, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var subjects []string
	for subject := range store.values {
		if strings.HasSuffix(subject, suffix) {
			subjects = append(subjects, subject)
		}
	}
	sort.Strings(subjects)
	return subjects, nil
}

func (store *fakeStore) ListSubjectsPrefix(prefix string) ([]string, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var subjects []string
	for subject := range store.values {
		if strings.HasPrefix(subject, prefix) {
			subjects = append(subjects, subject)
		}
	}
	sort.Strings(subjects)
	return subjects, nil
}

func (store *fakeStore) Get(subject string) (string, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.values[subject], nil
}

func (store *fakeStore) Set(subject string, value string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.values[subject] = value
	return nil
}

func (store *fakeStore) SubscribeString(
	subject string,
	handler func(string),
) (Subscription, error) {
	subscription := &fakeSubscription{}
	go handler(store.values[subject])
	return subscription, nil
}

func (store *fakeStore) Subscribe(
	_ string,
	_ address.ValueType,
	handler func(any),
) (Subscription, error) {
	subscription := &fakeSubscription{}
	go handler(store.live)
	return subscription, nil
}

type fakeSubscription struct{}

func (*fakeSubscription) Stop() {}

func TestConnectionAndAddressLifecycle(t *testing.T) {
	store := newFakeStore()
	server, err := NewServer(store, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}

	connection := `{
		"subject":"Modbus.Line1.config",
		"enabled":true,
		"config":{
			"url":"tcp://127.0.0.1:502",
			"unit_id":1,
			"byte_order":"big",
			"word_order":"little",
			"timeout":"2s",
			"poll_interval":"1s"
		}
	}`
	response := performRequest(server.Handler(), http.MethodPost, "/api/connections", connection)
	if response.Code != http.StatusCreated {
		t.Fatalf("create connection status = %d, body = %s", response.Code, response.Body.String())
	}

	point := `{
		"subject":"line1.temperature.address",
		"enabled":true,
		"connection":"Modbus.Line1.config",
		"config":{"register":"holding","address":100,"encoding":"float32"}
	}`
	response = performRequest(server.Handler(), http.MethodPost, "/api/addresses", point)
	if response.Code != http.StatusCreated {
		t.Fatalf("create address status = %d, body = %s", response.Code, response.Body.String())
	}
	var created AddressRecord
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ValueType != address.ValueTypeFloat64 {
		t.Fatalf("value type = %q, want float64", created.ValueType)
	}
	if created.TelemetrySubject != "line1.temperature" {
		t.Fatalf("telemetry subject = %q", created.TelemetrySubject)
	}

	response = performRequest(
		server.Handler(),
		http.MethodDelete,
		"/api/addresses/line1.temperature.address",
		"",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("disable address status = %d, body = %s", response.Code, response.Body.String())
	}
	descriptor, err := address.Parse(store.values["line1.temperature.address"])
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.Enabled {
		t.Fatal("soft-deleted address is still enabled")
	}
}

func TestAddressRejectsIncompatibleEncoding(t *testing.T) {
	store := newFakeStore()
	server, _ := NewServer(store, log.New(io.Discard, "", 0))
	point := `{
		"subject":"line1.switch.address",
		"enabled":true,
		"connection":"Modbus.Line1.config",
		"config":{"register":"coil","address":1,"encoding":"float32"}
	}`
	response := performRequest(server.Handler(), http.MethodPost, "/api/addresses", point)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "requires bool encoding") {
		t.Fatalf("unexpected validation response: %s", response.Body.String())
	}
}

func TestAlertPropertiesAndConfigLifecycle(t *testing.T) {
	store := newFakeStore()
	server, _ := NewServer(store, log.New(io.Discard, "", 0))

	properties := `{
		"subject":"AlertProperties.Alarm",
		"color":"#dc2626",
		"abbreviation":"ALM",
		"short_sign":"!",
		"priority":10,
		"requires_acknowledgement":true
	}`
	response := performRequest(
		server.Handler(), http.MethodPost, "/api/alert-properties", properties,
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("create properties status = %d, body = %s", response.Code, response.Body.String())
	}

	configs := []struct {
		name string
		body string
	}{
		{
			name: "binary",
			body: `{
				"subject":"tank.high.alert_config",
				"enabled":true,
				"type":"binary",
				"binary":{
					"bad_value":true,
					"true":{"property":"AlertProperties.Alarm","text":"High"},
					"false":{"text":"Normal"}
				}
			}`,
		},
		{
			name: "value",
			body: `{
				"subject":"tank.temperature.alert_config",
				"enabled":true,
				"type":"value",
				"value":{
					"value_type":"float64",
					"intervals":[
						{"min":null,"max":80,"active":false,"text":"Normal"},
						{"min":80,"max":null,"active":true,"property":"AlertProperties.Alarm","text":"Hot"}
					]
				}
			}`,
		},
		{
			name: "summary",
			body: `{
				"subject":"tank.alert_config",
				"enabled":true,
				"type":"summary",
				"summary":{"members":["tank.high.alert","tank.temperature.alert"]}
			}`,
		},
	}
	for _, test := range configs {
		t.Run(test.name, func(t *testing.T) {
			response := performRequest(
				server.Handler(), http.MethodPost, "/api/alert-configs", test.body,
			)
			if response.Code != http.StatusCreated {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			var item AlertConfigRecord
			if err := json.Unmarshal(response.Body.Bytes(), &item); err != nil {
				t.Fatal(err)
			}
			if item.InputSubject == "" || item.OutputSubject != item.InputSubject+".alert" {
				t.Fatalf("unexpected derived subjects: %#v", item)
			}
		})
	}

	response = performRequest(server.Handler(), http.MethodGet, "/api/alert-properties", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list properties status = %d, body = %s", response.Code, response.Body.String())
	}
	var propertyItems []AlertPropertiesRecord
	if err := json.Unmarshal(response.Body.Bytes(), &propertyItems); err != nil {
		t.Fatal(err)
	}
	if len(propertyItems) != 1 || propertyItems[0].ReferenceCount != 2 {
		t.Fatalf("unexpected property references: %#v", propertyItems)
	}

	response = performRequest(
		server.Handler(), http.MethodDelete,
		"/api/alert-configs/tank.high.alert_config", "",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("disable config status = %d, body = %s", response.Code, response.Body.String())
	}
	config, err := alert.ParseConfig(store.values["tank.high.alert_config"])
	if err != nil {
		t.Fatal(err)
	}
	if config.Enabled {
		t.Fatal("soft-disabled alert config is still enabled")
	}
}

func TestAlertConfigurationValidation(t *testing.T) {
	store := newFakeStore()
	server, _ := NewServer(store, log.New(io.Discard, "", 0))

	badProperties := `{
		"subject":"AlertProperties.Too.Many",
		"color":"#fff",
		"abbreviation":"ALM",
		"short_sign":"!",
		"priority":1,
		"requires_acknowledgement":true
	}`
	response := performRequest(
		server.Handler(), http.MethodPost, "/api/alert-properties", badProperties,
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("bad properties status = %d, body = %s", response.Code, response.Body.String())
	}

	badIntervals := `{
		"subject":"tank.temperature.alert_config",
		"enabled":true,
		"type":"value",
		"value":{
			"value_type":"float64",
			"intervals":[
				{"min":null,"max":80,"active":false,"text":"Normal"},
				{"min":81,"max":null,"active":true,"property":"AlertProperties.Alarm","text":"Hot"}
			]
		}
	}`
	response = performRequest(
		server.Handler(), http.MethodPost, "/api/alert-configs", badIntervals,
	)
	if response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), "exact boundary") {
		t.Fatalf("bad intervals status = %d, body = %s", response.Code, response.Body.String())
	}

	selfSummary := `{
		"subject":"tank.alert_config",
		"enabled":true,
		"type":"summary",
		"summary":{"members":["tank.alert"]}
	}`
	response = performRequest(
		server.Handler(), http.MethodPost, "/api/alert-configs", selfSummary,
	)
	if response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), "cannot include itself") {
		t.Fatalf("self summary status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestAlertsCanBeListedAndAcknowledged(t *testing.T) {
	store := newFakeStore()
	store.values["tank.level.alert"] = `{
		"version":1,
		"active":true,
		"pending":true,
		"property":"AlertProperties.Alarm",
		"color":"#dc2626",
		"abbreviation":"ALM",
		"short_sign":"!",
		"priority":10,
		"requires_acknowledgement":true,
		"text":"Tank level is high",
		"acknowledged":false,
		"came_time":"2026-08-13T12:00:00Z",
		"episode_id":"episode-1"
	}`
	server, _ := NewServer(store, log.New(io.Discard, "", 0))

	response := performRequest(server.Handler(), http.MethodGet, "/api/alerts", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list alerts status = %d, body = %s", response.Code, response.Body.String())
	}
	var items []AlertRecord
	if err := json.Unmarshal(response.Body.Bytes(), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Subject != "tank.level.alert" ||
		items[0].State.Text != "Tank level is high" {
		t.Fatalf("unexpected alerts: %#v", items)
	}

	response = performRequest(
		server.Handler(),
		http.MethodPost,
		"/api/alerts/tank.level.alert/acknowledge",
		"",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("acknowledge status = %d, body = %s", response.Code, response.Body.String())
	}
	var request map[string]any
	if err := json.Unmarshal([]byte(store.values["tank.level.alert"]), &request); err != nil {
		t.Fatal(err)
	}
	if acknowledged, _ := request["acknowledged"].(bool); !acknowledged {
		t.Fatalf("acknowledgement request was not written: %#v", request)
	}
	if request["episode_id"] != "episode-1" {
		t.Fatalf("episode id changed: %#v", request["episode_id"])
	}
}

func TestAcknowledgementRejectsNonPendingAlert(t *testing.T) {
	store := newFakeStore()
	store.values["tank.level.alert"] = `{
		"version":1,
		"active":false,
		"pending":false,
		"text":"Tank level is normal",
		"acknowledged":true
	}`
	server, _ := NewServer(store, log.New(io.Discard, "", 0))

	response := performRequest(
		server.Handler(),
		http.MethodPost,
		"/api/alerts/tank.level.alert/acknowledge",
		"",
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", response.Code, response.Body.String())
	}
}

func TestLiveWebSocketPublishesTypedValue(t *testing.T) {
	store := newFakeStore()
	store.live = float64(21.5)
	store.values["line1.temperature.address"] = `{
		"version":1,
		"driver":"modbus",
		"value_type":"float64",
		"enabled":true,
		"connection":"Modbus.Line1.config",
		"config":{"register":"holding","address":100,"encoding":"float32"}
	}`
	server, _ := NewServer(store, log.New(io.Discard, "", 0))
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(
		ctx,
		"ws"+strings.TrimPrefix(httpServer.URL, "http")+"/api/live",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(websocket.StatusNormalClosure, "")
	if err := wsjson.Write(ctx, connection, liveRequest{
		Action: "subscribe", Subject: "line1.temperature.address",
	}); err != nil {
		t.Fatal(err)
	}
	var event liveEvent
	if err := wsjson.Read(ctx, connection, &event); err != nil {
		t.Fatal(err)
	}
	if event.Type != "value" || event.Subject != "line1.temperature.address" {
		t.Fatalf("unexpected live event: %#v", event)
	}
	if value, ok := event.Value.(float64); !ok || value != 21.5 {
		t.Fatalf("live value = %#v", event.Value)
	}
}

func performRequest(handler http.Handler, method string, target string, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
