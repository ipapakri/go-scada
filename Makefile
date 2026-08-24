.PHONY: proto designer-api designer-ui retain-service alert-service simulator-up simulator-down test build-designer build-alert-service build-scada

# Generate Go code from Protocol Buffer definitions using buf.gen.yaml.
proto:
	buf generate

designer-api:
	go run ./designer-service

designer-ui:
	npm --prefix designer-ui run dev

retain-service:
	go run ./retain-service

alert-service:
	go run ./alert-service

simulator-up:
	docker compose --profile simulation up --build -d

simulator-down:
	docker compose --profile simulation down

test:
	go test ./address ./alert ./modbus ./stream ./retain ./designer ./designer-service ./simulator
	npm --prefix designer-ui test
	npm --prefix designer-ui run typecheck
	npm --prefix designer-ui run lint
	npm --prefix simulator/node-red test

build-designer:
	npm --prefix designer-ui run build
	mkdir -p bin
	go build -o bin/designer-service ./designer-service

build-alert-service:
	mkdir -p bin
	go build -o bin/alert-service ./alert-service

build-scada:
	mkdir -p bin
	go build -o bin/scada ./scada-cli
