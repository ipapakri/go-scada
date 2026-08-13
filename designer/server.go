package designer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"go-scada/address"
	"go-scada/modbus"
)

const maxRequestSize = 1 << 20

type ConnectionRecord struct {
	Subject      string                  `json:"subject"`
	Version      int                     `json:"version"`
	Driver       string                  `json:"driver"`
	Enabled      bool                    `json:"enabled"`
	Config       modbus.ConnectionConfig `json:"config"`
	AddressCount int                     `json:"address_count,omitempty"`
}

type AddressRecord struct {
	Subject          string               `json:"subject"`
	Version          int                  `json:"version"`
	Driver           string               `json:"driver"`
	ValueType        address.ValueType    `json:"value_type"`
	Enabled          bool                 `json:"enabled"`
	Connection       string               `json:"connection"`
	Config           modbus.AddressConfig `json:"config"`
	TelemetrySubject string               `json:"telemetry_subject"`
}

type apiError struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

// Server exposes designer operations over HTTP.
type Server struct {
	store  Store
	logger *log.Logger
	mux    *http.ServeMux
}

func NewServer(store Store, logger *log.Logger) (*Server, error) {
	if store == nil {
		return nil, errors.New("designer store is required")
	}
	if logger == nil {
		logger = log.Default()
	}
	server := &Server{store: store, logger: logger, mux: http.NewServeMux()}
	server.routes()
	return server, nil
}

func (server *Server) Handler() http.Handler {
	return server.recoverPanics(server.mux)
}

func (server *Server) routes() {
	server.mux.HandleFunc("GET /api/health", server.health)
	server.mux.HandleFunc("GET /api/connections", server.listConnections)
	server.mux.HandleFunc("POST /api/connections", server.createConnection)
	server.mux.HandleFunc("GET /api/connections/{subject}", server.getConnection)
	server.mux.HandleFunc("PUT /api/connections/{subject}", server.updateConnection)
	server.mux.HandleFunc("DELETE /api/connections/{subject}", server.disableConnection)
	server.mux.HandleFunc("GET /api/addresses", server.listAddresses)
	server.mux.HandleFunc("POST /api/addresses", server.createAddress)
	server.mux.HandleFunc("GET /api/addresses/{subject}", server.getAddress)
	server.mux.HandleFunc("PUT /api/addresses/{subject}", server.updateAddress)
	server.mux.HandleFunc("DELETE /api/addresses/{subject}", server.disableAddress)
	server.mux.HandleFunc("GET /api/alerts", server.listAlerts)
	server.mux.HandleFunc("POST /api/alerts/{subject}/acknowledge", server.acknowledgeAlert)
	server.mux.HandleFunc("GET /api/alert-properties", server.listAlertProperties)
	server.mux.HandleFunc("POST /api/alert-properties", server.createAlertProperties)
	server.mux.HandleFunc("GET /api/alert-properties/{subject}", server.getAlertProperties)
	server.mux.HandleFunc("PUT /api/alert-properties/{subject}", server.updateAlertProperties)
	server.mux.HandleFunc("GET /api/alert-configs", server.listAlertConfigs)
	server.mux.HandleFunc("POST /api/alert-configs", server.createAlertConfig)
	server.mux.HandleFunc("GET /api/alert-configs/{subject}", server.getAlertConfig)
	server.mux.HandleFunc("PUT /api/alert-configs/{subject}", server.updateAlertConfig)
	server.mux.HandleFunc("DELETE /api/alert-configs/{subject}", server.disableAlertConfig)
	server.mux.HandleFunc("GET /api/live", server.live)
}

func (server *Server) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (server *Server) listConnections(writer http.ResponseWriter, _ *http.Request) {
	subjects, err := server.store.ListSubjects(".config")
	if err != nil {
		writeStoreError(writer, err)
		return
	}
	addressCounts, err := server.connectionAddressCounts()
	if err != nil {
		writeStoreError(writer, err)
		return
	}
	items := make([]ConnectionRecord, 0, len(subjects))
	for _, subject := range subjects {
		item, err := server.loadConnection(subject)
		if err != nil {
			writeStoreError(writer, fmt.Errorf("load %s: %w", subject, err))
			return
		}
		item.AddressCount = addressCounts[subject]
		items = append(items, item)
	}
	writeJSON(writer, http.StatusOK, items)
}

func (server *Server) getConnection(writer http.ResponseWriter, request *http.Request) {
	item, err := server.loadConnection(request.PathValue("subject"))
	if err != nil {
		writeStoreError(writer, err)
		return
	}
	counts, err := server.connectionAddressCounts()
	if err != nil {
		writeStoreError(writer, err)
		return
	}
	item.AddressCount = counts[item.Subject]
	writeJSON(writer, http.StatusOK, item)
}

func (server *Server) createConnection(writer http.ResponseWriter, request *http.Request) {
	var item ConnectionRecord
	if !decodeRequest(writer, request, &item) {
		return
	}
	server.saveConnection(writer, item, http.StatusCreated)
}

