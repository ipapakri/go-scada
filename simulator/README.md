# Node-RED Modbus simulator

The simulator models a small process plant and exposes its values through three
independent Modbus TCP servers. The normal `modbus-service` polls those servers,
publishes telemetry to NATS JetStream, and feeds the designer and alert service.

## Start and stop

From the repository root:

```sh
make simulator-up
```

Open:

- Node-RED editor: `http://localhost:1880`
- Simulator dashboard: `http://localhost:1880/dashboard/plant`
- SCADA designer: `http://localhost:8080`
- NATS monitoring: `http://localhost:8222`

If port 1880 is already in use, choose another host port:

```sh
SIMULATOR_DASHBOARD_PORT=1881 make simulator-up
```

Stop the stack with:

```sh
make simulator-down
```

The `simulation` Compose profile starts NATS, stream bootstrap, descriptor
seeding, Node-RED, the Modbus poller, the alert service, and the designer.
Descriptor seeding is idempotent and runs each time the profile starts.

## Simulated controllers

All addresses below are zero-based. Analog values are IEEE-754 float32 values
stored in two consecutive big-endian input registers. Boolean status values use
discrete inputs or coils.

### Tank PLC

Endpoint `localhost:1502`, configured as unit ID 1:

- Input 0-1: tank level, percent
- Input 4-5: tank temperature, degrees Celsius
- Input 8-9: hydrostatic pressure, bar
- Input 12-13: inlet valve position, percent
- Input 16-17: outlet valve position, percent
- Discrete 0: high-level switch
- Discrete 1: low-level switch
- Discrete 2: bad-sensor status
- Coil 0: inlet valve open
- Coil 1: outlet valve open

### Pump PLC

Endpoint `localhost:1503`, configured as unit ID 2:

- Input 0-1: pump 1 speed, percent
- Input 4-5: pump 1 current, ampere
- Input 8-9: common discharge pressure, bar
- Input 12-13: pump 2 speed, percent
- Input 16-17: pump 2 current, ampere
- Input 20-21: total process flow, cubic metres per hour
- Input 24-25: vibration, millimetres per second
- Discrete 0/1: pump 1 running/trip
- Discrete 2/3: pump 2 running/trip

### Utility PLC

Endpoint `localhost:1504`, configured as unit ID 3:

- Input 0-1: process flow, cubic metres per hour
- Input 4-5: ambient temperature, degrees Celsius
- Input 8-9: conductivity, microsiemens per centimetre
- Input 12-13: cooling valve position, percent
- Discrete 0: high-temperature switch
- Discrete 1: low-flow switch
- Coil 0: cooling valve open

The complete Modbus-to-subject mapping is in
`simulator/config/descriptors.json`.

## Process behavior and controls

Automatic mode regulates the tank around 60 percent by adjusting inlet and
outlet valves and staging two pumps. Pump speed affects flow, pressure, current,
temperature, and vibration. Analog values contain small deterministic noise so
trends look realistic while tests remain repeatable.

The dashboard can switch to manual mode, change valve and pump commands, alter
simulation speed, and inject:

- pump 1 or pump 2 trips
- stuck inlet or outlet valves
- a high-temperature heat source
- frozen analog measurements
- bad tank sensor values

The high-level and high-temperature conditions are seeded as SCADA alert
definitions. Resetting faults does not reset process values; the automatic
controller returns the plant to its normal operating point.

## Verify telemetry

After the stack has been running for a few seconds:

```sh
go run ./scada-cli get -type float64 plant.tank.level
go run ./scada-cli get -type bool plant.pump1.running
go run ./scada-cli get -type float64 plant.utility.conductivity
go run ./scada-cli get plant.tank.temperature.alert
```

To inspect service status and logs:

```sh
docker compose --profile simulation ps
docker compose --profile simulation logs simulator modbus-service alert-service
```

Any Modbus client can also read the three published host ports directly. Use
function code 4 for input registers, code 2 for discrete inputs, and code 1 for
coils.

## Development checks

```sh
npm --prefix simulator/node-red test
go test ./simulator ./modbus
docker compose --profile simulation config --quiet
```

The Node tests cover process bounds, fault behavior, register encoding, and
register-map overlap. The Go tests validate every seeded connection and address
with the same parsers used by the running Modbus service.
