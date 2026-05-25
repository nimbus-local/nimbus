package sfn

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type aslDefinition struct {
	Comment string               `json:"Comment"`
	StartAt string               `json:"StartAt"`
	States  map[string]*aslState `json:"States"`
}

type aslState struct {
	Type string `json:"Type"`

	// Pass
	Result     json.RawMessage `json:"Result"`
	ResultPath json.RawMessage `json:"ResultPath"` // absent=nil, JSON null, or quoted path string
	InputPath  json.RawMessage `json:"InputPath"`
	OutputPath json.RawMessage `json:"OutputPath"`

	// Fail
	Error string `json:"Error"`
	Cause string `json:"Cause"`

	// Choice
	Choices []choiceRule `json:"Choices"`
	Default string       `json:"Default"`

	// Wait
	Seconds       int    `json:"Seconds"`
	SecondsPath   string `json:"SecondsPath"`
	Timestamp     string `json:"Timestamp"`
	TimestampPath string `json:"TimestampPath"`

	// Routing
	Next string `json:"Next"`
	End  bool   `json:"End"`
}

type choiceRule struct {
	// Combinator rules (no Variable)
	And []choiceRule `json:"And"`
	Or  []choiceRule `json:"Or"`
	Not *choiceRule  `json:"Not"`

	// Variable-based comparisons
	Variable string `json:"Variable"`

	// String comparisons (literal value)
	StringEquals             *string `json:"StringEquals"`
	StringLessThan           *string `json:"StringLessThan"`
	StringGreaterThan        *string `json:"StringGreaterThan"`
	StringLessThanEquals     *string `json:"StringLessThanEquals"`
	StringGreaterThanEquals  *string `json:"StringGreaterThanEquals"`
	StringMatches            *string `json:"StringMatches"`

	// Numeric comparisons
	NumericEquals            *float64 `json:"NumericEquals"`
	NumericLessThan          *float64 `json:"NumericLessThan"`
	NumericGreaterThan       *float64 `json:"NumericGreaterThan"`
	NumericLessThanEquals    *float64 `json:"NumericLessThanEquals"`
	NumericGreaterThanEquals *float64 `json:"NumericGreaterThanEquals"`

	// Boolean
	BooleanEquals *bool `json:"BooleanEquals"`

	// Type checks
	IsNull    *bool `json:"IsNull"`
	IsPresent *bool `json:"IsPresent"`
	IsString  *bool `json:"IsString"`
	IsNumeric *bool `json:"IsNumeric"`
	IsBoolean *bool `json:"IsBoolean"`

	// Next state (top-level rules only)
	Next string `json:"Next"`
}

func parseDefinition(def string) (*aslDefinition, error) {
	var asl aslDefinition
	if err := json.Unmarshal([]byte(def), &asl); err != nil {
		return nil, err
	}
	if asl.StartAt == "" {
		return nil, fmt.Errorf("definition missing StartAt")
	}
	if len(asl.States) == 0 {
		return nil, fmt.Errorf("definition has no States")
	}
	return &asl, nil
}

// jsonPath extracts the value at a JSONPath reference (e.g. "$" or "$.field.sub")
// from src. pathRaw is the raw JSON value of the path field (quoted string or absent).
func jsonPath(src json.RawMessage, pathRaw json.RawMessage) (json.RawMessage, error) {
	if len(pathRaw) == 0 {
		return src, nil
	}
	var path string
	if err := json.Unmarshal(pathRaw, &path); err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}
	return jsonPathStr(src, path)
}

func jsonPathStr(src json.RawMessage, path string) (json.RawMessage, error) {
	if path == "$" {
		return src, nil
	}
	if !strings.HasPrefix(path, "$.") {
		return nil, fmt.Errorf("unsupported path expression: %s", path)
	}
	return getNestedPath(src, path[2:])
}

