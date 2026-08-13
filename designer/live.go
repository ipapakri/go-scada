package designer

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"go-scada/alert"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type liveRequest struct {
	Action  string `json:"action"`
	Subject string `json:"subject"`
}

type liveEvent struct {
	Type             string `json:"type"`
	Subject          string `json:"subject,omitempty"`
	TelemetrySubject string `json:"telemetry_subject,omitempty"`
	Value            any    `json:"value,omitempty"`
	Timestamp        string `json:"timestamp,omitempty"`
	Message          string `json:"message,omitempty"`
}

func (server *Server) live(writer http.ResponseWriter, request *http.Request) {
	connection, err := websocket.Accept(writer, request, &websocket.AcceptOptions{
		OriginPatterns: []string{"localhost:*", "127.0.0.1:*"},
	})
	if err != nil {
		server.logger.Printf("accept live websocket: %v", err)
		return
	}
	defer connection.Close(websocket.StatusNormalClosure, "")

	var writeMu sync.Mutex
	send := func(event liveEvent) {
		writeMu.Lock()
		defer writeMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := wsjson.Write(ctx, connection, event); err != nil {
			server.logger.Printf("write live websocket: %v", err)
		}
	}

	subscriptions := make(map[string]Subscription)
	defer func() {
		for _, subscription := range subscriptions {
			subscription.Stop()
		}
	}()

	for {
		var command liveRequest
		if err := wsjson.Read(request.Context(), connection, &command); err != nil {
			if websocket.CloseStatus(err) != websocket.StatusNormalClosure &&
				websocket.CloseStatus(err) != websocket.StatusGoingAway {
				server.logger.Printf("read live websocket: %v", err)
			}
			return
		}
		command.Subject = strings.TrimSpace(command.Subject)
		isAlert := strings.HasSuffix(command.Subject, ".alert")
		suffix := ".address"
		if isAlert {
			suffix = ".alert"
		}
		if err := validateSubject(command.Subject, suffix); err != nil {
			send(liveEvent{Type: "error", Subject: command.Subject, Message: err.Error()})
			continue
		}
		switch command.Action {
		case "subscribe":
			if previous := subscriptions[command.Subject]; previous != nil {
				previous.Stop()
				delete(subscriptions, command.Subject)
			}
			if isAlert {
				subject := command.Subject
				subscription, err := server.store.SubscribeString(subject, func(value string) {
					state, err := alert.ParseState(value)
					if err != nil {
						send(liveEvent{
							Type: "error", Subject: subject,
							Message: fmt.Sprintf("parse alert state: %v", err),
						})
						return
					}
					send(liveEvent{
						Type: "alert", Subject: subject, Value: state,
						Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
					})
				})
				if err != nil {
					send(liveEvent{Type: "error", Subject: subject, Message: err.Error()})
					continue
				}
				subscriptions[subject] = subscription
				continue
			}
			item, err := server.loadAddress(command.Subject)
			if err != nil {
				send(liveEvent{
					Type: "error", Subject: command.Subject,
					Message: fmt.Sprintf("load address: %v", err),
				})
				continue
			}
			subject := command.Subject
			telemetrySubject := item.TelemetrySubject
			subscription, err := server.store.Subscribe(
				telemetrySubject,
				item.ValueType,
				func(value any) {
					send(liveEvent{
						Type: "value", Subject: subject,
						TelemetrySubject: telemetrySubject, Value: value,
						Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
					})
				},
			)
			if err != nil {
				send(liveEvent{Type: "error", Subject: subject, Message: err.Error()})
				continue
			}
			subscriptions[subject] = subscription
		case "unsubscribe":
			if subscription := subscriptions[command.Subject]; subscription != nil {
				subscription.Stop()
				delete(subscriptions, command.Subject)
			}
		default:
			send(liveEvent{
				Type: "error", Subject: command.Subject,
				Message: "action must be subscribe or unsubscribe",
			})
		}
	}
}
