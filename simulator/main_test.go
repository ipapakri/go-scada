package main

import (
	"encoding/json"
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
	if alerts != 8 {
		t.Errorf("alerts = %d, want 8", alerts)
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

func TestExpandReplicasStampsUniqueCopies(t *testing.T) {
	data, err := descriptorFiles.ReadFile("config/descriptors.json")
	if err != nil {
		t.Fatal(err)
	}
	template, err := parseManifest(data)
	if err != nil {
		t.Fatal(err)
	}

	unchanged, err := expandReplicas(template, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(unchanged) != len(template) {
		t.Fatalf("replicas=0 length = %d, want %d", len(unchanged), len(template))
	}

	replicable := 0
	for _, entry := range template {
		if !isSharedReplicaSubject(entry.Subject) {
			replicable++
		}
	}
	entries, err := expandReplicas(template, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(template)+2*replicable {
		t.Fatalf("expanded length = %d, want %d", len(entries), len(template)+2*replicable)
	}

	bySubject := map[string]manifestEntry{}
	var properties int
	for _, entry := range entries {
		if _, exists := bySubject[entry.Subject]; exists {
			t.Fatalf("duplicate expanded subject %q", entry.Subject)
		}
		bySubject[entry.Subject] = entry
		if strings.HasPrefix(entry.Subject, "AlertProperties.") {
			properties++
		}
	}
	if properties != 1 {
		t.Fatalf("AlertProperties copies = %d, want 1", properties)
	}

	replica, ok := bySubject["plant.001.tank.level.address"]
	if !ok {
		t.Fatal("missing replica address")
	}
	if !strings.Contains(string(replica.Value), `"Modbus.SimulatorTank.config"`) {
		t.Fatalf("replica did not reuse original connection: %s", replica.Value)
	}
	if strings.Contains(string(replica.Value), `Modbus.SimulatorTank.001.config`) {
		t.Fatalf("replica cloned a connection: %s", replica.Value)
	}
	if got := addressOffset(t, replica.Value); got != 0 {
		t.Fatalf("plant.001 tank level address = %d, want 0", got)
	}

	if got := addressOffset(t, bySubject["plant.002.tank.level.address"].Value); got != 20 {
		t.Fatalf("plant.002 tank level address = %d, want 20", got)
	}
	if got := addressOffset(t, bySubject["plant.002.tank.level_high.address"].Value); got != 3 {
		t.Fatalf("plant.002 level_high address = %d, want 3", got)
	}
	if got := addressOffset(t, bySubject["plant.002.valve.inlet.open.address"].Value); got != 2 {
		t.Fatalf("plant.002 inlet coil address = %d, want 2", got)
	}
	if got := addressOffset(t, bySubject["plant.002.pump1.speed.address"].Value); got != 28 {
		t.Fatalf("plant.002 pump speed address = %d, want 28", got)
	}
	if got := addressOffset(t, bySubject["plant.002.utility.process_flow.address"].Value); got != 16 {
		t.Fatalf("plant.002 utility flow address = %d, want 16", got)
	}

	var connections int
	for subject := range bySubject {
		if strings.HasPrefix(subject, "Modbus.") && strings.HasSuffix(subject, ".config") {
			connections++
		}
	}
	if connections != 3 {
		t.Fatalf("connections = %d, want 3 shared originals", connections)
	}
	if _, ok := bySubject["Modbus.SimulatorTank.001.config"]; ok {
		t.Fatal("replica cloned Modbus.SimulatorTank.config")
	}

	summary, ok := bySubject["plant.002.tank.alert_config"]
	if !ok {
		t.Fatal("missing replica summary")
	}
	encoded := string(summary.Value)
	if !strings.Contains(encoded, `plant.002.tank.level_high.alert`) ||
		!strings.Contains(encoded, `plant.002.tank.temperature.alert`) {
		t.Fatalf("summary members not rewritten: %s", encoded)
	}
	if strings.Contains(encoded, `"plant.tank.level_high.alert"`) {
		t.Fatalf("summary retained operator member: %s", encoded)
	}

	levelAlert := bySubject["plant.001.tank.level_high.alert_config"]
	if !strings.Contains(string(levelAlert.Value), `Plant 001 tank`) {
		t.Fatalf("alert text not stamped: %s", levelAlert.Value)
	}
}

func TestExpandReplicasRejectsInvalidCounts(t *testing.T) {
	_, err := expandReplicas(nil, -1)
	if err == nil || !strings.Contains(err.Error(), "replicas must be >= 0") {
		t.Fatalf("expected negative replica error, got %v", err)
	}
	_, err = expandReplicas(nil, maxReplicas+1)
	if err == nil || !strings.Contains(err.Error(), "replicas must be <=") {
		t.Fatalf("expected max replica error, got %v", err)
	}
}

func TestExpandReplicasMapsTenthPlantRegisters(t *testing.T) {
	data, err := descriptorFiles.ReadFile("config/descriptors.json")
	if err != nil {
		t.Fatal(err)
	}
	template, err := parseManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := expandReplicas(template, 10)
	if err != nil {
		t.Fatal(err)
	}
	bySubject := map[string]manifestEntry{}
	for _, entry := range entries {
		bySubject[entry.Subject] = entry
	}
	if got := addressOffset(t, bySubject["plant.010.tank.level.address"].Value); got != 180 {
		t.Fatalf("plant.010 tank level address = %d, want 180", got)
	}
	if got := addressOffset(t, bySubject["plant.010.tank.level_high.address"].Value); got != 27 {
		t.Fatalf("plant.010 level_high address = %d, want 27", got)
	}
	if got := addressOffset(t, bySubject["plant.010.valve.inlet.open.address"].Value); got != 18 {
		t.Fatalf("plant.010 inlet coil address = %d, want 18", got)
	}
	if got := addressOffset(t, bySubject["plant.010.pump1.speed.address"].Value); got != 252 {
		t.Fatalf("plant.010 pump speed address = %d, want 252", got)
	}
	if got := addressOffset(t, bySubject["plant.010.utility.process_flow.address"].Value); got != 144 {
		t.Fatalf("plant.010 utility flow address = %d, want 144", got)
	}
}

func addressOffset(t *testing.T, raw json.RawMessage) int {
	t.Helper()
	var payload struct {
		Config struct {
			Address int `json:"address"`
		} `json:"config"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	return payload.Config.Address
}
