package modbus

import (
	"reflect"
	"testing"
)

func TestGroupPointsMergesSameTable(t *testing.T) {
	t.Parallel()

	points := []pollPoint{
		holding("plant.002.temp", 4, EncodingFloat32),
		holding("plant.001.temp", 4, EncodingFloat32),
		holding("plant.level", 0, EncodingFloat32),
		coil("plant.valve", 0),
		coil("plant.pump", 1),
		input("plant.flow", 200, EncodingUint16),
	}
	groups := groupPoints(points)
	if len(groups) != 3 {
		t.Fatalf("groups = %d, want 3: %+v", len(groups), summarizeGroups(groups))
	}
	if groups[0].register != RegisterCoil ||
		groups[0].address != 0 ||
		groups[0].quantity != 2 ||
		len(groups[0].points) != 2 {
		t.Fatalf("coil group = %+v", groups[0])
	}
	if groups[1].register != RegisterHoldingRegister ||
		groups[1].address != 0 ||
		groups[1].quantity != 6 ||
		len(groups[1].points) != 3 {
		t.Fatalf("holding group = %+v", groups[1])
	}
	if groups[2].register != RegisterInputRegister ||
		groups[2].address != 200 ||
		groups[2].quantity != 1 {
		t.Fatalf("input group = %+v", groups[2])
	}
	subjects := make([]string, 0, 3)
	for _, point := range groups[1].points {
		subjects = append(subjects, point.subject)
	}
	if !reflect.DeepEqual(subjects, []string{
		"plant.level",
		"plant.001.temp",
		"plant.002.temp",
	}) {
		t.Fatalf("holding subjects = %v", subjects)
	}
}

func TestGroupPointsSplitsOverProtocolLimit(t *testing.T) {
	t.Parallel()

	points := []pollPoint{
		holding("low", 0, EncodingUint16),
		holding("high", maxRegisterQuantity, EncodingUint16),
	}
	groups := groupPoints(points)
	if len(groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(groups))
	}
	if groups[0].address != 0 || groups[0].quantity != 1 {
		t.Fatalf("first group = %+v", groups[0])
	}
	if groups[1].address != maxRegisterQuantity || groups[1].quantity != 1 {
		t.Fatalf("second group = %+v", groups[1])
	}
}

func TestGroupPointsEmpty(t *testing.T) {
	t.Parallel()

	if groups := groupPoints(nil); groups != nil {
		t.Fatalf("groupPoints(nil) = %+v", groups)
	}
}

func holding(subject string, address uint16, encoding Encoding) pollPoint {
	return pollPoint{
		subject: subject,
		point: Point{
			Register: RegisterHoldingRegister,
			Address:  address,
			Encoding: encoding,
		},
	}
}

func input(subject string, address uint16, encoding Encoding) pollPoint {
	return pollPoint{
		subject: subject,
		point: Point{
			Register: RegisterInputRegister,
			Address:  address,
			Encoding: encoding,
		},
	}
}

func coil(subject string, address uint16) pollPoint {
	return pollPoint{
		subject: subject,
		point: Point{
			Register: RegisterCoil,
			Address:  address,
			Encoding: EncodingBool,
		},
	}
}

func summarizeGroups(groups []pollGroup) []string {
	summaries := make([]string, 0, len(groups))
	for _, group := range groups {
		summaries = append(summaries, string(group.register))
	}
	return summaries
}
