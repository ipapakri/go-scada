package alert

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestProtocolRoundTrips(t *testing.T) {
	t.Parallel()

	properties := Properties{
		Version:                 CurrentVersion,
		Color:                   "#d00",
		Abbreviation:            "ALM",
		ShortSign:               "!",
		Priority:                10,
		RequiresAcknowledgement: true,
	}
	value, err := MarshalProperties(properties)
	if err != nil {
		t.Fatal(err)
	}
	decodedProperties, err := ParseProperties(value)
	if err != nil {
		t.Fatal(err)
	}
	if decodedProperties != properties {
		t.Fatalf("properties mismatch: got %+v, want %+v", decodedProperties, properties)
	}

	config := Config{
		Version: CurrentVersion,
		Enabled: true,
		Type:    TypeBinary,
		Binary: &BinaryConfig{
			BadValue: false,
			True: Mapping{
				Text: "running",
			},
			False: Mapping{
				Property: "AlertProperties.Fault",
				Text:     "stopped",
			},
		},
	}
	value, err = MarshalConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	decodedConfig, err := ParseConfig(value)
	if err != nil {
		t.Fatal(err)
	}
	if decodedConfig.Binary == nil ||
		decodedConfig.Binary.False.Text != config.Binary.False.Text {
		t.Fatalf("config mismatch: got %+v, want %+v", decodedConfig, config)
	}
}

func TestParseConfigRejectsInvalidJSONAndVariants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		match string
	}{
		{
			name:  "unknown nested field",
			value: `{"version":1,"enabled":true,"type":"binary","binary":{"bad_value":true,"true":{"property":"AlertProperties.Bad","text":"bad","typo":1},"false":{"text":"good"}}}`,
			match: "unknown field",
		},
		{
			name:  "trailing JSON",
			value: `{"version":1,"enabled":true,"type":"summary","summary":{"members":["a.alert"]}} {}`,
			match: "multiple JSON values",
		},
		{
			name:  "mismatched variant",
			value: `{"version":1,"enabled":true,"type":"value","binary":{"bad_value":true,"true":{"property":"AlertProperties.Bad","text":"bad"},"false":{"text":"good"}}}`,
			match: "requires value variant",
		},
		{
			name:  "multiple variants",
			value: `{"version":1,"enabled":true,"type":"summary","summary":{"members":["a.alert"]},"binary":{"bad_value":true,"true":{"property":"AlertProperties.Bad","text":"bad"},"false":{"text":"good"}}}`,
			match: "exactly one variant",
		},
		{
			name:  "good mapping has properties",
			value: `{"version":1,"enabled":true,"type":"binary","binary":{"bad_value":true,"true":{"property":"AlertProperties.Bad","text":"bad"},"false":{"property":"AlertProperties.Good","text":"good"}}}`,
			match: "must not define",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseConfig(test.value)
			if err == nil || !strings.Contains(err.Error(), test.match) {
				t.Fatalf("got error %v, want match %q", err, test.match)
			}
		})
	}
}

