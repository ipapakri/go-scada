package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"strings"

	"go-scada/address"
	"go-scada/modbus"
	"go-scada/stream"
)

//go:embed config/descriptors.json
var descriptorFiles embed.FS

type manifestEntry struct {
	Subject string          `json:"subject"`
	Value   json.RawMessage `json:"value"`
}

func main() {
	configPath := flag.String(
		"config",
		stream.DefaultConfigPath,
		"stream configuration file",
	)
	flag.Parse()

	data, err := descriptorFiles.ReadFile("config/descriptors.json")
	if err != nil {
		log.Fatalf("read simulator descriptors: %v", err)
	}
	entries, err := parseManifest(data)
	if err != nil {
		log.Fatalf("validate simulator descriptors: %v", err)
	}

	client, err := stream.New(*configPath)
	if err != nil {
		log.Fatalf("connect to stream: %v", err)
	}
	defer client.Close()

	for _, entry := range entries {
		if err := stream.Set(client, entry.Subject, string(entry.Value)); err != nil {
			log.Fatalf("seed %s: %v", entry.Subject, err)
		}
	}
	log.Printf("seeded %d simulator descriptors", len(entries))
}

func parseManifest(data []byte) ([]manifestEntry, error) {
	var entries []manifestEntry
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&entries); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("manifest contains multiple JSON values")
		}
		return nil, fmt.Errorf("finish manifest: %w", err)
	}

	subjects := make(map[string]struct{}, len(entries))
	connections := make(map[string]modbus.Connection)
	for index, entry := range entries {
		entry.Subject = strings.TrimSpace(entry.Subject)
		entries[index].Subject = entry.Subject
		if entry.Subject == "" {
			return nil, fmt.Errorf("entry %d has an empty subject", index)
		}
		if _, exists := subjects[entry.Subject]; exists {
			return nil, fmt.Errorf("duplicate subject %q", entry.Subject)
		}
		subjects[entry.Subject] = struct{}{}
		if len(bytes.TrimSpace(entry.Value)) == 0 || !json.Valid(entry.Value) {
			return nil, fmt.Errorf("subject %q has invalid JSON", entry.Subject)
		}
		if !strings.HasSuffix(entry.Subject, ".config") {
			continue
		}
		envelope, err := address.ParseConnection(string(entry.Value))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Subject, err)
		}
		connection, err := modbus.ParseConnection(envelope)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Subject, err)
		}
		connections[entry.Subject] = connection
	}

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Subject, ".address") {
			continue
		}
		envelope, err := address.Parse(string(entry.Value))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Subject, err)
		}
		connection, exists := connections[envelope.Connection]
		if !exists {
			return nil, fmt.Errorf(
				"%s references unknown connection %q",
				entry.Subject,
				envelope.Connection,
			)
		}
		if _, err := modbus.ParsePoint(envelope, connection); err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Subject, err)
		}
	}
	return entries, nil
}
