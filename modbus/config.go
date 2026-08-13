// Package modbus polls Modbus devices described by stream configuration
// subjects.
package modbus

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"go-scada/address"
)

type Register string

const (
	RegisterCoil            Register = "coil"
	RegisterDiscreteInput   Register = "discrete_input"
	RegisterInputRegister   Register = "input"
	RegisterHoldingRegister Register = "holding"
)

type Encoding string

const (
	EncodingBool    Encoding = "bool"
	EncodingInt16   Encoding = "int16"
	EncodingUint16  Encoding = "uint16"
	EncodingInt32   Encoding = "int32"
	EncodingUint32  Encoding = "uint32"
	EncodingFloat32 Encoding = "float32"
	EncodingFloat64 Encoding = "float64"
)

type ByteOrder string

const (
	OrderBig    ByteOrder = "big"
	OrderLittle ByteOrder = "little"
)

// ConnectionConfig is the driver-specific config in a .config subject.
type ConnectionConfig struct {
	URL          string    `json:"url"`
	UnitID       uint8     `json:"unit_id"`
	ByteOrder    ByteOrder `json:"byte_order"`
	WordOrder    ByteOrder `json:"word_order"`
	Timeout      string    `json:"timeout"`
	PollInterval string    `json:"poll_interval"`
}

// Connection is a validated shared Modbus connection definition.
type Connection struct {
	URL          string
	UnitID       uint8
	ByteOrder    ByteOrder
	WordOrder    ByteOrder
	Timeout      time.Duration
	PollInterval time.Duration
}

// AddressConfig is the point-specific config in an .address subject.
type AddressConfig struct {
	Register Register `json:"register"`
	Address  uint16   `json:"address"`
	Encoding Encoding `json:"encoding"`
}

// Point is a fully resolved Modbus polling definition.
type Point struct {
	ConnectionSubject string
	URL               string
	UnitID            uint8
	Register          Register
	Address           uint16
	Encoding          Encoding
	ByteOrder         ByteOrder
	WordOrder         ByteOrder
	Timeout           time.Duration
	PollInterval      time.Duration
	ValueType         address.ValueType
}

// ParseConnection decodes and validates a Modbus connection descriptor.
func ParseConnection(descriptor address.Connection) (Connection, error) {
	if descriptor.Driver != "modbus" {
		return Connection{}, fmt.Errorf(
			"connection driver %q is not modbus",
			descriptor.Driver,
		)
	}
	config, err := address.DecodeConnectionConfig[ConnectionConfig](descriptor)
	if err != nil {
		return Connection{}, err
	}
	config.URL = strings.TrimSpace(config.URL)
	parsedURL, err := url.Parse(config.URL)
	if err != nil || parsedURL.Scheme != "tcp" || parsedURL.Host == "" {
		return Connection{}, fmt.Errorf(
			"modbus url %q must be a tcp URL with a host",
			config.URL,
		)
	}
	if config.ByteOrder == "" {
		config.ByteOrder = OrderBig
	}
	if config.WordOrder == "" {
		config.WordOrder = OrderBig
	}
	if !validOrder(config.ByteOrder) {
		return Connection{}, fmt.Errorf(
			"unsupported byte_order %q",
			config.ByteOrder,
		)
	}
	if !validOrder(config.WordOrder) {
		return Connection{}, fmt.Errorf(
			"unsupported word_order %q",
			config.WordOrder,
		)
	}
	timeout, err := parsePositiveDuration("timeout", config.Timeout)
	if err != nil {
		return Connection{}, err
	}
	pollInterval, err := parsePositiveDuration(
		"poll_interval",
		config.PollInterval,
	)
	if err != nil {
		return Connection{}, err
	}
	return Connection{
		URL:          config.URL,
		UnitID:       config.UnitID,
		ByteOrder:    config.ByteOrder,
		WordOrder:    config.WordOrder,
		Timeout:      timeout,
		PollInterval: pollInterval,
	}, nil
}

// ParsePoint decodes a point descriptor and combines it with its connection.
func ParsePoint(
	descriptor address.Descriptor,
	connection Connection,
) (Point, error) {
	if descriptor.Driver != "modbus" {
		return Point{}, fmt.Errorf(
			"address driver %q is not modbus",
			descriptor.Driver,
		)
	}
	config, err := address.DecodeConfig[AddressConfig](descriptor)
	if err != nil {
		return Point{}, err
	}
	switch config.Register {
	case RegisterCoil, RegisterDiscreteInput:
		if config.Encoding != EncodingBool {
			return Point{}, fmt.Errorf(
				"register %q requires bool encoding",
				config.Register,
			)
		}
	case RegisterInputRegister, RegisterHoldingRegister:
		switch config.Encoding {
		case EncodingInt16, EncodingUint16, EncodingInt32,
			EncodingUint32, EncodingFloat32, EncodingFloat64:
		default:
			return Point{}, fmt.Errorf(
				"register %q does not support encoding %q",
				config.Register,
				config.Encoding,
			)
		}
	default:
		return Point{}, fmt.Errorf(
			"unsupported modbus register %q",
			config.Register,
		)
	}
	expectedType := valueTypeForEncoding(config.Encoding)
	if descriptor.ValueType != expectedType {
		return Point{}, fmt.Errorf(
			"encoding %q produces %q, not %q",
			config.Encoding,
			expectedType,
			descriptor.ValueType,
		)
	}
	if int(config.Address)+int(registerCount(config.Encoding)) > 1<<16 {
		return Point{}, fmt.Errorf(
			"address %d with encoding %q exceeds the register range",
			config.Address,
			config.Encoding,
		)
	}
	return Point{
		ConnectionSubject: descriptor.Connection,
		URL:               connection.URL,
		UnitID:            connection.UnitID,
		Register:          config.Register,
		Address:           config.Address,
		Encoding:          config.Encoding,
		ByteOrder:         connection.ByteOrder,
		WordOrder:         connection.WordOrder,
		Timeout:           connection.Timeout,
		PollInterval:      connection.PollInterval,
		ValueType:         descriptor.ValueType,
	}, nil
}

func validOrder(order ByteOrder) bool {
	return order == OrderBig || order == OrderLittle
}

func valueTypeForEncoding(encoding Encoding) address.ValueType {
	switch encoding {
	case EncodingBool:
		return address.ValueTypeBool
	case EncodingInt16, EncodingUint16, EncodingInt32, EncodingUint32:
		return address.ValueTypeInt64
	case EncodingFloat32, EncodingFloat64:
		return address.ValueTypeFloat64
	default:
		return ""
	}
}

func registerCount(encoding Encoding) uint16 {
	switch encoding {
	case EncodingInt32, EncodingUint32, EncodingFloat32:
		return 2
	case EncodingFloat64:
		return 4
	default:
		return 1
	}
}

func parsePositiveDuration(name string, value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("modbus connection %s is required", name)
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf(
			"parse Modbus connection %s %q: %w",
			name,
			value,
			err,
		)
	}
	if duration <= 0 {
		return 0, errors.New("modbus connection " + name + " must be positive")
	}
	return duration, nil
}
