package alert

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
)

// ParseProperties decodes and validates an AlertProperties value.
func ParseProperties(value string) (Properties, error) {
	var properties Properties
	if err := decodeStrict(value, &properties, "alert properties"); err != nil {
		return Properties{}, err
	}
	if err := properties.Validate(); err != nil {
		return Properties{}, err
	}
	return properties, nil
}

// MarshalProperties validates and encodes an AlertProperties value.
func MarshalProperties(properties Properties) (string, error) {
	if err := properties.Validate(); err != nil {
		return "", err
	}
	return encode(properties, "alert properties")
}

// ParseConfig decodes and validates an .alert_config value.
func ParseConfig(value string) (Config, error) {
	var config Config
	if err := decodeStrict(value, &config, "alert config"); err != nil {
		return Config{}, err
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

// MarshalConfig validates and encodes an .alert_config value.
func MarshalConfig(config Config) (string, error) {
	if err := config.Validate(); err != nil {
		return "", err
	}
	return encode(config, "alert config")
}

// ParseState decodes and validates an .alert runtime value.
func ParseState(value string) (State, error) {
	state, err := parseStateUnchecked(value)
	if err != nil {
		return State{}, err
	}
	if err := state.Validate(); err != nil {
		return State{}, err
	}
	return state, nil
}

// parseStateUnchecked enforces the wire schema without runtime invariants.
// It is used for same-subject acknowledgement requests, where an operator may
// set acknowledged while the retained canonical state still says pending.
func parseStateUnchecked(value string) (State, error) {
	var state State
	if err := decodeStrict(value, &state, "alert state"); err != nil {
		return State{}, err
	}
	return state, nil
}

// MarshalState validates and encodes an .alert runtime value.
func MarshalState(state State) (string, error) {
	if err := state.Validate(); err != nil {
		return "", err
	}
	return encode(state, "alert state")
}

// ValidatePropertiesSubject checks an AlertProperties.<name> subject.
func ValidatePropertiesSubject(subject string) error {
	if err := validateSubject(subject); err != nil {
		return fmt.Errorf("invalid alert properties subject: %w", err)
	}
	name := strings.TrimPrefix(subject, propertiesPrefix)
	if name == subject || name == "" || strings.Contains(name, ".") {
		return errors.New(
			"alert properties subject must be AlertProperties.<name>",
		)
	}
	return nil
}

// InputSubject derives the telemetry subject monitored by an alert config.
func InputSubject(configSubject string) (string, error) {
	if err := validateSubject(configSubject); err != nil {
		return "", fmt.Errorf("invalid alert config subject: %w", err)
	}
	base := strings.TrimSuffix(configSubject, configSuffix)
	if base == configSubject || base == "" {
		return "", errors.New("alert config subject must end in .alert_config")
	}
	return base, nil
}

// OutputSubject derives the runtime subject for an alert configuration.
func OutputSubject(configSubject string) (string, error) {
	input, err := InputSubject(configSubject)
	if err != nil {
		return "", err
	}
	return input + alertSuffix, nil
}

// ValidateForSubject performs checks that depend on the owning config subject.
func (config Config) ValidateForSubject(subject string) error {
	if err := config.Validate(); err != nil {
		return err
	}
	output, err := OutputSubject(subject)
	if err != nil {
		return err
	}
	if config.Summary != nil {
		for _, member := range config.Summary.Members {
			if member == output {
				return fmt.Errorf("summary alert %q cannot include itself", output)
			}
		}
	}
	return nil
}

// Validate checks alert property fields.
func (properties Properties) Validate() error {
	if properties.Version != CurrentVersion {
		return fmt.Errorf(
			"unsupported alert properties version %d",
			properties.Version,
		)
	}
	if strings.TrimSpace(properties.Color) == "" {
		return errors.New("alert properties color is required")
	}
	if strings.TrimSpace(properties.Abbreviation) == "" {
		return errors.New("alert properties abbreviation is required")
	}
	if strings.TrimSpace(properties.ShortSign) == "" {
		return errors.New("alert properties short_sign is required")
	}
	if properties.Priority < 0 {
		return errors.New("alert properties priority must not be negative")
	}
	return nil
}

// Validate checks the tagged config and its selected variant.
func (config Config) Validate() error {
	if config.Version != CurrentVersion {
		return fmt.Errorf("unsupported alert config version %d", config.Version)
	}
	var variantCount int
	if config.Binary != nil {
		variantCount++
	}
	if config.Value != nil {
		variantCount++
	}
	if config.Summary != nil {
		variantCount++
	}
	if variantCount != 1 {
		return errors.New("alert config must contain exactly one variant")
	}
	switch config.Type {
	case TypeBinary:
		if config.Binary == nil {
			return errors.New("binary alert config requires binary variant")
		}
		return config.Binary.validate()
	case TypeValue:
		if config.Value == nil {
			return errors.New("value alert config requires value variant")
		}
		return config.Value.validate()
	case TypeSummary:
		if config.Summary == nil {
			return errors.New("summary alert config requires summary variant")
		}
		return config.Summary.validate()
	default:
		return fmt.Errorf("unsupported alert config type %q", config.Type)
	}
}

func (config BinaryConfig) validate() error {
	if err := config.True.validate("true", config.BadValue); err != nil {
		return err
	}
	return config.False.validate("false", !config.BadValue)
}

func (mapping Mapping) validate(name string, active bool) error {
	if active {
		if err := validatePropertyReference(mapping.Property); err != nil {
			return fmt.Errorf("binary %s mapping: %w", name, err)
		}
	} else if mapping.Property != "" {
		return fmt.Errorf(
			"binary %s good mapping must not define alert properties",
			name,
		)
	}
	if strings.TrimSpace(mapping.Text) == "" {
		return fmt.Errorf("binary %s mapping text is required", name)
	}
	return nil
}

func (config ValueConfig) validate() error {
	switch config.ValueType {
	case ValueTypeInt64, ValueTypeFloat64:
	default:
		return fmt.Errorf("unsupported alert value_type %q", config.ValueType)
	}
	if len(config.Intervals) == 0 {
		return errors.New("value alert intervals are required")
	}
	for index := range config.Intervals {
		if err := config.validateInterval(index); err != nil {
			return err
		}
	}
	if config.Intervals[0].Min != nil {
		return errors.New("value alert first interval must have unbounded min")
	}
	if config.Intervals[len(config.Intervals)-1].Max != nil {
		return errors.New("value alert last interval must have unbounded max")
	}
	for index := 1; index < len(config.Intervals); index++ {
		previous := config.Intervals[index-1]
		current := config.Intervals[index]
		if previous.Max == nil || current.Min == nil ||
			!config.boundsEqual(previous.Max, current.Min) {
			return fmt.Errorf(
				"value alert intervals %d and %d must share an exact boundary",
				index-1,
				index,
			)
		}
	}
	return nil
}

func (config ValueConfig) validateInterval(index int) error {
	interval := config.Intervals[index]
	if interval.Active {
		if err := validatePropertyReference(interval.Property); err != nil {
			return fmt.Errorf("value interval %d: %w", index, err)
		}
	} else if interval.Property != "" {
		return fmt.Errorf(
			"value interval %d good range must not define alert properties",
			index,
		)
	}
	if strings.TrimSpace(interval.Text) == "" {
		return fmt.Errorf("value interval %d text is required", index)
	}
	hasMin, hasMax := interval.Min != nil, interval.Max != nil
	if _, _, err := config.parseBound(interval.Min); err != nil {
		return fmt.Errorf("value interval %d min: %w", index, err)
	}
	if _, _, err := config.parseBound(interval.Max); err != nil {
		return fmt.Errorf("value interval %d max: %w", index, err)
	}
	if hasMin && hasMax && !config.boundLess(interval.Min, interval.Max) {
		return fmt.Errorf("value interval %d min must be less than max", index)
	}
	return nil
}

func (config ValueConfig) boundLess(left, right *json.Number) bool {
	if config.ValueType == ValueTypeInt64 {
		leftValue, _ := strconv.ParseInt(left.String(), 10, 64)
		rightValue, _ := strconv.ParseInt(right.String(), 10, 64)
		return leftValue < rightValue
	}
	leftValue, _ := strconv.ParseFloat(left.String(), 64)
	rightValue, _ := strconv.ParseFloat(right.String(), 64)
	return leftValue < rightValue
}

func (config ValueConfig) boundsEqual(left, right *json.Number) bool {
	if config.ValueType == ValueTypeInt64 {
		leftValue, leftErr := strconv.ParseInt(left.String(), 10, 64)
		rightValue, rightErr := strconv.ParseInt(right.String(), 10, 64)
		return leftErr == nil && rightErr == nil && leftValue == rightValue
	}
	leftValue, leftErr := strconv.ParseFloat(left.String(), 64)
	rightValue, rightErr := strconv.ParseFloat(right.String(), 64)
	return leftErr == nil && rightErr == nil && leftValue == rightValue
}

func (config ValueConfig) parseBound(bound *json.Number) (float64, bool, error) {
	if bound == nil {
		return 0, false, nil
	}
	if config.ValueType == ValueTypeInt64 {
		value, err := strconv.ParseInt(bound.String(), 10, 64)
		if err != nil {
			return 0, false, errors.New("must be an int64")
		}
		return float64(value), true, nil
	}
	value, err := strconv.ParseFloat(bound.String(), 64)
	if err != nil {
		return 0, false, errors.New("must be a finite float64")
	}
	if math.IsInf(value, 0) || math.IsNaN(value) {
		return 0, false, errors.New("must be a finite float64")
	}
	return value, true, nil
}

func (config SummaryConfig) validate() error {
	if len(config.Members) == 0 {
		return errors.New("summary alert members are required")
	}
	seen := make(map[string]struct{}, len(config.Members))
	for _, member := range config.Members {
		if err := validateSubject(member); err != nil {
			return fmt.Errorf("invalid summary member %q: %w", member, err)
		}
		if !strings.HasSuffix(member, alertSuffix) ||
			strings.TrimSuffix(member, alertSuffix) == "" {
			return fmt.Errorf("summary member %q must end in .alert", member)
		}
		if _, exists := seen[member]; exists {
			return fmt.Errorf("duplicate summary member %q", member)
		}
		seen[member] = struct{}{}
	}
	return nil
}

// Validate checks runtime-state invariants.
func (state State) Validate() error {
	if state.Version != CurrentVersion {
		return fmt.Errorf("unsupported alert state version %d", state.Version)
	}
	if state.Pending && state.Acknowledged {
		return errors.New("pending alert state cannot be acknowledged")
	}
	if state.Active && state.EpisodeID == "" {
		return errors.New("active alert state requires episode_id")
	}
	if state.Active && state.CameTime == nil {
		return errors.New("active alert state requires came_time")
	}
	if state.Active && state.WentTime != nil {
		return errors.New("active alert state cannot have went_time")
	}
	if state.WentTime != nil && state.CameTime == nil {
		return errors.New("alert state went_time requires came_time")
	}
	if state.AckTime != nil && !state.Acknowledged {
		return errors.New("alert state ack_time requires acknowledged")
	}
	for name, value := range map[string]*time.Time{
		"came_time": state.CameTime,
		"went_time": state.WentTime,
		"ack_time":  state.AckTime,
	} {
		if value != nil {
			_, offset := value.Zone()
			if offset == 0 {
				continue
			}
			return fmt.Errorf("alert state %s must be UTC", name)
		}
	}
	hasPresentation := state.Property != "" || state.Color != "" ||
		state.Abbreviation != "" || state.ShortSign != "" ||
		state.Priority != 0 || state.RequiresAcknowledgement
	if hasPresentation {
		if err := validatePropertyReference(state.Property); err != nil {
			return fmt.Errorf("alert state: %w", err)
		}
		if strings.TrimSpace(state.Color) == "" ||
			strings.TrimSpace(state.Abbreviation) == "" ||
			strings.TrimSpace(state.ShortSign) == "" {
			return errors.New("alert state property presentation is incomplete")
		}
	}
	seen := make(map[string]struct{}, len(state.Members))
	for _, member := range state.Members {
		if err := validateSubject(member); err != nil ||
			!strings.HasSuffix(member, alertSuffix) {
			return fmt.Errorf("invalid alert state member %q", member)
		}
		if _, exists := seen[member]; exists {
			return fmt.Errorf("duplicate alert state member %q", member)
		}
		seen[member] = struct{}{}
	}
	if state.Dominant != "" {
		if _, exists := seen[state.Dominant]; !exists {
			return errors.New("alert state dominant must be included in members")
		}
	}
	return nil
}

func validatePropertyReference(subject string) error {
	if err := ValidatePropertiesSubject(subject); err != nil {
		return fmt.Errorf("invalid property reference %q: %w", subject, err)
	}
	return nil
}

func validateSubject(subject string) error {
	if strings.TrimSpace(subject) == "" {
		return errors.New("subject is required")
	}
	if subject != strings.TrimSpace(subject) {
		return errors.New("subject must not contain surrounding whitespace")
	}
	for _, token := range strings.Split(subject, ".") {
		if token == "" {
			return errors.New("subject contains an empty token")
		}
		if strings.ContainsAny(token, " \t\r\n*>") {
			return errors.New("subject contains whitespace or wildcards")
		}
	}
	return nil
}

func decodeStrict(value string, destination any, name string) error {
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode %s: %w", name, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%s contains multiple JSON values", name)
		}
		return fmt.Errorf("finish decoding %s: %w", name, err)
	}
	return nil
}

func encode(value any, name string) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode %s: %w", name, err)
	}
	return string(data), nil
}
