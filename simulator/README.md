# Node-RED Modbus simulator

The simulator runs N identical, independent process plants and exposes them
through three Modbus TCP servers. Each plant has its own tank, pump 1, pump 2,
inlet/outlet valves, cooling valve, and faults. The default is 10 plants.
Override with `SIMULATOR_INSTANCES`. The normal `modbus-service` polls those
servers, publishes telemetry to NATS, and feeds the designer and alert service.

## Start and stop

From the repository root:

```sh
make simulator-up
```

Open:

- Node-RED editor: `http://localhost:1880`
- Simulator dashboard: `http://localhost:1880/dashboard/plant`
- SCADA designer plant view: `http://localhost:8080`
- NATS monitoring: `http://localhost:8222`

If port 1880 is already in use, choose another host port:

```sh
SIMULATOR_DASHBOARD_PORT=1881 make simulator-up
```

Run more independent plants (rebuild is not required, but the container must
restart to pick up the env var):

```sh
SIMULATOR_INSTANCES=25 make simulator-up
```

Stop the stack with:

```sh
make simulator-down
```

The `simulation` Compose profile starts NATS, the retain service, descriptor
seeding, Node-RED, the Modbus poller, the alert service, and the designer.
Descriptor seeding is idempotent and runs each time the profile starts. By
default it stamps 10 plant copies (`plant.001` … `plant.010`) that reuse the
original three Modbus connections and map each copy onto that plant's register
block (`plant.001` is plant 1, `plant.010` is plant 10). Set
`SIMULATOR_REPLICAS` to match `SIMULATOR_INSTANCES` when you change the count.
`SIMULATOR_REPLICAS=0 make simulator-up` seeds only the operator plant.

## Simulated controllers

All addresses below are zero-based. Analog values are IEEE-754 float32 values
stored in two consecutive big-endian input registers. Boolean status values use
discrete inputs or coils. Plant *n* is 1-based.

### Tank PLC

Endpoint `localhost:1502`, configured as unit ID 1. Each plant occupies a
20-register analog block, three discrete inputs, and two coils:

- Input `20*(n-1)` + 0-1: tank level, percent
- Input `20*(n-1)` + 4-5: tank temperature, degrees Celsius
- Input `20*(n-1)` + 8-9: hydrostatic pressure, bar
- Input `20*(n-1)` + 12-13: inlet valve position, percent
- Input `20*(n-1)` + 16-17: outlet valve position, percent
- Discrete `3*(n-1)` + 0: high-level switch
- Discrete `3*(n-1)` + 1: low-level switch
- Discrete `3*(n-1)` + 2: bad-sensor status
- Coil `2*(n-1)` + 0: inlet valve open
- Coil `2*(n-1)` + 1: outlet valve open

Plant 1 keeps the original map (inputs 0-17, discrete 0-2, coils 0-1).

### Pump PLC

Endpoint `localhost:1503`, configured as unit ID 2. Each plant occupies a
28-register analog block and four discrete inputs:

- Input `28*(n-1)` + 0-1: pump 1 speed, percent
- Input `28*(n-1)` + 4-5: pump 1 current, ampere
- Input `28*(n-1)` + 8-9: discharge pressure, bar
- Input `28*(n-1)` + 12-13: pump 2 speed, percent
- Input `28*(n-1)` + 16-17: pump 2 current, ampere
- Input `28*(n-1)` + 20-21: total process flow, cubic metres per hour
- Input `28*(n-1)` + 24-25: vibration, millimetres per second
- Discrete `4*(n-1)` + 0/1: pump 1 running/trip
- Discrete `4*(n-1)` + 2/3: pump 2 running/trip

### Utility PLC

Endpoint `localhost:1504`, configured as unit ID 3. Each plant occupies a
16-register analog block, two discrete inputs, and one coil:

- Input `16*(n-1)` + 0-1: process flow, cubic metres per hour
- Input `16*(n-1)` + 4-5: ambient temperature, degrees Celsius
- Input `16*(n-1)` + 8-9: conductivity, microsiemens per centimetre
- Input `16*(n-1)` + 12-13: cooling valve position, percent
- Discrete `2*(n-1)` + 0: high-temperature switch
- Discrete `2*(n-1)` + 1: low-flow switch
- Coil `n-1`: cooling valve open

The complete Modbus-to-subject mapping is in
`simulator/config/descriptors.json`. The operator plant (`plant.tank.*`) and
`plant.001.*` both follow plant 1's register block. `plant.002.*` through
`plant.NNN.*` are offset by that plant's analog, discrete, and coil strides.

## Process behavior and controls

Each plant is an independent copy of the original model. Automatic mode
regulates that plant's tank around 60 percent by adjusting its inlet and outlet
valves and staging its two pumps. Pump speed affects that plant's flow,
pressure, current, temperature, and vibration. Analog values contain small
deterministic noise so trends look realistic while tests remain repeatable.
Plants share the same starting values but use independent noise seeds, so they
diverge instead of staying lockstep.

The dashboard selects a plant. Controls, valve commands, and faults — including
pump trips — apply only to that plant. Simulation speed is global. Available
faults:

- pump 1 or pump 2 trips
- stuck inlet or outlet valves
- a high-temperature heat source
- frozen analog measurements
- bad tank sensor values

The high-level and high-temperature conditions are seeded as SCADA alert
definitions. Resetting faults does not reset process values; the automatic
controller returns that plant to its normal operating point.

## Verify telemetry

After the stack has been running for a few seconds:

```sh
go run ./scada-cli get -type float64 plant.tank.level
go run ./scada-cli get -type bool plant.pump1.running
go run ./scada-cli get -type float64 plant.utility.conductivity
go run ./scada-cli get plant.tank.temperature.alert
go run ./scada-cli get -type float64 plant.001.tank.level
go run ./scada-cli get plant.010.tank.alert
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

The Node tests cover independent plants, configurable instance count, process
bounds, fault isolation, register encoding, and register-map overlap. The Go
tests validate every seeded connection and address with the same parsers used
by the running Modbus service.
