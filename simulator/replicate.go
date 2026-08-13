package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

const maxReplicas = 999

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
		for _, entry := range template {
			if isSharedReplicaSubject(entry.Subject) {
				continue
			}
			cloned, err := cloneReplica(entry, id)
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

func cloneReplica(entry manifestEntry, id string) (manifestEntry, error) {
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
	encoded, err := json.Marshal(value)
	if err != nil {
		return manifestEntry{}, fmt.Errorf("encode %s: %w", subject, err)
	}
	return manifestEntry{Subject: subject, Value: encoded}, nil
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
