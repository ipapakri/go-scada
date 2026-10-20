# go-scada

A supervisory control and data acquisition system that reads plant devices, keeps their latest values, and raises alarms on them.

## Plant data

**Site**:
One plant or location running its own go-scada system, named by an identifier that is unique across the company. An overview system can bridge several sites and tell them apart by this name.
_Avoid_: System, system name, plant (as an identifier)

**Tag**:
A named point of plant data, such as a sensor reading or a setpoint, with a fixed type, unit and description.
_Avoid_: Point, address, signal

**Tag path**:
The dotted name that identifies a tag within its site, such as `sensor1.state.value`. Its tokens carry no meaning to the system beyond the reserved `_sys` root, so any hierarchy in it is the user's own convention.
_Avoid_: Facet, subject (when meaning the tag's name)

**Value**:
One timestamped reading of a tag, with its quality.
_Avoid_: Telemetry message, sample

**Quality**:
How far a value can be trusted: good, uncertain or bad. A value that isn't good also carries the reason, such as a lost connection or a failed read or write.
_Avoid_: Status, stale

**Unit**:
The engineering unit of a tag, such as `°C` or `bar`. It belongs to the tag, not to each value.

**Memory tag**:
A tag with no binding, whose values come only from writes by users or services.
_Avoid_: Internal tag, virtual tag

**Source**:
Who wrote a value: the driver instance or UI service, plus the user when a person caused the write.
_Avoid_: Origin, writer, forced value

## Devices

**Driver**:
A protocol implementation that moves values between devices and tags, such as `modbus`.
_Avoid_: Service (when meaning the protocol)

**Connection**:
One configured link from a driver to a device, such as `modbus.line1`. A connection is the unit that gets assigned to an instance.
_Avoid_: Device config

**Instance**:
One running process of a driver, such as `modbus_1`, that polls the connections assigned to it.
_Avoid_: Worker, node

**Binding**:
The static mapping from a connection's device data to one tag, such as a Modbus register, encoding, deadband and scaling.
_Avoid_: Address, address descriptor

**Direction**:
Whether a binding reads device data into its tag (input) or writes the tag's values to the device (output).
_Avoid_: Writable, read-only

**Output tag**:
A tag whose binding has the output direction, such as `motor1.commands.start`. It is an ordinary tag, and its values are kept like any other.
_Avoid_: Command, command tag, write request

## Alarms

**Alarm**:
A monitored condition on a tag, or on other alarms, that an operator may need to notice and acknowledge.
_Avoid_: Alert

**Alarm class**:
A named, reusable set of presentation and acknowledgement rules that an active alarm refers to, such as `Alarm` or `Warning`.
_Avoid_: AlertProperties, property

**Summary alarm**:
An alarm whose state is derived from a list of member alarms instead of from a tag.
_Avoid_: Group alert, summary alert

**Alarm state**:
The current condition of one alarm: active or not, pending acknowledgement or not, and its current episode. Only the alarm service changes it.
_Avoid_: Alert state, alarm value

**Episode**:
One period from the moment an alarm becomes active until it has cleared and, if its class requires it, been acknowledged. If the alarm becomes active again before that, the same episode continues.
