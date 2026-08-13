package modbus

import (
	"math"
	"reflect"
	"testing"

	simonmodbus "github.com/simonvetter/modbus"
)

type fakeReader struct {
	coil     bool
	discrete bool
	raw      []byte
}

func (reader fakeReader) ReadCoil(uint16) (bool, error) {
	return reader.coil, nil
}

func (reader fakeReader) ReadDiscreteInput(uint16) (bool, error) {
	return reader.discrete, nil
}

func (reader fakeReader) ReadRawBytes(
	uint16,
	uint16,
	simonmodbus.RegType,
) ([]byte, error) {
	return append([]byte(nil), reader.raw...), nil
}

func TestReadValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		point     Point
		reader    fakeReader
		want      any
		tolerance float64
	}{
		{
			name:   "coil",
			point:  Point{Register: RegisterCoil},
			reader: fakeReader{coil: true},
			want:   true,
		},
		{
			name: "signed 16 bit",
			point: Point{
				Register:  RegisterHoldingRegister,
				Encoding:  EncodingInt16,
				ByteOrder: OrderBig,
				WordOrder: OrderBig,
			},
			reader: fakeReader{raw: []byte{0xff, 0xfe}},
			want:   int64(-2),
		},
		{
			name: "float32 low word first",
			point: Point{
				Register:  RegisterHoldingRegister,
				Encoding:  EncodingFloat32,
				ByteOrder: OrderBig,
				WordOrder: OrderLittle,
			},
			reader:    fakeReader{raw: []byte{0x00, 0x00, 0x41, 0x48}},
			want:      float64(12.5),
			tolerance: 0.0001,
		},
		{
			name: "uint32 little bytes",
			point: Point{
				Register:  RegisterInputRegister,
				Encoding:  EncodingUint32,
				ByteOrder: OrderLittle,
				WordOrder: OrderBig,
			},
			reader: fakeReader{raw: []byte{0x02, 0x01, 0x04, 0x03}},
			want:   int64(0x01020304),
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := readValue(test.reader, test.point)
			if err != nil {
				t.Fatal(err)
			}
			if test.tolerance > 0 {
				if math.Abs(got.(float64)-test.want.(float64)) > test.tolerance {
					t.Fatalf("readValue() = %v, want %v", got, test.want)
				}
			} else if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("readValue() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestReorderDoesNotMutateInput(t *testing.T) {
	t.Parallel()

	raw := []byte{1, 2, 3, 4}
	got := reorder(raw, OrderLittle, OrderLittle)
	if !reflect.DeepEqual(got, []byte{4, 3, 2, 1}) {
		t.Fatalf("reorder() = %v", got)
	}
	if !reflect.DeepEqual(raw, []byte{1, 2, 3, 4}) {
		t.Fatalf("reorder() mutated input: %v", raw)
	}
}
