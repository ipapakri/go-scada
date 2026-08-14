package modbus

import "sort"

// Modbus protocol limits for a single request.
const (
	maxRegisterQuantity uint16 = 125
	maxBitQuantity      uint16 = 2000
)

type pollPoint struct {
	subject string
	point   Point
}

type pollGroup struct {
	register Register
	address  uint16
	quantity uint16
	points   []pollPoint
}

// groupPoints packs points on one connection into as few Modbus requests as
// possible. Points are partitioned by register table, sorted by address, and
// merged while the span stays within the protocol quantity limit. That is the
// FUXA default (non-fragmented) strategy and matches Rapid SCADA's advice to
// keep element groups large.
func groupPoints(points []pollPoint) []pollGroup {
	if len(points) == 0 {
		return nil
	}
	byRegister := make(map[Register][]pollPoint)
	for _, point := range points {
		byRegister[point.point.Register] = append(
			byRegister[point.point.Register],
			point,
		)
	}
	registers := make([]Register, 0, len(byRegister))
	for register := range byRegister {
		registers = append(registers, register)
	}
	sort.Slice(registers, func(i, j int) bool {
		return registers[i] < registers[j]
	})
	groups := make([]pollGroup, 0)
	for _, register := range registers {
		groups = append(groups, mergeRegisterPoints(register, byRegister[register])...)
	}
	return groups
}

func mergeRegisterPoints(register Register, points []pollPoint) []pollGroup {
	sort.Slice(points, func(i, j int) bool {
		if points[i].point.Address != points[j].point.Address {
			return points[i].point.Address < points[j].point.Address
		}
		if endI, endJ := pointEnd(points[i].point), pointEnd(points[j].point); endI != endJ {
			return endI < endJ
		}
		return points[i].subject < points[j].subject
	})
	limit := uint32(maxQuantity(register))
	groups := make([]pollGroup, 0)
	var current pollGroup
	for _, point := range points {
		start := uint32(point.point.Address)
		end := pointEnd(point.point)
		if len(current.points) == 0 {
			current = pollGroup{
				register: register,
				address:  point.point.Address,
				quantity: uint16(end - start),
				points:   []pollPoint{point},
			}
			continue
		}
		currentEnd := uint32(current.address) + uint32(current.quantity)
		nextEnd := end
		if currentEnd > nextEnd {
			nextEnd = currentEnd
		}
		span := nextEnd - uint32(current.address)
		if span <= limit {
			current.quantity = uint16(span)
			current.points = append(current.points, point)
			continue
		}
		groups = append(groups, current)
		current = pollGroup{
			register: register,
			address:  point.point.Address,
			quantity: uint16(end - start),
			points:   []pollPoint{point},
		}
	}
	if len(current.points) > 0 {
		groups = append(groups, current)
	}
	return groups
}

func pointEnd(point Point) uint32 {
	return uint32(point.Address) + uint32(registerCount(point.Encoding))
}

func maxQuantity(register Register) uint16 {
	switch register {
	case RegisterCoil, RegisterDiscreteInput:
		return maxBitQuantity
	default:
		return maxRegisterQuantity
	}
}
