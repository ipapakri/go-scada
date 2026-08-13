package alert

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"
)

// EvaluateBinary evaluates a binary configuration and resolves its properties.
func EvaluateBinary(
	config BinaryConfig,
	value bool,
	properties map[string]Properties,
) (Evaluation, error) {
	if err := config.validate(); err != nil {
		return Evaluation{}, err
	}
	mapping := config.False
	if value {
		mapping = config.True
	}
	active := value == config.BadValue
	var presentation Presentation
	if active {
		var err error
		presentation, err = resolvePresentation(mapping.Property, properties)
		if err != nil {
			return Evaluation{}, err
		}
	}
	return Evaluation{
		Active:       active,
		Presentation: presentation,
		Text:         mapping.Text,
	}, nil
}

// EvaluateInt64 evaluates an int64 value configuration.
func EvaluateInt64(
	config ValueConfig,
	value int64,
	properties map[string]Properties,
) (Evaluation, error) {
	if config.ValueType != ValueTypeInt64 {
		return Evaluation{}, fmt.Errorf(
			"cannot evaluate int64 with value_type %q",
			config.ValueType,
		)
	}
	if err := config.validate(); err != nil {
		return Evaluation{}, err
	}
	for _, interval := range config.Intervals {
		minOK, err := intLowerContains(interval.Min, value)
		if err != nil {
			return Evaluation{}, err
		}
		maxOK, err := intUpperContains(interval.Max, value)
		if err != nil {
			return Evaluation{}, err
		}
		if minOK && maxOK {
			return evaluationForInterval(interval, properties)
		}
	}
	return Evaluation{}, errors.New("int64 value is not covered by intervals")
}

// EvaluateFloat64 evaluates a finite float64 value configuration.
func EvaluateFloat64(
	config ValueConfig,
	value float64,
	properties map[string]Properties,
) (Evaluation, error) {
	if config.ValueType != ValueTypeFloat64 {
		return Evaluation{}, fmt.Errorf(
			"cannot evaluate float64 with value_type %q",
			config.ValueType,
		)
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return Evaluation{}, errors.New("alert input must be a finite float64")
	}
	if err := config.validate(); err != nil {
		return Evaluation{}, err
	}
	for _, interval := range config.Intervals {
		minOK, err := floatLowerContains(interval.Min, value)
		if err != nil {
			return Evaluation{}, err
		}
		maxOK, err := floatUpperContains(interval.Max, value)
		if err != nil {
			return Evaluation{}, err
		}
		if minOK && maxOK {
			return evaluationForInterval(interval, properties)
		}
	}
	return Evaluation{}, errors.New("float64 value is not covered by intervals")
}

// EvaluateValue dispatches to the evaluator selected by ValueType.
func EvaluateValue(
	config ValueConfig,
	value any,
	properties map[string]Properties,
) (Evaluation, error) {
	switch config.ValueType {
	case ValueTypeInt64:
		integer, ok := value.(int64)
		if !ok {
			return Evaluation{}, fmt.Errorf(
				"int64 alert requires int64 input, got %T",
				value,
			)
		}
		return EvaluateInt64(config, integer, properties)
	case ValueTypeFloat64:
		number, ok := value.(float64)
		if !ok {
			return Evaluation{}, fmt.Errorf(
				"float64 alert requires float64 input, got %T",
				value,
			)
		}
		return EvaluateFloat64(config, number, properties)
	default:
		return Evaluation{}, fmt.Errorf(
			"unsupported alert value_type %q",
			config.ValueType,
		)
	}
}

// EvaluateSummary resolves active and pending members and picks the member
// with the greatest priority. Equal priorities retain configuration order.
func EvaluateSummary(
	config SummaryConfig,
	states map[string]State,
) (Evaluation, error) {
	if err := config.validate(); err != nil {
		return Evaluation{}, err
	}
	var result Evaluation
	var dominant *State
	for _, subject := range config.Members {
		state, exists := states[subject]
		if !exists {
			return Evaluation{}, fmt.Errorf(
				"summary member %q has no runtime state",
				subject,
			)
		}
		if err := state.Validate(); err != nil {
			return Evaluation{}, fmt.Errorf(
				"summary member %q: %w",
				subject,
				err,
			)
		}
		if !state.Active && !state.Pending {
			continue
		}
		result.Members = append(result.Members, subject)
		result.Active = result.Active || state.Active
		result.Pending = result.Pending || state.Pending
		if dominant == nil ||
			(state.Active && !dominant.Active) ||
			(state.Active == dominant.Active &&
				state.Priority > dominant.Priority) {
			copy := state
			dominant = &copy
			result.Dominant = subject
		}
	}
	if dominant == nil {
		result.Acknowledged = true
		return result, nil
	}
	result.Presentation = dominant.Presentation
	result.Text = dominant.Text
	result.Acknowledged = !result.Pending
	return result, nil
}

