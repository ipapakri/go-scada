package designer

import (
	"encoding/json"
	"fmt"
	"net/http"

	"go-scada/alert"
)

// AlertRecord pairs an alert's NATS subject with its canonical runtime state.
type AlertRecord struct {
	Subject string      `json:"subject"`
	State   alert.State `json:"state"`
}

func (server *Server) listAlerts(writer http.ResponseWriter, _ *http.Request) {
	subjects, err := server.store.ListSubjects(".alert")
	if err != nil {
		writeStoreError(writer, err)
		return
	}

	items := make([]AlertRecord, 0, len(subjects))
	for _, subject := range subjects {
		item, err := server.loadAlert(subject)
		if err != nil {
			writeStoreError(writer, fmt.Errorf("load %s: %w", subject, err))
			return
		}
		items = append(items, item)
	}
	writeJSON(writer, http.StatusOK, items)
}

func (server *Server) acknowledgeAlert(writer http.ResponseWriter, request *http.Request) {
	subject := request.PathValue("subject")
	if err := validateSubject(subject, ".alert"); err != nil {
		writeValidationError(writer, "subject", err.Error())
		return
	}

	item, err := server.loadAlert(subject)
	if err != nil {
		writeStoreError(writer, err)
		return
	}
	if !item.State.Pending {
		writeValidationError(writer, "alert", "only pending alerts can be acknowledged")
		return
	}

	item.State.Acknowledged = true
	data, err := json.Marshal(item.State)
	if err != nil {
		writeValidationError(writer, "alert", err.Error())
		return
	}
	if err := server.store.Set(subject, string(data)); err != nil {
		writeStoreError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, item)
}

func (server *Server) loadAlert(subject string) (AlertRecord, error) {
	if err := validateSubject(subject, ".alert"); err != nil {
		return AlertRecord{}, err
	}
	raw, err := server.store.Get(subject)
	if err != nil {
		return AlertRecord{}, err
	}
	state, err := alert.ParseState(raw)
	if err != nil {
		return AlertRecord{}, err
	}
	return AlertRecord{Subject: subject, State: state}, nil
}
