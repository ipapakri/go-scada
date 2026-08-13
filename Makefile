.PHONY: proto designer-api designer-ui alert-service test build-designer build-alert-service build-scada

# Generate Go code from Protocol Buffer definitions using buf.gen.yaml.
proto:
	buf generate

designer-api:
	go run ./designer-service

designer-ui:
	npm --prefix designer-ui run dev

alert-service:
	go run ./alert-service

test:
	go test ./address ./alert ./modbus ./stream ./designer ./designer-service
	npm --prefix designer-ui test
	npm --prefix designer-ui run typecheck
	npm --prefix designer-ui run lint

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
