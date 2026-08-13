package modbus

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go-scada/address"
)

func TestParseConnectionAndPoint(t *testing.T) {
	t.Parallel()

	connection, err := ParseConnection(address.Connection{
		Version: address.CurrentVersion,
		Driver:  "modbus",
		Enabled: true,
		Config: json.RawMessage(`{
			"url":"tcp://127.0.0.1:502",
			"unit_id":1,
			"byte_order":"big",
			"word_order":"little",
			"timeout":"2s",
			"poll_interval":"250ms"
		}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	descriptor := address.Descriptor{
		Version:    address.CurrentVersion,
		Driver:     "modbus",
		ValueType:  address.ValueTypeFloat64,
		Enabled:    true,
		Connection: "Modbus.Modbus1.config",
		Config: json.RawMessage(`{
			"register":"holding",
			"address":100,
			"encoding":"float32"
		}`),
	}
	point, err := ParsePoint(descriptor, connection)
	if err != nil {
		t.Fatal(err)
	}
	if point.ConnectionSubject != descriptor.Connection ||
		point.URL != "tcp://127.0.0.1:502" ||
		point.UnitID != 1 ||
		point.Register != RegisterHoldingRegister ||
		point.Address != 100 ||
		point.Encoding != EncodingFloat32 ||
		point.WordOrder != OrderLittle ||
		point.Timeout != 2*time.Second ||
		point.PollInterval != 250*time.Millisecond {
		t.Fatalf("unexpected point: %+v", point)
	}
}

func TestParseConnectionValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config string
		match  string
	}{
		{
			name:   "transport",
			config: `{"url":"rtu:///dev/tty0","timeout":"1s","poll_interval":"1s"}`,
			match:  "must be a tcp URL",
		},
		{
			name:   "timeout",
			config: `{"url":"tcp://localhost:502","timeout":"0s","poll_interval":"1s"}`,
			match:  "timeout must be positive",
		},
		{
			name:   "poll interval",
			config: `{"url":"tcp://localhost:502","timeout":"1s"}`,
			match:  "poll_interval is required",
		},
		{
			name:   "unknown point field",
			config: `{"url":"tcp://localhost:502","timeout":"1s","poll_interval":"1s","address":2}`,
			match:  "unknown field",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseConnection(address.Connection{
				Version: address.CurrentVersion,
				Driver:  "modbus",
				Enabled: true,
				Config:  json.RawMessage(test.config),
			})
			if err == nil || !strings.Contains(err.Error(), test.match) {
				t.Fatalf("got error %v, want match %q", err, test.match)
			}
		})
	}
}

func TestParsePointValidation(t *testing.T) {
	t.Parallel()

	connection := Connection{
		URL:          "tcp://localhost:502",
		ByteOrder:    OrderBig,
		WordOrder:    OrderBig,
		Timeout:      time.Second,
		PollInterval: time.Second,
	}
	tests := []struct {
		name      string
		valueType address.ValueType
		config    string
		match     string
	}{
		{
			name:      "register encoding",
			valueType: address.ValueTypeFloat64,
			config:    `{"register":"coil","encoding":"float32"}`,
			match:     "requires bool encoding",
		},
		{
			name:      "value type",
			valueType: address.ValueTypeInt64,
			config:    `{"register":"holding","encoding":"float32"}`,
			match:     "produces \"float64\"",
		},
		{
			name:      "range overflow",
			valueType: address.ValueTypeFloat64,
			config:    `{"register":"holding","address":65535,"encoding":"float64"}`,
			match:     "exceeds the register range",
		},
		{
			name:      "connection override",
			valueType: address.ValueTypeBool,
			config:    `{"register":"coil","encoding":"bool","unit_id":2}`,
			match:     "unknown field",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			descriptor := address.Descriptor{
				Version:    address.CurrentVersion,
				Driver:     "modbus",
				ValueType:  test.valueType,
				Enabled:    true,
				Connection: "connection.config",
				Config:     json.RawMessage(test.config),
			}
			_, err := ParsePoint(descriptor, connection)
			if err == nil || !strings.Contains(err.Error(), test.match) {
				t.Fatalf("got error %v, want match %q", err, test.match)
			}
		})
	}
}