// ApplyEvaluation applies lifecycle rules to a previous state. episodeID is
// used only when a new active episode starts.
func ApplyEvaluation(
	previous *State,
	evaluation Evaluation,
	now time.Time,
	episodeID string,
) (State, error) {
	now = now.UTC()
	state := State{
		Version:      CurrentVersion,
		Active:       evaluation.Active,
		Presentation: evaluation.Presentation,
		Text:         evaluation.Text,
		Dominant:     evaluation.Dominant,
		Members:      append([]string(nil), evaluation.Members...),
	}

	if previous != nil {
		if err := previous.Validate(); err != nil {
			return State{}, fmt.Errorf("previous alert state: %w", err)
		}
		state.CameTime = cloneTime(previous.CameTime)
		state.WentTime = cloneTime(previous.WentTime)
		state.AckTime = cloneTime(previous.AckTime)
		state.EpisodeID = previous.EpisodeID
		state.Pending = previous.Pending
		state.Acknowledged = previous.Acknowledged
	}

	starting := evaluation.Active && (previous == nil || !previous.Active)
	if starting {
		if episodeID == "" {
			return State{}, errors.New("new active episode requires episode ID")
		}
		state.CameTime = cloneTime(&now)
		state.WentTime = nil
		state.AckTime = nil
		state.EpisodeID = episodeID
		state.Pending = evaluation.Pending ||
			(evaluation.RequiresAcknowledgement &&
				!evaluation.Acknowledged)
		state.Acknowledged = !state.Pending
	} else if evaluation.Active {
		state.Pending = state.Pending || evaluation.Pending
		if state.Pending {
			state.Acknowledged = false
			state.AckTime = nil
		}
	} else {
		if previous != nil && previous.Active {
			state.WentTime = cloneTime(&now)
		}
		state.Pending = state.Pending || evaluation.Pending
		if state.Pending {
			state.Acknowledged = false
			state.AckTime = nil
		} else {
			state.Acknowledged = evaluation.Acknowledged ||
				!evaluation.RequiresAcknowledgement
		}
	}

	if err := state.Validate(); err != nil {
		return State{}, err
	}
	return state, nil
}

// Acknowledge marks a pending episode acknowledged when the episode identity
// matches, allowing callers to reject stale acknowledgement writes.
func Acknowledge(
	state State,
	episodeID string,
	now time.Time,
) (State, error) {
	if err := state.Validate(); err != nil {
		return State{}, err
	}
	if episodeID == "" || episodeID != state.EpisodeID {
		return State{}, errors.New("acknowledgement episode ID does not match")
	}
	if !state.Pending {
		return State{}, errors.New("alert episode is not pending")
	}
	now = now.UTC()
	state.Pending = false
	state.Acknowledged = true
	state.AckTime = cloneTime(&now)
	return state, nil
}

func resolvePresentation(
	subject string,
	properties map[string]Properties,
) (Presentation, error) {
	propertiesValue, exists := properties[subject]
	if !exists {
		return Presentation{}, fmt.Errorf(
			"alert property %q is not defined",
			subject,
		)
	}
	if err := propertiesValue.Validate(); err != nil {
		return Presentation{}, fmt.Errorf(
			"alert property %q: %w",
			subject,
			err,
		)
	}
	return Presentation{
		Property:                subject,
		Color:                   propertiesValue.Color,
		Abbreviation:            propertiesValue.Abbreviation,
		ShortSign:               propertiesValue.ShortSign,
		Priority:                propertiesValue.Priority,
		RequiresAcknowledgement: propertiesValue.RequiresAcknowledgement,
	}, nil
}

func evaluationForInterval(
	interval Interval,
	properties map[string]Properties,
) (Evaluation, error) {
	var presentation Presentation
	if interval.Active {
		var err error
		presentation, err = resolvePresentation(interval.Property, properties)
		if err != nil {
			return Evaluation{}, err
		}
	}
	return Evaluation{
		Active:       interval.Active,
		Presentation: presentation,
		Text:         interval.Text,
	}, nil
}

func intLowerContains(bound *json.Number, value int64) (bool, error) {
	if bound == nil {
		return true, nil
	}
	min, err := strconv.ParseInt(bound.String(), 10, 64)
	if err != nil {
		return false, fmt.Errorf("invalid int64 lower bound %q", bound.String())
	}
	return value >= min, nil
}

func intUpperContains(bound *json.Number, value int64) (bool, error) {
	if bound == nil {
		return true, nil
	}
	max, err := strconv.ParseInt(bound.String(), 10, 64)
	if err != nil {
		return false, fmt.Errorf("invalid int64 upper bound %q", bound.String())
	}
	return value < max, nil
}

func floatLowerContains(bound *json.Number, value float64) (bool, error) {
	if bound == nil {
		return true, nil
	}
	min, err := strconv.ParseFloat(bound.String(), 64)
	if err != nil {
		return false, fmt.Errorf("invalid float64 lower bound %q", bound.String())
	}
	return value >= min, nil
}

func floatUpperContains(bound *json.Number, value float64) (bool, error) {
	if bound == nil {
		return true, nil
	}
	max, err := strconv.ParseFloat(bound.String(), 64)
	if err != nil {
		return false, fmt.Errorf("invalid float64 upper bound %q", bound.String())
	}
	return value < max, nil
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
