package designer

import (
	"fmt"
	"net/http"

	"go-scada/alert"
)

type AlertPropertiesRecord struct {
	Subject string `json:"subject"`
	alert.Properties
	ReferenceCount int `json:"reference_count,omitempty"`
}

func (server *Server) listAlertProperties(writer http.ResponseWriter, _ *http.Request) {
	subjects, err := server.store.ListSubjectsPrefix("AlertProperties.")
	if err != nil {
		writeStoreError(writer, err)
		return
	}
	references, err := server.alertPropertyReferenceCounts()
	if err != nil {
		writeStoreError(writer, err)
		return
	}
	items := make([]AlertPropertiesRecord, 0, len(subjects))
	for _, subject := range subjects {
		item, err := server.loadAlertProperties(subject)
		if err != nil {
			writeStoreError(writer, fmt.Errorf("load %s: %w", subject, err))
			return
		}
		item.ReferenceCount = references[subject]
		items = append(items, item)
	}
	writeJSON(writer, http.StatusOK, items)
}

func (server *Server) getAlertProperties(writer http.ResponseWriter, request *http.Request) {
	item, err := server.loadAlertProperties(request.PathValue("subject"))
	if err != nil {
		writeStoreError(writer, err)
		return
	}
	references, err := server.alertPropertyReferenceCounts()
	if err != nil {
		writeStoreError(writer, err)
		return
	}
	item.ReferenceCount = references[item.Subject]
	writeJSON(writer, http.StatusOK, item)
}

func (server *Server) createAlertProperties(writer http.ResponseWriter, request *http.Request) {
	var item AlertPropertiesRecord
	if !decodeRequest(writer, request, &item) {
		return
	}
	server.saveAlertProperties(writer, item, http.StatusCreated)
}

func (server *Server) updateAlertProperties(writer http.ResponseWriter, request *http.Request) {
	var item AlertPropertiesRecord
	if !decodeRequest(writer, request, &item) {
		return
	}
	subject := request.PathValue("subject")
	if item.Subject != "" && item.Subject != subject {
		writeValidationError(writer, "subject", "body subject must match the URL")
		return
	}
	item.Subject = subject
	server.saveAlertProperties(writer, item, http.StatusOK)
}

func (server *Server) saveAlertProperties(
	writer http.ResponseWriter,
	item AlertPropertiesRecord,
	status int,
) {
	if err := alert.ValidatePropertiesSubject(item.Subject); err != nil {
		writeValidationError(writer, "subject", err.Error())
		return
	}
	if item.Version == 0 {
		item.Version = alert.CurrentVersion
	}
	raw, err := alert.MarshalProperties(item.Properties)
	if err != nil {
		writeValidationError(writer, "properties", err.Error())
		return
	}
	if err := server.store.Set(item.Subject, raw); err != nil {
		writeStoreError(writer, err)
		return
	}
	writeJSON(writer, status, item)
}

func (server *Server) loadAlertProperties(subject string) (AlertPropertiesRecord, error) {
	if err := alert.ValidatePropertiesSubject(subject); err != nil {
		return AlertPropertiesRecord{}, err
	}
	raw, err := server.store.Get(subject)
	if err != nil {
		return AlertPropertiesRecord{}, err
	}
	properties, err := alert.ParseProperties(raw)
	if err != nil {
		return AlertPropertiesRecord{}, err
	}
	return AlertPropertiesRecord{Subject: subject, Properties: properties}, nil
}

func (server *Server) alertPropertyReferenceCounts() (map[string]int, error) {
	subjects, err := server.store.ListSubjects(".alert_config")
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int)
	for _, subject := range subjects {
		raw, err := server.store.Get(subject)
		if err != nil {
			return nil, err
		}
		config, err := alert.ParseConfig(raw)
		if err != nil {
			continue
		}
		if config.Binary != nil {
			for _, mapping := range []alert.Mapping{config.Binary.True, config.Binary.False} {
				if mapping.Property != "" {
					counts[mapping.Property]++
				}
			}
		}
		if config.Value != nil {
			for _, interval := range config.Value.Intervals {
				if interval.Property != "" {
					counts[interval.Property]++
				}
			}
		}
	}
	return counts, nil
}
