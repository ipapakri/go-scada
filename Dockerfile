FROM node:alpine AS ui-build
WORKDIR /src/designer-ui
COPY designer-ui/package*.json ./
RUN npm ci
COPY designer-ui/ ./
RUN npm run build

FROM golang:alpine AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/designer-service ./designer-service && \
    CGO_ENABLED=0 go build -o /out/bootstrap ./bootstrap && \
    CGO_ENABLED=0 go build -o /out/modbus-service ./modbus-service && \
    CGO_ENABLED=0 go build -o /out/alert-service ./alert-service

FROM alpine:latest AS designer
WORKDIR /app
COPY --from=go-build /out/designer-service /app/designer-service
COPY --from=ui-build /src/designer-ui/dist /app/ui
EXPOSE 8080
ENTRYPOINT ["/app/designer-service", "-listen", "0.0.0.0:8080", "-static", "/app/ui"]

FROM alpine:latest AS bootstrap
WORKDIR /app
COPY --from=go-build /out/bootstrap /app/bootstrap
ENTRYPOINT ["/app/bootstrap"]

FROM alpine:latest AS modbus-service
WORKDIR /app
COPY --from=go-build /out/modbus-service /app/modbus-service
ENTRYPOINT ["/app/modbus-service"]

FROM alpine:latest AS alert-service
WORKDIR /app
COPY --from=go-build /out/alert-service /app/alert-service
ENTRYPOINT ["/app/alert-service"]
