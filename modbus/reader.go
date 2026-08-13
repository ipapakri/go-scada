package modbus

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	simonmodbus "github.com/simonvetter/modbus"
)

type deviceReader interface {
	ReadCoil(addr uint16) (bool, error)
	ReadDiscreteInput(addr uint16) (bool, error)
	ReadRawBytes(
		addr uint16,
		quantity uint16,
		regType simonmodbus.RegType,
	) ([]byte, error)
}

func readValue(reader deviceReader, point Point) (any, error) {
	switch point.Register {
	case RegisterCoil:
		return reader.ReadCoil(point.Address)
	case RegisterDiscreteInput:
		return reader.ReadDiscreteInput(point.Address)
	}

	registerType := simonmodbus.HOLDING_REGISTER
	if point.Register == RegisterInputRegister {
		registerType = simonmodbus.INPUT_REGISTER
	}
	count := registerCount(point.Encoding) * 2
	raw, err := reader.ReadRawBytes(point.Address, count, registerType)
	if err != nil {
		return nil, err
	}
	expectedLength := int(count)
	if len(raw) != expectedLength {
		return nil, fmt.Errorf(
			"read %d bytes for %s, want %d",
			len(raw),
			point.Encoding,
			expectedLength,
		)
	}
	ordered := reorder(raw, point.ByteOrder, point.WordOrder)
	switch point.Encoding {
	case EncodingInt16:
		return int64(int16(binary.BigEndian.Uint16(ordered))), nil
	case EncodingUint16:
		return int64(binary.BigEndian.Uint16(ordered)), nil
	case EncodingInt32:
		return int64(int32(binary.BigEndian.Uint32(ordered))), nil
	case EncodingUint32:
		return int64(binary.BigEndian.Uint32(ordered)), nil
	case EncodingFloat32:
		return float64(math.Float32frombits(binary.BigEndian.Uint32(ordered))), nil
	case EncodingFloat64:
		return math.Float64frombits(binary.BigEndian.Uint64(ordered)), nil
	default:
		return nil, errors.New("unsupported register encoding")
	}
}

func reorder(raw []byte, byteOrder ByteOrder, wordOrder ByteOrder) []byte {
	ordered := append([]byte(nil), raw...)
	if byteOrder == OrderLittle {
		for index := 0; index < len(ordered); index += 2 {
			ordered[index], ordered[index+1] =
				ordered[index+1], ordered[index]
		}
	}
	if wordOrder == OrderLittle && len(ordered) > 2 {
		for left, right := 0, len(ordered)-2; left < right; left, right =
			left+2, right-2 {
			ordered[left], ordered[right] = ordered[right], ordered[left]
			ordered[left+1], ordered[right+1] =
				ordered[right+1], ordered[left+1]
		}
	}
	return ordered
}