func TestValueConfigRangeValidation(t *testing.T) {
	t.Parallel()

	valid := Config{
		Version: CurrentVersion,
		Enabled: true,
		Type:    TypeValue,
		Value: &ValueConfig{
			ValueType: ValueTypeFloat64,
			Intervals: []Interval{
				{
					Max:      number("0"),
					Active:   true,
					Property: "AlertProperties.Low",
					Text:     "low",
				},
				{
					Min:  number("0.0"),
					Max:  number("100"),
					Text: "normal",
				},
				{
					Min:      number("100.0"),
					Active:   true,
					Property: "AlertProperties.High",
					Text:     "high",
				},
			},
		},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid exhaustive ranges rejected: %v", err)
	}

	tests := []struct {
		name  string
		edit  func(*Config)
		match string
	}{
		{
			name: "bounded start",
			edit: func(config *Config) {
				config.Value.Intervals[0].Min = number("-100")
			},
			match: "unbounded min",
		},
		{
			name: "gap",
			edit: func(config *Config) {
				config.Value.Intervals[1].Min = number("1")
			},
			match: "exact boundary",
		},
		{
			name: "overlap",
			edit: func(config *Config) {
				config.Value.Intervals[1].Min = number("-1")
			},
			match: "exact boundary",
		},
		{
			name: "reversed",
			edit: func(config *Config) {
				config.Value.Intervals[1].Max = number("-1")
			},
			match: "less than max",
		},
		{
			name: "good interval has properties",
			edit: func(config *Config) {
				config.Value.Intervals[1].Property = "AlertProperties.Normal"
			},
			match: "must not define",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config := cloneConfig(t, valid)
			test.edit(&config)
			err := config.Validate()
			if err == nil || !strings.Contains(err.Error(), test.match) {
				t.Fatalf("got error %v, want match %q", err, test.match)
			}
		})
	}
}

func TestSubjectsAndSummaryReferences(t *testing.T) {
	t.Parallel()

	output, err := OutputSubject("Plant.Pump.trip.alert_config")
	if err != nil {
		t.Fatal(err)
	}
	if output != "Plant.Pump.trip.alert" {
		t.Fatalf("got output %q", output)
	}
	input, err := InputSubject("Plant.Pump.trip.alert_config")
	if err != nil {
		t.Fatal(err)
	}
	if input != "Plant.Pump.trip" {
		t.Fatalf("got input %q", input)
	}
	if err := ValidatePropertiesSubject("AlertProperties.High"); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePropertiesSubject("AlertProperties.Area.High"); err == nil {
		t.Fatal("expected multi-token property name to be rejected")
	}

	config := Config{
		Version: CurrentVersion,
		Enabled: true,
		Type:    TypeSummary,
		Summary: &SummaryConfig{Members: []string{
			"Plant.Pump.trip.alert",
			"Plant.Pump.trip.alert",
		}},
	}
	if err := config.Validate(); err == nil ||
		!strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("got error %v, want duplicate", err)
	}
	config.Summary.Members = []string{"Plant.Area.alert"}
	if err := config.ValidateForSubject("Plant.Area.alert_config"); err == nil ||
		!strings.Contains(err.Error(), "itself") {
		t.Fatalf("got error %v, want self-reference", err)
	}
}

func TestBinaryAndValueEvaluation(t *testing.T) {
	t.Parallel()

	properties := testProperties()
	binary := BinaryConfig{
		BadValue: false,
		True: Mapping{
			Text: "running",
		},
		False: Mapping{
			Property: "AlertProperties.Bad",
			Text:     "stopped",
		},
	}
	evaluation, err := EvaluateBinary(binary, false, properties)
	if err != nil {
		t.Fatal(err)
	}
	if !evaluation.Active || evaluation.Text != "stopped" ||
		evaluation.Priority != 20 {
		t.Fatalf("unexpected binary evaluation: %+v", evaluation)
	}

	valueConfig := ValueConfig{
		ValueType: ValueTypeInt64,
		Intervals: []Interval{
			{
				Max:      number("10"),
				Active:   true,
				Property: "AlertProperties.Bad",
				Text:     "low",
			},
			{
				Min:  number("10"),
				Text: "normal",
			},
		},
	}
	evaluation, err = EvaluateInt64(valueConfig, 10, properties)
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.Active || evaluation.Text != "normal" {
		t.Fatalf("[min,max) boundary selected wrong interval: %+v", evaluation)
	}
	if evaluation.Property != "" {
		t.Fatalf("good interval has alert properties: %+v", evaluation)
	}
	if _, err := EvaluateValue(valueConfig, float64(10), properties); err == nil {
		t.Fatal("expected typed int64 evaluator to reject float64")
	}
}

