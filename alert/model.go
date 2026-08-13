// Package alert defines the protocol and pure evaluation logic for alerts.
package alert

import (
	"encoding/json"
	"time"
)

const (
	// CurrentVersion is the JSON protocol version understood by this package.
	CurrentVersion = 1

	propertiesPrefix = "AlertProperties."
	configSuffix     = ".alert_config"
	alertSuffix      = ".alert"
)

// Type identifies an alert configuration variant.
type Type string

const (
	TypeBinary  Type = "binary"
	TypeValue   Type = "value"
	TypeSummary Type = "summary"
)

// ValueType identifies the JSON number accepted by a value alert.
type ValueType string

const (
	ValueTypeInt64   ValueType = "int64"
	ValueTypeFloat64 ValueType = "float64"
)

// Properties is the presentation and acknowledgement policy stored under an
// AlertProperties.<name> subject.
type Properties struct {
	Version                 int    `json:"version"`
	Color                   string `json:"color"`
	Abbreviation            string `json:"abbreviation"`
	ShortSign               string `json:"short_sign"`
	Priority                int    `json:"priority"`
	RequiresAcknowledgement bool   `json:"requires_acknowledgement"`
}

// Mapping selects presentation properties and text for a binary state or
// value interval.
type Mapping struct {
	Property string `json:"property,omitempty"`
	Text     string `json:"text"`
}

// BinaryConfig evaluates the boolean subject derived from its config subject.
type BinaryConfig struct {
	BadValue bool    `json:"bad_value"`
	True     Mapping `json:"true"`
	False    Mapping `json:"false"`
}

// Interval is a lower-inclusive, upper-exclusive numeric range. A nil Min or
// Max is an unbounded end.
type Interval struct {
	Min      *json.Number `json:"min"`
	Max      *json.Number `json:"max"`
	Active   bool         `json:"active"`
	Property string       `json:"property,omitempty"`
	Text     string       `json:"text"`
}

// ValueConfig evaluates the numeric subject derived from its config subject.
type ValueConfig struct {
	ValueType ValueType  `json:"value_type"`
	Intervals []Interval `json:"intervals"`
}

// SummaryConfig aggregates member .alert subjects.
type SummaryConfig struct {
	Members []string `json:"members"`
}

// Config is the tagged JSON value stored under a *.alert_config subject.
// Exactly one variant matching Type must be present.
type Config struct {
	Version int  `json:"version"`
	Enabled bool `json:"enabled"`
	Type    Type `json:"type"`

	Binary  *BinaryConfig  `json:"binary,omitempty"`
	Value   *ValueConfig   `json:"value,omitempty"`
	Summary *SummaryConfig `json:"summary,omitempty"`
}

// Presentation is the resolved property data included in runtime state.
type Presentation struct {
	Property                string `json:"property"`
	Color                   string `json:"color"`
	Abbreviation            string `json:"abbreviation"`
	ShortSign               string `json:"short_sign"`
	Priority                int    `json:"priority"`
	RequiresAcknowledgement bool   `json:"requires_acknowledgement"`
}

// State is the canonical JSON value stored under a *.alert subject.
type State struct {
	Version int `json:"version"`

	Active  bool `json:"active"`
	Pending bool `json:"pending"`
	Presentation
	Text         string     `json:"text"`
	Acknowledged bool       `json:"acknowledged"`
	CameTime     *time.Time `json:"came_time,omitempty"`
	WentTime     *time.Time `json:"went_time,omitempty"`
	AckTime      *time.Time `json:"ack_time,omitempty"`
	Dominant     string     `json:"dominant,omitempty"`
	Members      []string   `json:"members,omitempty"`
	EpisodeID    string     `json:"episode_id,omitempty"`
}

// Evaluation is a stateless evaluation result used to construct runtime state.
type Evaluation struct {
	Active       bool
	Pending      bool
	Acknowledged bool
	Presentation
	Text     string
	Dominant string
	Members  []string
}
