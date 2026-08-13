package main

import (
	"strings"
	"testing"
)

func TestEmbeddedManifestIsValid(t *testing.T) {
	data, err := descriptorFiles.ReadFile("config/descriptors.json")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := parseManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 30 {
		t.Fatalf("expected a representative simulator tag set, got %d entries", len(entries))
	}

	var connections, addresses, alerts int
	for _, entry := range entries {
		switch {
		case strings.HasSuffix(entry.Subject, ".config"):
			connections++
		case strings.HasSuffix(entry.Subject, ".address"):
			addresses++
		case strings.HasSuffix(entry.Subject, ".alert_config"):
			alerts++
		}
	}
	if connections != 3 {
		t.Errorf("connections = %d, want 3", connections)
	}
	if addresses != 28 {
		t.Errorf("addresses = %d, want 28", addresses)
	}
	if alerts != 3 {
		t.Errorf("alerts = %d, want 3", alerts)
	}
}

func TestManifestRejectsDuplicateSubjects(t *testing.T) {
	_, err := parseManifest([]byte(`[
		{"subject":"duplicate","value":{}},
		{"subject":"duplicate","value":{}}
	]`))
	if err == nil || !strings.Contains(err.Error(), "duplicate subject") {
		t.Fatalf("expected duplicate subject error, got %v", err)
	}
}