func (server *Server) updateConnection(writer http.ResponseWriter, request *http.Request) {
	var item ConnectionRecord
	if !decodeRequest(writer, request, &item) {
		return
	}
	subject := request.PathValue("subject")
	if item.Subject != "" && item.Subject != subject {
		writeValidationError(writer, "subject", "body subject must match the URL")
		return
	}
	item.Subject = subject
	server.saveConnection(writer, item, http.StatusOK)
}

func (server *Server) saveConnection(
	writer http.ResponseWriter,
	item ConnectionRecord,
	status int,
) {
	raw, normalized, err := validateConnection(item)
	if err != nil {
		writeValidationError(writer, "connection", err.Error())
		return
	}
	if err := server.store.Set(normalized.Subject, raw); err != nil {
		writeStoreError(writer, err)
		return
	}
	writeJSON(writer, status, normalized)
}

func (server *Server) disableConnection(writer http.ResponseWriter, request *http.Request) {
	item, err := server.loadConnection(request.PathValue("subject"))
	if err != nil {
		writeStoreError(writer, err)
		return
	}
	item.Enabled = false
	server.saveConnection(writer, item, http.StatusOK)
}

func (server *Server) listAddresses(writer http.ResponseWriter, _ *http.Request) {
	subjects, err := server.store.ListSubjects(".address")
	if err != nil {
		writeStoreError(writer, err)
		return
	}
	items := make([]AddressRecord, 0, len(subjects))
	for _, subject := range subjects {
		item, err := server.loadAddress(subject)
		if err != nil {
			writeStoreError(writer, fmt.Errorf("load %s: %w", subject, err))
			return
		}
		items = append(items, item)
	}
	writeJSON(writer, http.StatusOK, items)
}

func (server *Server) getAddress(writer http.ResponseWriter, request *http.Request) {
	item, err := server.loadAddress(request.PathValue("subject"))
	if err != nil {
		writeStoreError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, item)
}

func (server *Server) createAddress(writer http.ResponseWriter, request *http.Request) {
	var item AddressRecord
	if !decodeRequest(writer, request, &item) {
		return
	}
	server.saveAddress(writer, item, http.StatusCreated)
}

func (server *Server) updateAddress(writer http.ResponseWriter, request *http.Request) {
	var item AddressRecord
	if !decodeRequest(writer, request, &item) {
		return
	}
	subject := request.PathValue("subject")
	if item.Subject != "" && item.Subject != subject {
		writeValidationError(writer, "subject", "body subject must match the URL")
		return
	}
	item.Subject = subject
	server.saveAddress(writer, item, http.StatusOK)
}

func (server *Server) saveAddress(writer http.ResponseWriter, item AddressRecord, status int) {
	raw, normalized, err := validateAddress(item)
	if err != nil {
		writeValidationError(writer, "address", err.Error())
		return
	}
	connectionRaw, err := server.store.Get(normalized.Connection)
	if err != nil {
		writeValidationError(writer, "connection", "referenced connection does not exist")
		return
	}
	connectionDescriptor, err := address.ParseConnection(connectionRaw)
	if err != nil {
		writeValidationError(writer, "connection", err.Error())
		return
	}
	connection, err := modbus.ParseConnection(connectionDescriptor)
	if err != nil {
		writeValidationError(writer, "connection", err.Error())
		return
	}
	descriptor, _ := address.Parse(raw)
	if _, err := modbus.ParsePoint(descriptor, connection); err != nil {
		writeValidationError(writer, "address", err.Error())
		return
	}
	if err := server.store.Set(normalized.Subject, raw); err != nil {
		writeStoreError(writer, err)
		return
	}
	writeJSON(writer, status, normalized)
}

func (server *Server) disableAddress(writer http.ResponseWriter, request *http.Request) {
	item, err := server.loadAddress(request.PathValue("subject"))
	if err != nil {
		writeStoreError(writer, err)
		return
	}
	item.Enabled = false
	server.saveAddress(writer, item, http.StatusOK)
}

func (server *Server) loadConnection(subject string) (ConnectionRecord, error) {
	value, err := server.store.Get(subject)
	if err != nil {
		return ConnectionRecord{}, err
	}
	descriptor, err := address.ParseConnection(value)
	if err != nil {
		return ConnectionRecord{}, err
	}
	if _, err := modbus.ParseConnection(descriptor); err != nil {
		return ConnectionRecord{}, err
	}
	config, err := address.DecodeConnectionConfig[modbus.ConnectionConfig](descriptor)
	if err != nil {
		return ConnectionRecord{}, err
	}
	return ConnectionRecord{
		Subject: subject, Version: descriptor.Version, Driver: descriptor.Driver,
		Enabled: descriptor.Enabled, Config: config,
	}, nil
}

