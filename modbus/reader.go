package modbus

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	simonmodbus "github.com/simonvetter/modbus"
)

type deviceReader interface {
	ReadCoils(addr uint16, quantity uint16) ([]bool, error)
	ReadDiscreteInputs(addr uint16, quantity uint16) ([]bool, error)
	ReadRawBytes(
		addr uint16,
		quantity uint16,
		regType simonmodbus.RegType,
	) ([]byte, error)
}

func readGroup(reader deviceReader, group pollGroup) ([]any, error) {
	switch group.register {
	case RegisterCoil:
		bits, err := reader.ReadCoils(group.address, group.quantity)
		if err != nil {
			return nil, err
		}
		return decodeBits(group, bits)
	case RegisterDiscreteInput:
		bits, err := reader.ReadDiscreteInputs(group.address, group.quantity)
		if err != nil {
			return nil, err
		}
		return decodeBits(group, bits)
	}

	registerType := simonmodbus.HOLDING_REGISTER
	if group.register == RegisterInputRegister {
		registerType = simonmodbus.INPUT_REGISTER
	}
	raw, err := reader.ReadRawBytes(
		group.address,
		group.quantity*2,
		registerType,
	)
	if err != nil {
		return nil, err
	}
	if len(raw) != int(group.quantity)*2 {
		return nil, fmt.Errorf(
			"read %d bytes from %s %d, want %d",
			len(raw),
			group.register,
			group.address,
			int(group.quantity)*2,
		)
	}
	values := make([]any, 0, len(group.points))
	for _, point := range group.points {
		offset := int(point.point.Address-group.address) * 2
		count := int(registerCount(point.point.Encoding)) * 2
		if offset < 0 || offset+count > len(raw) {
			return nil, fmt.Errorf(
				"point %s is outside the %s block at %d",
				point.subject,
				group.register,
				group.address,
			)
		}
		value, err := decodeRegisters(raw[offset:offset+count], point.point)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func decodeBits(group pollGroup, bits []bool) ([]any, error) {
	if len(bits) != int(group.quantity) {
		return nil, fmt.Errorf(
			"read %d bits from %s %d, want %d",
			len(bits),
			group.register,
			group.address,
			group.quantity,
		)
	}
	values := make([]any, 0, len(group.points))
	for _, point := range group.points {
		index := int(point.point.Address - group.address)
		if index < 0 || index >= len(bits) {
			return nil, fmt.Errorf(
				"point %s is outside the %s block at %d",
				point.subject,
				group.register,
				group.address,
			)
		}
		values = append(values, bits[index])
	}
	return values, nil
}

func readValue(reader deviceReader, point Point) (any, error) {
	values, err := readGroup(reader, pollGroup{
		register: point.Register,
		address:  point.Address,
		quantity: registerCount(point.Encoding),
		points:   []pollPoint{{point: point}},
	})
	if err != nil {
		return nil, err
	}
	return values[0], nil
}

func decodeRegisters(raw []byte, point Point) (any, error) {
	expectedLength := int(registerCount(point.Encoding)) * 2
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
