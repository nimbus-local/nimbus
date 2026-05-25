package sfn

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// historyEvent is a JSON-serialisable map so extra detail keys merge naturally.
type historyEvent = map[string]interface{}

type execution struct {
	mu          sync.Mutex
	name        string
	arn         string
	smARN       string
	smName      string
	input       string
	output      string // set when SUCCEEDED
	status      string // RUNNING | SUCCEEDED | FAILED | ABORTED
	startedAt   time.Time
	stoppedAt   *time.Time
	errCode     string
	cause       string
	history     []historyEvent
	nextEventID int64
}

func (e *execution) addEvent(evtType string, extra map[string]interface{}) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nextEventID++
	evt := historyEvent{
		"id":        e.nextEventID,
		"timestamp": float64(time.Now().UnixNano()) / 1e9,
		"type":      evtType,
	}
	for k, v := range extra {
		evt[k] = v
	}
	e.history = append(e.history, evt)
}

func (e *execution) succeed(output string) {
	now := time.Now().UTC()
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nextEventID++
	evt := historyEvent{
		"id":        e.nextEventID,
		"timestamp": float64(now.UnixNano()) / 1e9,
		"type":      "ExecutionSucceeded",
		"executionSucceededEventDetails": map[string]interface{}{
			"output":        output,
			"outputDetails": map[string]interface{}{"included": true, "truncated": false},
		},
	}
	e.history = append(e.history, evt)
	e.output = output
	e.status = "SUCCEEDED"
	e.stoppedAt = &now
}

func (e *execution) failExec(errCode, cause string) {
	now := time.Now().UTC()
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nextEventID++
	evt := historyEvent{
		"id":        e.nextEventID,
		"timestamp": float64(now.UnixNano()) / 1e9,
		"type":      "ExecutionFailed",
		"executionFailedEventDetails": map[string]interface{}{
			"error": errCode,
			"cause": cause,
		},
	}
	e.history = append(e.history, evt)
	e.errCode = errCode
	e.cause = cause
	e.status = "FAILED"
	e.stoppedAt = &now
}

func (s *Service) runExecution(exec *execution, sm *stateMachine) {
	asl, err := parseDefinition(sm.definition)
	if err != nil {
		exec.failExec("States.Runtime", fmt.Sprintf("invalid definition: %s", err))
		return
	}

	exec.addEvent("ExecutionStarted", map[string]interface{}{
		"executionStartedEventDetails": map[string]interface{}{
			"input":        exec.input,
			"inputDetails": map[string]interface{}{"included": true, "truncated": false},
		},
	})

	current := json.RawMessage(exec.input)
	stateName := asl.StartAt

	for {
		state, ok := asl.States[stateName]
		if !ok {
			exec.failExec("States.Runtime", fmt.Sprintf("state not found: %s", stateName))
			return
		}

		exec.addEvent(enteredEvent(state.Type), map[string]interface{}{
			"stateEnteredEventDetails": map[string]interface{}{
				"name":         stateName,
				"input":        string(current),
				"inputDetails": map[string]interface{}{"included": true, "truncated": false},
			},
		})

		switch state.Type {
		case "Pass":
			output, next, end, err := runPass(state, current)
			if err != nil {
				exec.failExec("States.Runtime", err.Error())
				return
			}
			exec.addEvent("PassStateExited", map[string]interface{}{
				"stateExitedEventDetails": map[string]interface{}{
					"name":          stateName,
					"output":        string(output),
					"outputDetails": map[string]interface{}{"included": true, "truncated": false},
				},
			})
			if end {
				exec.succeed(string(output))
				return
			}
			current = output
			stateName = next

		case "Succeed":
			exec.addEvent("SucceedStateExited", map[string]interface{}{
				"stateExitedEventDetails": map[string]interface{}{
					"name":          stateName,
					"output":        string(current),
					"outputDetails": map[string]interface{}{"included": true, "truncated": false},
				},
			})
			exec.succeed(string(current))
			return

		case "Fail":
			exec.failExec(state.Error, state.Cause)
			return

		default:
			exec.failExec("States.Runtime", fmt.Sprintf("unsupported state type in Part 2: %s", state.Type))
			return
		}
	}
}

// runPass applies InputPath → Result/ResultPath → OutputPath and returns the
// output, next state name, end flag, and any error.
func runPass(state *aslState, input json.RawMessage) (output json.RawMessage, next string, end bool, err error) {
	// 1. InputPath
	effective, err := jsonPath(input, state.InputPath)
	if err != nil {
		return nil, "", false, fmt.Errorf("InputPath: %w", err)
	}

	// 2. Result / ResultPath
	result := effective
	if len(state.Result) > 0 {
		result = state.Result
	}
	merged, err := applyResultPath(effective, result, state.ResultPath)
	if err != nil {
		return nil, "", false, fmt.Errorf("ResultPath: %w", err)
	}

	// 3. OutputPath
	final, err := jsonPath(merged, state.OutputPath)
	if err != nil {
		return nil, "", false, fmt.Errorf("OutputPath: %w", err)
	}

	return final, state.Next, state.End, nil
}

func enteredEvent(stateType string) string {
	switch stateType {
	case "Pass":
		return "PassStateEntered"
	case "Succeed":
		return "SucceedStateEntered"
	case "Fail":
		return "FailStateEntered"
	default:
		return stateType + "StateEntered"
	}
}
