// Package address defines protocol-neutral hardware address descriptors.
package address

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const CurrentVersion = 1

// ValueType is the stream value produced by a hardware driver.
type ValueType string

const (
	ValueTypeBool    ValueType = "bool"
	ValueTypeInt64   ValueType = "int64"
	ValueTypeFloat64 ValueType = "float64"
	ValueTypeString  ValueType = "string"
)

// Descriptor is the stable envelope stored in a .address subject.
// Config is interpreted by the driver named in Driver.
type Descriptor struct {
	Version    int             `json:"version"`
	Driver     string          `json:"driver"`
	ValueType  ValueType       `json:"value_type"`
	Enabled    bool            `json:"enabled"`
	Connection string          `json:"connection"`
	Config     json.RawMessage `json:"config"`
}

// Connection is the stable envelope stored in a .config subject.
type Connection struct {
	Version int             `json:"version"`
	Driver  string          `json:"driver"`
	Enabled bool            `json:"enabled"`
	Config  json.RawMessage `json:"config"`
}

// Parse decodes and validates a descriptor.
func Parse(value string) (Descriptor, error) {
	var descriptor Descriptor
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&descriptor); err != nil {
		return Descriptor{}, fmt.Errorf("decode address descriptor: %w", err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return Descriptor{}, err
	}
	if err := descriptor.Validate(); err != nil {
		return Descriptor{}, err
	}
	return descriptor, nil
}

// Marshal validates and encodes a descriptor.
func Marshal(descriptor Descriptor) (string, error) {
	if err := descriptor.Validate(); err != nil {
		return "", err
	}
	data, err := json.Marshal(descriptor)
	if err != nil {
		return "", fmt.Errorf("encode address descriptor: %w", err)
	}
	return string(data), nil
}

// ParseConnection decodes and validates a connection descriptor.
func ParseConnection(value string) (Connection, error) {
	var connection Connection
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&connection); err != nil {
		return Connection{}, fmt.Errorf("decode connection descriptor: %w", err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return Connection{}, err
	}
	if err := connection.Validate(); err != nil {
		return Connection{}, err
	}
	return connection, nil
}

// MarshalConnection validates and encodes a connection descriptor.
func MarshalConnection(connection Connection) (string, error) {
	if err := connection.Validate(); err != nil {
		return "", err
	}
	data, err := json.Marshal(connection)
	if err != nil {
		return "", fmt.Errorf("encode connection descriptor: %w", err)
	}
	return string(data), nil
}

// Validate checks the protocol-neutral fields.
func (descriptor Descriptor) Validate() error {
	if descriptor.Version != CurrentVersion {
		return fmt.Errorf(
			"unsupported address descriptor version %d",
			descriptor.Version,
		)
	}
	if strings.TrimSpace(descriptor.Driver) == "" {
		return errors.New("address driver is required")
	}
	if strings.TrimSpace(descriptor.Connection) == "" {
		return errors.New("address connection is required")
	}
	if !strings.HasSuffix(descriptor.Connection, ".config") {
		return errors.New("address connection must end in .config")
	}
	switch descriptor.ValueType {
	case ValueTypeBool, ValueTypeInt64, ValueTypeFloat64, ValueTypeString:
	default:
		return fmt.Errorf(
			"unsupported address value_type %q",
			descriptor.ValueType,
		)
	}
	if len(bytes.TrimSpace(descriptor.Config)) == 0 ||
		!json.Valid(descriptor.Config) {
		return errors.New("address config must be valid JSON")
	}
	return nil
}

// Validate checks the protocol-neutral connection fields.
func (connection Connection) Validate() error {
	if connection.Version != CurrentVersion {
		return fmt.Errorf(
			"unsupported connection descriptor version %d",
			connection.Version,
		)
	}
	if strings.TrimSpace(connection.Driver) == "" {
		return errors.New("connection driver is required")
	}
	if len(bytes.TrimSpace(connection.Config)) == 0 ||
		!json.Valid(connection.Config) {
		return errors.New("connection config must be valid JSON")
	}
	return nil
}

// DecodeConfig decodes the driver-specific configuration.
func DecodeConfig[T any](descriptor Descriptor) (T, error) {
	return decodeConfig[T](descriptor.Driver, descriptor.Config)
}

// DecodeConnectionConfig decodes driver-specific connection configuration.
func DecodeConnectionConfig[T any](connection Connection) (T, error) {
	return decodeConfig[T](connection.Driver, connection.Config)
}

func decodeConfig[T any](driver string, raw json.RawMessage) (T, error) {
	var config T
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, fmt.Errorf(
			"decode %s driver config: %w",
			driver,
			err,
		)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return config, err
	}
	return config, nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("address descriptor contains multiple JSON values")
		}
		return fmt.Errorf("finish decoding address descriptor: %w", err)
	}
	return nil
}
