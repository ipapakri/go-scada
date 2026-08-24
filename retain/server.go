package retain

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/nats-io/nats.go"
)

// Server ingests live values and answers Get/List request-reply lookups.
type Server struct {
	store  *Store
	system string
	ingest *nats.Subscription
	get    *nats.Subscription
	list   *nats.Subscription
}

// Listen subscribes to live traffic first, then serves Get/List.
func Listen(connection *nats.Conn, store *Store, systemName string) (*Server, error) {
	if connection == nil {
		return nil, fmt.Errorf("nats connection is required")
	}
	if store == nil {
		return nil, fmt.Errorf("retain store is required")
	}
	systemName = strings.TrimSpace(systemName)
	if systemName == "" {
		return nil, fmt.Errorf("system name is required")
	}

	server := &Server{store: store, system: systemName}
	ingest, err := connection.Subscribe(systemName+".>", server.handleIngest)
	if err != nil {
		return nil, fmt.Errorf("subscribe to %s.>: %w", systemName, err)
	}
	get, err := connection.QueueSubscribe(
		GetSubject(systemName),
		QueueGroup,
		server.handleGet,
	)
	if err != nil {
		_ = ingest.Unsubscribe()
		return nil, fmt.Errorf("subscribe to retain get: %w", err)
	}
	list, err := connection.QueueSubscribe(
		ListSubject(systemName),
		QueueGroup,
		server.handleList,
	)
	if err != nil {
		_ = ingest.Unsubscribe()
		_ = get.Unsubscribe()
		return nil, fmt.Errorf("subscribe to retain list: %w", err)
	}
	if err := connection.Flush(); err != nil {
		_ = ingest.Unsubscribe()
		_ = get.Unsubscribe()
		_ = list.Unsubscribe()
		return nil, fmt.Errorf("flush retain subscriptions: %w", err)
	}
	server.ingest = ingest
	server.get = get
	server.list = list
	return server, nil
}

// Close unsubscribes retain handlers. The store stays open for the caller.
func (server *Server) Close() error {
	if server == nil {
		return nil
	}
	var first error
	for _, subscription := range []*nats.Subscription{
		server.ingest,
		server.get,
		server.list,
	} {
		if subscription == nil {
			continue
		}
		if err := subscription.Unsubscribe(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (server *Server) handleIngest(message *nats.Msg) {
	if message == nil || message.Subject == "" {
		return
	}
	server.store.Put(message.Subject, message.Data)
}

func (server *Server) handleGet(message *nats.Msg) {
	if message == nil || message.Reply == "" {
		return
	}
	payload, ok := server.store.Get(string(message.Data))
	if !ok {
		reply := nats.NewMsg(message.Reply)
		reply.Header.Set(HeaderError, ErrorNotFound)
		_ = message.RespondMsg(reply)
		return
	}
	_ = message.Respond(payload)
}

func (server *Server) handleList(message *nats.Msg) {
	if message == nil || message.Reply == "" {
		return
	}
	var request ListRequest
	if len(message.Data) > 0 {
		if err := json.Unmarshal(message.Data, &request); err != nil {
			_ = message.Respond(nil)
			return
		}
	}
	prefix := server.system + "."
	matched := make([]string, 0)
	for _, full := range server.store.Subjects() {
		relative, ok := strings.CutPrefix(full, prefix)
		if !ok || relative == "" {
			continue
		}
		if request.Suffix != "" && !strings.HasSuffix(relative, request.Suffix) {
			continue
		}
		if request.Prefix != "" && !strings.HasPrefix(relative, request.Prefix) {
			continue
		}
		matched = append(matched, relative)
	}
	sort.Strings(matched)
	body, err := json.Marshal(ListResponse{Subjects: matched})
	if err != nil {
		_ = message.Respond(nil)
		return
	}
	_ = message.Respond(body)
}
