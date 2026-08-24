package stream

import (
	"encoding/json"
	"strings"
)

// SubjectCreatedSubject is the system-relative subject that announces the
// first write to a telemetry subject.
const SubjectCreatedSubject = "registry.subject.created"

// SubjectCreated is published once when a subject first appears in the stream.
type SubjectCreated struct {
	Subject string `json:"subject"`
	Suffix  string `json:"suffix"`
}

// LastToken returns the final subject token, used as the registry suffix.
func LastToken(subject string) string {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return ""
	}
	if index := strings.LastIndex(subject, "."); index >= 0 {
		return subject[index+1:]
	}
	return subject
}

func encodeSubjectCreated(relative string) (string, error) {
	payload, err := json.Marshal(SubjectCreated{
		Subject: relative,
		Suffix:  LastToken(relative),
	})
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func DecodeSubjectCreated(value string) (SubjectCreated, error) {
	var created SubjectCreated
	if err := json.Unmarshal([]byte(value), &created); err != nil {
		return SubjectCreated{}, err
	}
	created.Subject = strings.TrimSpace(created.Subject)
	created.Suffix = strings.TrimSpace(created.Suffix)
	if created.Suffix == "" {
		created.Suffix = LastToken(created.Subject)
	}
	return created, nil
}
