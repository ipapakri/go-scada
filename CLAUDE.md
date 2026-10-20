# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

@AGENTS.md

## Commands

- `make test` runs the full suite: Go tests for the listed packages, then the designer-ui tests, typecheck and oxlint, then the Node-RED simulator tests. A new Go package with tests must be added to the `test` target, which lists packages explicitly instead of using `./...`.
- Run one Go test with `go test ./alert -run TestName`. Run one UI test with `npm --prefix designer-ui test -- src/path/to/file.test.tsx`.
- `stream/client_test.go` has a NATS integration test that skips unless `NATS_TEST_URL` is set, e.g. `docker compose up -d nats` and then `NATS_TEST_URL=nats://localhost:4222 go test ./stream`.
- `make build-designer` builds the UI and `bin/designer-service`. `make proto` regenerates `gen/go` from `proto/` with buf. Never hand-edit `gen/`.
- `make simulator-up` / `make simulator-down` run the whole stack against the Node-RED Modbus simulator. Docker Compose services are gated by profiles (`base`, `run`, `simulation`, `designer`, `alert-service`), and the Dockerfile has one runtime target per service.

## Architecture

Every service talks only through NATS, using the `stream` package. Nothing calls another service directly.

- **Subjects are system-relative.** `config.yaml` sets `system_name`; `stream` prefixes every subject with `<system_name>.` on the wire and strips it before handing subjects to callers. Code and docs outside `stream` and `retain` use relative subjects.
- **Every value is a `telemetry.v1.Message` protobuf** (`proto/go_scada/telemetry/v1`). Configuration documents (Modbus connections and addresses, alert configs, alert properties, alert state) are JSON carried as the protobuf's `string_value`, so "publishing a config" means `stream.Set[string]`.
- **Live traffic is core NATS, not JetStream.** Latest values come from `retain-service`, which subscribes to `<system>.>`, stores the last payload per subject in SQLite, and answers request-reply on `retain.<system>.get` / `retain.<system>.list`. `stream.Get`, `ListSubjects*`, and the catch-up phase of `Subscribe*` depend on it, so retain-service must be running for anything else to work.
- `stream.Subscribe{Suffix,Prefix}` deliver the retained value first, then live updates, dropping duplicate `message_id`s and values whose `observed_at` is older than the last one delivered. `SubscribeExact` watches a changing set of exact subjects and pins each subject to one worker goroutine. The first `Set` on a subject also announces it on `registry.subject.created`.
- **Subject suffixes are the discovery protocol.** `modbus-service` watches `*.config` (connections) and `*.address` (points) and publishes each point's value on the address subject minus `.address`. `alert-service` watches `*.alert_config` and `AlertProperties.*` and publishes state on `*.alert`; acknowledgement is a publish back to the `.alert` subject. README.md holds the exact JSON schemas and alert semantics, so read it before changing descriptor or alert formats.
- **Deletes are soft.** The designer republishes the old descriptor with `enabled: false`, because retain only keeps latest values and the services have to observe the change.

Layout: each runtime has a thin `main` in `*-service/` (or `scada-cli/`, `simulator/`) that wires config and NATS, and the logic sits in the matching library package (`modbus`, `alert`, `retain`, `designer`, `address`). `designer` is the HTTP/WebSocket gateway for `designer-ui` (React + Vite); the browser never connects to NATS. `alert` reaches the bus through the small `serviceStream` interface in `alert/service.go`, which its tests fake. `app1`, `app2`, and the JSON files in `alert-service/` and `modbus-service/` are hand-run examples, not part of the system.
