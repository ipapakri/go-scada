package designer

import (
	"fmt"
	"net/http"

	"go-scada/alert"
)

type AlertConfigRecord struct {
	Subject string `json:"subject"`
	alert.Config
	InputSubject  string `json:"input_subject"`
	OutputSubject string `json:"output_subject"`
}

func (server *Server) listAlertConfigs(writer http.ResponseWriter, _ *http.Request) {
	subjects, err := server.store.ListSubjects(".alert_config")
	if err != nil {
		writeStoreError(writer, err)
		return
	}
	items := make([]AlertConfigRecord, 0, len(subjects))
	for _, subject := range subjects {
		item, err := server.loadAlertConfig(subject)
		if err != nil {
			writeStoreError(writer, fmt.Errorf("load %s: %w", subject, err))
			return
		}
		items = append(items, item)
	}
	writeJSON(writer, http.StatusOK, items)
}

func (server *Server) getAlertConfig(writer http.ResponseWriter, request *http.Request) {
	item, err := server.loadAlertConfig(request.PathValue("subject"))
	if err != nil {
		writeStoreError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, item)
}

func (server *Server) createAlertConfig(writer http.ResponseWriter, request *http.Request) {
	var item AlertConfigRecord
	if !decodeRequest(writer, request, &item) {
		return
	}
	server.saveAlertConfig(writer, item, http.StatusCreated)
}

func (server *Server) updateAlertConfig(writer http.ResponseWriter, request *http.Request) {
	var item AlertConfigRecord
	if !decodeRequest(writer, request, &item) {
		return
	}
	subject := request.PathValue("subject")
	if item.Subject != "" && item.Subject != subject {
		writeValidationError(writer, "subject", "body subject must match the URL")
		return
	}
	item.Subject = subject
	server.saveAlertConfig(writer, item, http.StatusOK)
}

func (server *Server) disableAlertConfig(writer http.ResponseWriter, request *http.Request) {
	item, err := server.loadAlertConfig(request.PathValue("subject"))
	if err != nil {
		writeStoreError(writer, err)
		return
	}
	item.Enabled = false
	server.saveAlertConfig(writer, item, http.StatusOK)
}

func (server *Server) saveAlertConfig(
	writer http.ResponseWriter,
	item AlertConfigRecord,
	status int,
) {
	if item.Version == 0 {
		item.Version = alert.CurrentVersion
	}
	if err := item.Config.ValidateForSubject(item.Subject); err != nil {
		writeValidationError(writer, "config", err.Error())
		return
	}
	raw, err := alert.MarshalConfig(item.Config)
	if err != nil {
		writeValidationError(writer, "config", err.Error())
		return
	}
	input, _ := alert.InputSubject(item.Subject)
	output, _ := alert.OutputSubject(item.Subject)
	item.InputSubject = input
	item.OutputSubject = output
	if err := server.store.Set(item.Subject, raw); err != nil {
		writeStoreError(writer, err)
		return
	}
	writeJSON(writer, status, item)
}

func (server *Server) loadAlertConfig(subject string) (AlertConfigRecord, error) {
	input, err := alert.InputSubject(subject)
	if err != nil {
		return AlertConfigRecord{}, err
	}
	output, err := alert.OutputSubject(subject)
	if err != nil {
		return AlertConfigRecord{}, err
	}
	raw, err := server.store.Get(subject)
	if err != nil {
		return AlertConfigRecord{}, err
	}
	config, err := alert.ParseConfig(raw)
	if err != nil {
		return AlertConfigRecord{}, err
	}
	if err := config.ValidateForSubject(subject); err != nil {
		return AlertConfigRecord{}, err
	}
	return AlertConfigRecord{
		Subject: subject, Config: config,
		InputSubject: input, OutputSubject: output,
	}, nil
}
