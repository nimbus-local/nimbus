package sfn

import (
	"encoding/json"
	"fmt"
	"strings"
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

	// Routing
	Next string `json:"Next"`
	End  bool   `json:"End"`
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