func getNestedPath(src json.RawMessage, dotPath string) (json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(src, &m); err != nil {
		return nil, fmt.Errorf("cannot traverse non-object with path .%s", dotPath)
	}
	parts := strings.SplitN(dotPath, ".", 2)
	val, ok := m[parts[0]]
	if !ok {
		return json.RawMessage("null"), nil
	}
	if len(parts) == 1 {
		return val, nil
	}
	return getNestedPath(val, parts[1])
}

// applyResultPath merges result into input according to the ResultPath reference.
//
//	absent (nil)   → output = result (default "$" behaviour)
//	JSON null      → output = input  (discard result)
//	"$"            → output = result
//	"$.field"      → output = input with input.field = result
func applyResultPath(input, result json.RawMessage, resultPath json.RawMessage) (json.RawMessage, error) {
	if string(resultPath) == "null" {
		return input, nil
	}
	if len(resultPath) == 0 {
		return result, nil
	}
	var path string
	if err := json.Unmarshal(resultPath, &path); err != nil {
		return nil, fmt.Errorf("invalid ResultPath: %w", err)
	}
	if path == "$" {
		return result, nil
	}
	if !strings.HasPrefix(path, "$.") {
		return nil, fmt.Errorf("unsupported ResultPath: %s", path)
	}
	return setNestedPath(input, path[2:], result)
}

func setNestedPath(obj json.RawMessage, dotPath string, value json.RawMessage) (json.RawMessage, error) {
	var m map[string]json.RawMessage
	if len(obj) == 0 || string(obj) == "null" {
		m = map[string]json.RawMessage{}
	} else if err := json.Unmarshal(obj, &m); err != nil {
		return nil, fmt.Errorf("ResultPath target is not an object: %w", err)
	}
	parts := strings.SplitN(dotPath, ".", 2)
	if len(parts) == 1 {
		m[parts[0]] = value
	} else {
		nested, err := setNestedPath(m[parts[0]], parts[1], value)
		if err != nil {
			return nil, err
		}
		m[parts[0]] = nested
	}
	return json.Marshal(m)
}

