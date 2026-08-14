package address

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDescriptorRoundTrip(t *testing.T) {
	t.Parallel()

	original := Descriptor{
		Version:         CurrentVersion,
		Driver:          "modbus",
		ValueType:       ValueTypeFloat64,
		Enabled:         true,
		Connection:      "Modbus.Modbus1.config",
		PublishOnChange: true,
		Config:          json.RawMessage(`{"url":"tcp://127.0.0.1:502"}`),
	}
	value, err := Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Version != original.Version ||
		decoded.Driver != original.Driver ||
		decoded.ValueType != original.ValueType ||
		decoded.Enabled != original.Enabled ||
		decoded.Connection != original.Connection ||
		decoded.PublishOnChange != original.PublishOnChange ||
		string(decoded.Config) != string(original.Config) {
		t.Fatalf("round trip mismatch: got %+v, want %+v", decoded, original)
	}
}

func TestDescriptorOmitsPublishOnChangeByDefault(t *testing.T) {
	t.Parallel()

	decoded, err := Parse(`{
		"version":1,
		"driver":"modbus",
		"value_type":"bool",
		"enabled":true,
		"connection":"c.config",
		"config":{}
	}`)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.PublishOnChange {
		t.Fatal("omitted publish_on_change should default to false")
	}
	encoded, err := Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encoded, "publish_on_change") {
		t.Fatalf("false publish_on_change should be omitted, got %s", encoded)
	}
}

func TestDescriptorValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		match string
	}{
		{
			name:  "unknown version",
			value: `{"version":2,"driver":"modbus","value_type":"bool","enabled":true,"connection":"c.config","config":{}}`,
			match: "unsupported address descriptor version",
		},
		{
			name:  "missing driver",
			value: `{"version":1,"driver":"","value_type":"bool","enabled":true,"connection":"c.config","config":{}}`,
			match: "driver is required",
		},
		{
			name:  "missing connection",
			value: `{"version":1,"driver":"modbus","value_type":"bool","enabled":true,"config":{}}`,
			match: "connection is required",
		},
		{
			name:  "invalid connection subject",
			value: `{"version":1,"driver":"modbus","value_type":"bool","enabled":true,"connection":"connection","config":{}}`,
			match: "must end in .config",
		},
		{
			name:  "unknown value type",
			value: `{"version":1,"driver":"modbus","value_type":"uint16","enabled":true,"connection":"c.config","config":{}}`,
			match: "unsupported address value_type",
		},
		{
			name:  "unknown field",
			value: `{"version":1,"driver":"modbus","value_type":"bool","enabled":true,"connection":"c.config","config":{},"typo":1}`,
			match: "unknown field",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := Parse(test.value)
			if err == nil || !strings.Contains(err.Error(), test.match) {
				t.Fatalf("got error %v, want match %q", err, test.match)
			}
		})
	}
}

func TestDecodeConfigRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	descriptor := Descriptor{
		Version:    CurrentVersion,
		Driver:     "test",
		ValueType:  ValueTypeBool,
		Enabled:    true,
		Connection: "test.config",
		Config:     json.RawMessage(`{"name":"ok","typo":true}`),
	}
	_, err := DecodeConfig[struct {
		Name string `json:"name"`
	}](descriptor)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("got error %v, want unknown field", err)
	}
}

func TestConnectionRoundTrip(t *testing.T) {
	t.Parallel()

	original := Connection{
		Version: CurrentVersion,
		Driver:  "modbus",
		Enabled: true,
		Config:  json.RawMessage(`{"url":"tcp://127.0.0.1:502"}`),
	}
	value, err := MarshalConnection(original)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ParseConnection(value)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Version != original.Version ||
		decoded.Driver != original.Driver ||
		decoded.Enabled != original.Enabled ||
		string(decoded.Config) != string(original.Config) {
		t.Fatalf("round trip mismatch: got %+v, want %+v", decoded, original)
	}
}