func TestSummaryDominance(t *testing.T) {
	t.Parallel()

	states := map[string]State{
		"low.alert": testState("AlertProperties.Good", 5, true, false),
		"high.alert": testState(
			"AlertProperties.Bad",
			20,
			true,
			true,
		),
		"cleared.alert": testState(
			"AlertProperties.Bad",
			100,
			false,
			true,
		),
	}
	evaluation, err := EvaluateSummary(
		SummaryConfig{Members: []string{
			"low.alert",
			"cleared.alert",
			"high.alert",
		}},
		states,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !evaluation.Active || !evaluation.Pending ||
		evaluation.Dominant != "high.alert" ||
		len(evaluation.Members) != 3 {
		t.Fatalf("unexpected summary evaluation: %+v", evaluation)
	}
}

func TestLifecycleAndAcknowledgement(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)
	evaluation := Evaluation{
		Active: true,
		Presentation: Presentation{
			Property:                "AlertProperties.Bad",
			Color:                   "red",
			Abbreviation:            "BAD",
			ShortSign:               "!",
			Priority:                20,
			RequiresAcknowledgement: true,
		},
		Text: "trip",
	}
	state, err := ApplyEvaluation(nil, evaluation, start, "episode-1")
	if err != nil {
		t.Fatal(err)
	}
	if !state.Active || !state.Pending || state.Acknowledged ||
		state.CameTime == nil {
		t.Fatalf("unexpected new episode: %+v", state)
	}

	clearTime := start.Add(time.Minute)
	evaluation.Active = false
	state, err = ApplyEvaluation(&state, evaluation, clearTime, "")
	if err != nil {
		t.Fatal(err)
	}
	if state.Active || !state.Pending || state.WentTime == nil {
		t.Fatalf("cleared episode did not remain pending: %+v", state)
	}
	ackTime := clearTime.Add(time.Minute)
	if _, err := Acknowledge(state, "old-episode", ackTime); err == nil {
		t.Fatal("expected stale acknowledgement rejection")
	}
	state, err = Acknowledge(state, "episode-1", ackTime)
	if err != nil {
		t.Fatal(err)
	}
	if state.Pending || !state.Acknowledged || state.AckTime == nil {
		t.Fatalf("unexpected acknowledged state: %+v", state)
	}
	encoded, err := MarshalState(state)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ParseState(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.EpisodeID != state.EpisodeID ||
		!decoded.AckTime.Equal(*state.AckTime) {
		t.Fatalf("state round trip mismatch: got %+v, want %+v", decoded, state)
	}
}

func number(value string) *json.Number {
	number := json.Number(value)
	return &number
}

func cloneConfig(t *testing.T, config Config) Config {
	t.Helper()
	value, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	var clone Config
	decoder := json.NewDecoder(strings.NewReader(string(value)))
	decoder.UseNumber()
	if err := decoder.Decode(&clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func testProperties() map[string]Properties {
	return map[string]Properties{
		"AlertProperties.Good": {
			Version:      CurrentVersion,
			Color:        "green",
			Abbreviation: "OK",
			ShortSign:    "+",
			Priority:     5,
		},
		"AlertProperties.Bad": {
			Version:                 CurrentVersion,
			Color:                   "red",
			Abbreviation:            "BAD",
			ShortSign:               "!",
			Priority:                20,
			RequiresAcknowledgement: true,
		},
	}
}

func testState(
	property string,
	priority int,
	active bool,
	pending bool,
) State {
	came := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)
	state := State{
		Version:      CurrentVersion,
		Active:       active,
		Pending:      pending,
		Text:         property,
		Acknowledged: !pending,
		CameTime:     &came,
		EpisodeID:    "episode-" + property,
	}
	if property != "" {
		state.Presentation = Presentation{
			Property:                property,
			Color:                   "red",
			Abbreviation:            "ALM",
			ShortSign:               "!",
			Priority:                priority,
			RequiresAcknowledgement: pending,
		}
	}
	if !active {
		went := came.Add(time.Minute)
		state.WentTime = &went
	}
	return state
}