// evalChoiceRule evaluates a single choice rule (or combinator) against the state input.
func evalChoiceRule(rule choiceRule, input json.RawMessage) (bool, error) {
	// Combinators
	if len(rule.And) > 0 {
		for _, sub := range rule.And {
			ok, err := evalChoiceRule(sub, input)
			if err != nil || !ok {
				return false, err
			}
		}
		return true, nil
	}
	if len(rule.Or) > 0 {
		for _, sub := range rule.Or {
			ok, err := evalChoiceRule(sub, input)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil
	}
	if rule.Not != nil {
		ok, err := evalChoiceRule(*rule.Not, input)
		return !ok, err
	}

	// Extract variable value
	val, err := jsonPathStr(input, rule.Variable)
	if err != nil {
		return false, fmt.Errorf("Variable %q: %w", rule.Variable, err)
	}

	// IsPresent / IsNull
	if rule.IsPresent != nil {
		return (string(val) != "null") == *rule.IsPresent, nil
	}
	if rule.IsNull != nil {
		return (string(val) == "null") == *rule.IsNull, nil
	}

	// Type checks
	if rule.IsString != nil {
		_, err := strconv.Unquote(string(val))
		return (err == nil) == *rule.IsString, nil
	}
	if rule.IsNumeric != nil {
		_, err := strconv.ParseFloat(string(val), 64)
		return (err == nil) == *rule.IsNumeric, nil
	}
	if rule.IsBoolean != nil {
		isBool := string(val) == "true" || string(val) == "false"
		return isBool == *rule.IsBoolean, nil
	}

	// String comparisons
	if rule.StringEquals != nil || rule.StringLessThan != nil || rule.StringGreaterThan != nil ||
		rule.StringLessThanEquals != nil || rule.StringGreaterThanEquals != nil || rule.StringMatches != nil {
		var s string
		if err := json.Unmarshal(val, &s); err != nil {
			return false, nil // type mismatch → rule does not match
		}
		if rule.StringEquals != nil {
			return s == *rule.StringEquals, nil
		}
		if rule.StringLessThan != nil {
			return s < *rule.StringLessThan, nil
		}
		if rule.StringGreaterThan != nil {
			return s > *rule.StringGreaterThan, nil
		}
		if rule.StringLessThanEquals != nil {
			return s <= *rule.StringLessThanEquals, nil
		}
		if rule.StringGreaterThanEquals != nil {
			return s >= *rule.StringGreaterThanEquals, nil
		}
		if rule.StringMatches != nil {
			return matchWildcard(*rule.StringMatches, s), nil
		}
	}

	// Numeric comparisons
	if rule.NumericEquals != nil || rule.NumericLessThan != nil || rule.NumericGreaterThan != nil ||
		rule.NumericLessThanEquals != nil || rule.NumericGreaterThanEquals != nil {
		var n float64
		if err := json.Unmarshal(val, &n); err != nil {
			return false, nil
		}
		if rule.NumericEquals != nil {
			return n == *rule.NumericEquals, nil
		}
		if rule.NumericLessThan != nil {
			return n < *rule.NumericLessThan, nil
		}
		if rule.NumericGreaterThan != nil {
			return n > *rule.NumericGreaterThan, nil
		}
		if rule.NumericLessThanEquals != nil {
			return n <= *rule.NumericLessThanEquals, nil
		}
		if rule.NumericGreaterThanEquals != nil {
			return n >= *rule.NumericGreaterThanEquals, nil
		}
	}

	// Boolean
	if rule.BooleanEquals != nil {
		var b bool
		if err := json.Unmarshal(val, &b); err != nil {
			return false, nil
		}
		return b == *rule.BooleanEquals, nil
	}

	return false, fmt.Errorf("choice rule has no comparison operator")
}

// matchWildcard implements ASL StringMatches: only '*' wildcards are supported.
func matchWildcard(pattern, s string) bool {
	// Split on '*' and match each segment in order
	parts := strings.Split(pattern, "*")
	pos := 0
	for i, part := range parts {
		if i == 0 {
			if !strings.HasPrefix(s[pos:], part) {
				return false
			}
			pos += len(part)
		} else if i == len(parts)-1 {
			return strings.HasSuffix(s, part)
		} else {
			idx := strings.Index(s[pos:], part)
			if idx < 0 {
				return false
			}
			pos += idx + len(part)
		}
	}
	return pos == len(s)
}

// waitDuration computes the duration for a Wait state.
func waitDuration(state *aslState, input json.RawMessage) (time.Duration, error) {
	if state.Seconds > 0 {
		return time.Duration(state.Seconds) * time.Second, nil
	}
	if state.SecondsPath != "" {
		val, err := jsonPathStr(input, state.SecondsPath)
		if err != nil {
			return 0, err
		}
		var n float64
		if err := json.Unmarshal(val, &n); err != nil {
			return 0, fmt.Errorf("SecondsPath value is not a number")
		}
		return time.Duration(n * float64(time.Second)), nil
	}
	if state.Timestamp != "" {
		t, err := time.Parse(time.RFC3339, state.Timestamp)
		if err != nil {
			return 0, fmt.Errorf("invalid Timestamp: %w", err)
		}
		if d := time.Until(t); d > 0 {
			return d, nil
		}
		return 0, nil
	}
	if state.TimestampPath != "" {
		val, err := jsonPathStr(input, state.TimestampPath)
		if err != nil {
			return 0, err
		}
		var ts string
		if err := json.Unmarshal(val, &ts); err != nil {
			return 0, fmt.Errorf("TimestampPath value is not a string")
		}
		t, err := time.Parse(time.RFC3339, ts)
		if err != nil {
			return 0, fmt.Errorf("invalid timestamp: %w", err)
		}
		if d := time.Until(t); d > 0 {
			return d, nil
		}
		return 0, nil
	}
	return 0, fmt.Errorf("Wait state has no duration field")
}
