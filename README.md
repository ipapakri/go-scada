# go-scada

Go SCADA services backed by a NATS JetStream latest-value stream. The Modbus
service discovers connection definitions on `*.config`, point definitions on
`*.address`, and publishes each point's live value to the address subject
without the `.address` suffix.

## Modbus web designer

The designer provides validated forms for Modbus TCP connections and addresses,
enable/disable controls, and live point values. The browser talks only to the Go
gateway; NATS is never exposed to frontend code.

### Run locally

Requirements: Go, Node.js/npm, Docker, and Docker Compose.

```sh
docker compose up -d nats
go run ./bootstrap
go run ./modbus-service
```

In two more terminals, start the API and Vite development server:

```sh
make designer-api
make designer-ui
```

Open `http://localhost:5173`. The Vite server proxies `/api` and the live
WebSocket to the API at `http://127.0.0.1:8080`.

The default stream settings are in `config.yaml`. To use another file:

```sh
go run ./designer-service -config /path/to/config.yaml
```

### Run the web stack

```sh
docker compose up --build
```

This starts NATS and serves the compiled designer at `http://localhost:8080`.

### Run with the Node-RED Modbus simulator

The simulation profile adds stream bootstrap, three Node-RED Modbus TCP
servers, automatic descriptor seeding, the Modbus poller, and the alert service:

```sh
make simulator-up
```

Open the process dashboard at `http://localhost:1880/dashboard/plant` and the
SCADA designer at `http://localhost:8080`. The designer **Plant** tab shows live
tank, pump, valve, and utility values plus alarms. The browser talks only to the
Go gateway WebSocket; NATS is never exposed to frontend code. The simulated
tank, pumps, valves, and analog sensors are available on host ports 1502 through
1504. See `simulator/README.md` for the register map, controls, fault scenarios,
and verification commands.

### Configuration behavior

- Connection subjects end in `.config`, for example
  `Modbus.Line1.config`.
- Address subjects end in `.address`, for example
  `line1.temperature.address`.
- The address above publishes telemetry on `line1.temperature`.
- Deleting or renaming in the designer is a soft delete: the previous
  descriptor is republished with `enabled: false`. This guarantees that the
  running Modbus service observes the change.
- Modbus configuration is TCP/read-only. Unit ID, timeout, byte/word order, and
  polling interval belong to a connection; register, offset, and encoding
  belong to an address.

## Alert service

Run with `make alert-service`; build `bin/alert-service` with
`make build-alert-service`. The Docker runtime target is `alert-service`.
Example JSON files in `alert-service/` are named after the subjects to which
their values should be published.

### Subject protocol

- `AlertProperties.<name>` stores reusable presentation and acknowledgement
  policy. `<name>` must be one non-empty NATS token.
- `<name>.alert_config` stores a definition; the service publishes its
  canonical state on `<name>.alert`.
- Binary and value input subjects are derived by removing `.alert_config`.
  For example, `tank.temperature.alert_config` monitors `tank.temperature`
  and publishes state to `tank.temperature.alert`. Inputs must exactly match
  the declared `bool`, `int64`, or `float64` stream type; the service performs
  no numeric conversion.
- Summary definitions list `.alert` subjects. They may be nested but cannot
  contain cycles or themselves.
- JSON is strict: unknown fields, unsupported versions, trailing JSON values,
  or configs with anything other than exactly one matching variant are
  rejected.

Version 1 alert properties have this exact schema:

```json
{
  "version": 1,
  "color": "non-empty string",
  "abbreviation": "non-empty string",
  "short_sign": "non-empty string",
  "priority": 0,
  "requires_acknowledgement": false
}
```

`priority` is a non-negative integer. A valid example is
`alert-service/AlertProperties.Alarm.json`, published as
`AlertProperties.Alarm`. Good states do not reference alert properties.

Every config contains `version`, `enabled`, `type`, and exactly the object
selected by `type`. The exact binary schema/example is:

```json
{
  "version": 1,
  "enabled": true,
  "type": "binary",
  "binary": {
    "bad_value": true,
    "true": {
      "property": "AlertProperties.Alarm",
      "text": "Tank level is high"
    },
    "false": {
      "text": "Tank level is normal"
    }
  }
}
```

The alert is active when the derived boolean input equals `bad_value`. The bad
mapping must define `property`; the good mapping must omit it. Each mapping is
selected by the corresponding input value. This is
`alert-service/tank.level_high.alert_config.json`.

The exact value schema/example is:

```json
{
  "version": 1,
  "enabled": true,
  "type": "value",
  "value": {
    "value_type": "float64",
    "intervals": [
      {
        "min": null,
        "max": 80,
        "active": false,
        "text": "Tank temperature is normal"
      },
      {
        "min": 80,
        "max": null,
        "active": true,
        "property": "AlertProperties.Alarm",
        "text": "Tank temperature is high"
      }
    ]
  }
}
```

`value_type` is exactly `int64` or `float64`. Intervals are ordered, contiguous,
and exhaustive: the first `min` and last `max` are `null`, and adjacent bounds
are identical. Boundaries are lower-inclusive and upper-exclusive,
`[min,max)`, so exactly `80` belongs to the second interval. `int64` bounds are
integer literals and `float64` bounds are finite. This is
`alert-service/tank.temperature.alert_config.json`; because of its subject, it
monitors `tank.temperature`. Active intervals must define `property`, while
good intervals (`active: false`) must omit it.

The exact summary schema/example is:

```json
{
  "version": 1,
  "enabled": true,
  "type": "summary",
  "summary": {
    "members": [
      "tank.level_high.alert",
      "tank.temperature.alert"
    ]
  }
}
```

This is `alert-service/tank.alert_config.json`. A summary is active if any
member is active and pending if any member is pending. The dominant state is
the active member with the highest numeric priority, or, when none is active,
the pending member with the highest numeric priority. Equal priorities retain
configured member order. The dominant member supplies presentation and text.

### Runtime state and acknowledgement

The exact version 1 `.alert` state schema is:

```json
{
  "version": 1,
  "active": true,
  "pending": true,
  "property": "AlertProperties.Alarm",
  "color": "#ef4444",
  "abbreviation": "ALM",
  "short_sign": "A",
  "priority": 100,
  "requires_acknowledgement": true,
  "text": "Tank temperature is high",
  "acknowledged": false,
  "came_time": "2026-08-13T10:00:00Z",
  "episode_id": "episode-identity"
}
```

Presentation fields (`property`, `color`, `abbreviation`, `short_sign`,
`priority`, and `requires_acknowledgement`) are empty for good states.
`came_time`, `went_time`, `ack_time`, `dominant`, `members`, and `episode_id`
are omitted when not applicable; timestamps are UTC. Lifecycle timestamps are
latched per episode: `came_time` is set when the alert becomes active,
`went_time` when it clears, and `ack_time` when acknowledged. They are retained
as state changes and replaced only by the relevant lifecycle transition.

To acknowledge, read the latest state from that same `.alert` subject, retain
its `episode_id`, flip only `acknowledged` to `true`, and publish the complete
JSON back to the same `.alert` subject. The service accepts the request only
for a pending matching episode and ignores all other modified fields,
republishing its canonical state. Acknowledging a summary acknowledges its
active or pending member tree.

The stream stores latest state only: the alert service provides no event
history. The alert service also has no designer UI; configure it by publishing
JSON to the subjects above.

## API

- `GET|POST /api/connections`
- `GET|PUT|DELETE /api/connections/{subject}`
- `GET|POST /api/addresses`
- `GET|PUT|DELETE /api/addresses/{subject}`
- `GET /api/live` (WebSocket)
- `GET /api/health`

The WebSocket accepts
`{"action":"subscribe","subject":"line1.temperature.address"}` and
`{"action":"unsubscribe","subject":"line1.temperature.address"}`.

## Verify

```sh
make test
make build-designer
```