func (server *Server) loadAddress(subject string) (AddressRecord, error) {
	value, err := server.store.Get(subject)
	if err != nil {
		return AddressRecord{}, err
	}
	descriptor, err := address.Parse(value)
	if err != nil {
		return AddressRecord{}, err
	}
	config, err := address.DecodeConfig[modbus.AddressConfig](descriptor)
	if err != nil {
		return AddressRecord{}, err
	}
	if _, err := modbus.ParsePoint(descriptor, modbus.Connection{}); err != nil {
		return AddressRecord{}, err
	}
	return AddressRecord{
		Subject: subject, Version: descriptor.Version, Driver: descriptor.Driver,
		ValueType: descriptor.ValueType, Enabled: descriptor.Enabled,
		Connection: descriptor.Connection, Config: config,
		TelemetrySubject: strings.TrimSuffix(subject, ".address"),
	}, nil
}

func (server *Server) connectionAddressCounts() (map[string]int, error) {
	subjects, err := server.store.ListSubjects(".address")
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int)
	for _, subject := range subjects {
		item, err := server.loadAddress(subject)
		if err != nil {
			continue
		}
		counts[item.Connection]++
	}
	return counts, nil
}

func validateConnection(item ConnectionRecord) (string, ConnectionRecord, error) {
	if err := validateSubject(item.Subject, ".config"); err != nil {
		return "", item, err
	}
	if item.Version == 0 {
		item.Version = address.CurrentVersion
	}
	if item.Driver == "" {
		item.Driver = "modbus"
	}
	config, err := json.Marshal(item.Config)
	if err != nil {
		return "", item, err
	}
	descriptor := address.Connection{
		Version: item.Version, Driver: item.Driver, Enabled: item.Enabled, Config: config,
	}
	raw, err := address.MarshalConnection(descriptor)
	if err != nil {
		return "", item, err
	}
	if _, err := modbus.ParseConnection(descriptor); err != nil {
		return "", item, err
	}
	return raw, item, nil
}

func validateAddress(item AddressRecord) (string, AddressRecord, error) {
	if err := validateSubject(item.Subject, ".address"); err != nil {
		return "", item, err
	}
	if item.Version == 0 {
		item.Version = address.CurrentVersion
	}
	if item.Driver == "" {
		item.Driver = "modbus"
	}
	item.ValueType = valueTypeForEncoding(item.Config.Encoding)
	item.TelemetrySubject = strings.TrimSuffix(item.Subject, ".address")
	config, err := json.Marshal(item.Config)
	if err != nil {
		return "", item, err
	}
	descriptor := address.Descriptor{
		Version: item.Version, Driver: item.Driver, ValueType: item.ValueType,
		Enabled: item.Enabled, Connection: item.Connection, Config: config,
	}
	raw, err := address.Marshal(descriptor)
	if err != nil {
		return "", item, err
	}
	if _, err := modbus.ParsePoint(descriptor, modbus.Connection{}); err != nil {
		return "", item, err
	}
	return raw, item, nil
}

func valueTypeForEncoding(encoding modbus.Encoding) address.ValueType {
	switch encoding {
	case modbus.EncodingBool:
		return address.ValueTypeBool
	case modbus.EncodingInt16, modbus.EncodingUint16,
		modbus.EncodingInt32, modbus.EncodingUint32:
		return address.ValueTypeInt64
	case modbus.EncodingFloat32, modbus.EncodingFloat64:
		return address.ValueTypeFloat64
	default:
		return ""
	}
}

func validateSubject(subject string, suffix string) error {
	if subject == "" || strings.TrimSpace(subject) != subject {
		return errors.New("subject is required and cannot have surrounding whitespace")
	}
	if !strings.HasSuffix(subject, suffix) {
		return fmt.Errorf("subject must end in %s", suffix)
	}
	if strings.HasPrefix(subject, ".") || strings.Contains(subject, "..") ||
		strings.ContainsAny(subject, " \t\r\n*>") {
		return errors.New("subject is not a valid relative NATS subject")
	}
	return nil
}

func decodeRequest(writer http.ResponseWriter, request *http.Request, destination any) bool {
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestSize)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeValidationError(writer, "request", err.Error())
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeValidationError(writer, "request", "request must contain one JSON object")
		return false
	}
	return true
}

func writeValidationError(writer http.ResponseWriter, field string, message string) {
	writeJSON(writer, http.StatusBadRequest, apiError{Error: errorDetail{
		Code: "validation_error", Message: message, Fields: map[string]string{field: message},
	}})
}

func writeStoreError(writer http.ResponseWriter, err error) {
	writeJSON(writer, http.StatusBadGateway, apiError{Error: errorDetail{
		Code: "store_error", Message: err.Error(),
	}})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func (server *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				server.logger.Printf("designer request panic: %v", recovered)
				writeJSON(writer, http.StatusInternalServerError, apiError{Error: errorDetail{
					Code: "internal_error", Message: "internal server error",
				}})
			}
		}()
		start := time.Now()
		next.ServeHTTP(writer, request)
		server.logger.Printf("%s %s (%s)", request.Method, request.URL.Path, time.Since(start))
	})
}
