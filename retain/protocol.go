package retain

// QueueGroup is the NATS queue group for last-value request-reply so only one
// retain instance answers each Get/List request.
const QueueGroup = "retain"

// HeaderError is set on request-reply messages when the lookup fails.
const HeaderError = "X-Retain-Error"

// ErrorNotFound is the HeaderError value for a missing subject.
const ErrorNotFound = "not_found"

// GetSubject is the request-reply subject for fetching one retained payload.
func GetSubject(systemName string) string {
	return "retain." + systemName + ".get"
}

// ListSubject is the request-reply subject for listing retained subjects.
func ListSubject(systemName string) string {
	return "retain." + systemName + ".list"
}

// ListRequest selects subjects by suffix and/or prefix. Prefixes should already
// include a trailing dot so matching is token-bounded.
type ListRequest struct {
	Suffix string `json:"suffix,omitempty"`
	Prefix string `json:"prefix,omitempty"`
}

// ListResponse contains system-relative subject names.
type ListResponse struct {
	Subjects []string `json:"subjects"`
}
