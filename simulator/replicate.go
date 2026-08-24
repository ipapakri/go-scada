package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

const maxReplicas = 999

// replicaStrides match simulator/node-red/lib/simulator.js REGISTER_MAP.
// Plant n (1-based, subject plant.NNN) uses instance n-1, so plant.001 keeps
// the operator plant's original register block.
var replicaStrides = map[string]map[string]int{
	"Modbus.SimulatorTank.config": {
		"input":          20,
		"holding":        20,
		"discrete_input": 3,
		"coil":           2,
	},
	"Modbus.SimulatorPumps.config": {
		"input":          28,
		"holding":        28,
		"discrete_input": 4,
	},
	"Modbus.SimulatorUtility.config": {
		"input":          16,
		"holding":        16,
		"discrete_input": 2,
		"coil":           1,
	},
}

func expandReplicas(template []manifestEntry, replicas int) ([]manifestEntry, error) {
	if replicas < 0 {
		return nil, fmt.Errorf("replicas must be >= 0")
	}
	if replicas > maxReplicas {
		return nil, fmt.Errorf("replicas must be <= %d", maxReplicas)
	}

	expanded := append([]manifestEntry(nil), template...)
	for index := 1; index <= replicas; index++ {
		id := fmt.Sprintf("%03d", index)
		instance := index - 1
		for _, entry := range template {
			if isSharedReplicaSubject(entry.Subject) {
				continue
			}
			cloned, err := cloneReplica(entry, id, instance)
			if err != nil {
				return nil, fmt.Errorf("replica %s: %w", id, err)
			}
			expanded = append(expanded, cloned)
		}
	}
	if err := validateManifest(expanded); err != nil {
		return nil, err
	}
	return expanded, nil
}

func cloneReplica(entry manifestEntry, id string, instance int) (manifestEntry, error) {
	subject, err := rewriteSubject(entry.Subject, id)
	if err != nil {
		return manifestEntry{}, err
	}

	var value any
	if err := json.Unmarshal(entry.Value, &value); err != nil {
		return manifestEntry{}, fmt.Errorf("decode %s: %w", entry.Subject, err)
	}
	if err := rewriteValue(value, id); err != nil {
		return manifestEntry{}, fmt.Errorf("rewrite %s: %w", entry.Subject, err)
	}
	if strings.HasSuffix(entry.Subject, ".address") {
		if err := offsetReplicaAddress(value, instance); err != nil {
			return manifestEntry{}, fmt.Errorf("offset %s: %w", entry.Subject, err)
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return manifestEntry{}, fmt.Errorf("encode %s: %w", subject, err)
	}
	return manifestEntry{Subject: subject, Value: encoded}, nil
}

func offsetReplicaAddress(value any, instance int) error {
	if instance == 0 {
		return nil
	}
	typed, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("address value must be an object")
	}
	connection, _ := typed["connection"].(string)
	config, ok := typed["config"].(map[string]any)
	if !ok {
		return fmt.Errorf("address config must be an object")
	}
	register, _ := config["register"].(string)
	stride, err := replicaStride(connection, register)
	if err != nil {
		return err
	}
	address, err := jsonInt(config["address"])
	if err != nil {
		return fmt.Errorf("address: %w", err)
	}
	next := address + instance*stride
	if next > 0xffff {
		return fmt.Errorf("address %d exceeds the register range", next)
	}
	config["address"] = next
	return nil
}

func replicaStride(connection, register string) (int, error) {
	byRegister, ok := replicaStrides[connection]
	if !ok {
		return 0, fmt.Errorf("no replica stride for connection %q", connection)
	}
	stride, ok := byRegister[register]
	if !ok {
		return 0, fmt.Errorf("no replica stride for %s register %q", connection, register)
	}
	return stride, nil
}

func jsonInt(value any) (int, error) {
	switch typed := value.(type) {
	case float64:
		return int(typed), nil
	case int:
		return typed, nil
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, err
		}
		return int(parsed), nil
	default:
		return 0, fmt.Errorf("expected a number, got %T", value)
	}
}

func isSharedReplicaSubject(subject string) bool {
	if strings.HasPrefix(subject, "AlertProperties.") {
		return true
	}
	return strings.HasPrefix(subject, "Modbus.") && strings.HasSuffix(subject, ".config")
}

func rewriteSubject(subject, id string) (string, error) {
	if isSharedReplicaSubject(subject) {
		return subject, nil
	}
	if strings.HasPrefix(subject, "plant.") {
		return "plant." + id + "." + strings.TrimPrefix(subject, "plant."), nil
	}
	return "", fmt.Errorf("cannot replicate subject %q", subject)
}

func rewriteValue(value any, id string) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if key == "text" {
				text, ok := child.(string)
				if !ok {
					return fmt.Errorf("text must be a string")
				}
				typed[key] = strings.Replace(text, "Plant ", "Plant "+id+" ", 1)
				continue
			}
			if err := rewriteValue(child, id); err != nil {
				return err
			}
		}
	case []any:
		for index, child := range typed {
			text, ok := child.(string)
			if !ok {
				if err := rewriteValue(child, id); err != nil {
					return err
				}
				continue
			}
			rewritten, err := rewriteSubject(text, id)
			if err != nil {
				return err
			}
			typed[index] = rewritten
		}
	}
	return nil
}
