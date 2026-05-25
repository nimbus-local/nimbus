package sfn

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// historyEvent is a JSON-serialisable map so extra detail keys merge naturally.
type historyEvent = map[string]interface{}

type execution struct {
	mu          sync.Mutex
	cancel      context.CancelFunc
	ctx         context.Context
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
	e.history = append(e.history, historyEvent{
		"id":        e.nextEventID,
		"timestamp": float64(now.UnixNano()) / 1e9,
		"type":      "ExecutionSucceeded",
		"executionSucceededEventDetails": map[string]interface{}{
			"output":        output,
			"outputDetails": map[string]interface{}{"included": true, "truncated": false},
		},
	})
	e.output = output
	e.status = "SUCCEEDED"
	e.stoppedAt = &now
}

func (e *execution) failExec(errCode, cause string) {
	now := time.Now().UTC()
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nextEventID++
	e.history = append(e.history, historyEvent{
		"id":        e.nextEventID,
		"timestamp": float64(now.UnixNano()) / 1e9,
		"type":      "ExecutionFailed",
		"executionFailedEventDetails": map[string]interface{}{
			"error": errCode,
			"cause": cause,
		},
	})
	e.errCode = errCode
	e.cause = cause
	e.status = "FAILED"
	e.stoppedAt = &now
}

// abort is called by StopExecution — sets ABORTED and records the event.
func (e *execution) abort(errCode, cause string) bool {
	now := time.Now().UTC()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.status != "RUNNING" {
		return false
	}
	e.nextEventID++
	e.history = append(e.history, historyEvent{
		"id":        e.nextEventID,
		"timestamp": float64(now.UnixNano()) / 1e9,
		"type":      "ExecutionAborted",
		"executionAbortedEventDetails": map[string]interface{}{
			"error": errCode,
			"cause": cause,
		},
	})
	e.errCode = errCode
	e.cause = cause
	e.status = "ABORTED"
	e.stoppedAt = &now
	return true
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
		// Check if StopExecution was called between states.
		select {
		case <-exec.ctx.Done():
			return
		default:
		}

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

		case "Choice":
			next, err := runChoice(state, current)
			if err != nil {
				exec.failExec("States.NoChoiceMatched", err.Error())
				return
			}
			exec.addEvent("ChoiceStateExited", map[string]interface{}{
				"stateExitedEventDetails": map[string]interface{}{
					"name":          stateName,
					"output":        string(current),
					"outputDetails": map[string]interface{}{"included": true, "truncated": false},
				},
			})
			stateName = next

		case "Wait":
			dur, err := waitDuration(state, current)
			if err != nil {
				exec.failExec("States.Runtime", err.Error())
				return
			}
			select {
			case <-time.After(dur):
			case <-exec.ctx.Done():
				return
			}
			exec.addEvent("WaitStateExited", map[string]interface{}{
				"stateExitedEventDetails": map[string]interface{}{
					"name":          stateName,
					"output":        string(current),
					"outputDetails": map[string]interface{}{"included": true, "truncated": false},
				},
			})
			if state.End {
				exec.succeed(string(current))
				return
			}
			stateName = state.Next

		case "Task":
			output, next, end, taskErr, taskCause := s.runTask(exec.ctx, state, current)
			if taskErr != "" {
				if catchNext, catchInput := applyCatch(state.Catch, taskErr, taskCause, current); catchNext != "" {
					exec.addEvent("TaskStateExited", exitedDetails(stateName, catchInput))
					current = catchInput
					stateName = catchNext
					continue
				}
				exec.failExec(taskErr, taskCause)
				return
			}
			exec.addEvent("TaskStateExited", exitedDetails(stateName, output))
			if end {
				exec.succeed(string(output))
				return
			}
			current = output
			stateName = next

		case "Parallel":
			output, next, end, taskErr, taskCause := s.runParallelState(exec.ctx, state, current)
			if taskErr != "" {
				if catchNext, catchInput := applyCatch(state.Catch, taskErr, taskCause, current); catchNext != "" {
					exec.addEvent("ParallelStateExited", exitedDetails(stateName, catchInput))
					current = catchInput
					stateName = catchNext
					continue
				}
				exec.failExec(taskErr, taskCause)
				return
			}
			exec.addEvent("ParallelStateExited", exitedDetails(stateName, output))
			if end {
				exec.succeed(string(output))
				return
			}
			current = output
			stateName = next

		case "Map":
			output, next, end, taskErr, taskCause := s.runMapState(exec.ctx, state, current)
			if taskErr != "" {
				if catchNext, catchInput := applyCatch(state.Catch, taskErr, taskCause, current); catchNext != "" {
					exec.addEvent("MapStateExited", exitedDetails(stateName, catchInput))
					current = catchInput
					stateName = catchNext
					continue
				}
				exec.failExec(taskErr, taskCause)
				return
			}
			exec.addEvent("MapStateExited", exitedDetails(stateName, output))
			if end {
				exec.succeed(string(output))
				return
			}
			current = output
			stateName = next

		default:
			exec.failExec("States.Runtime", fmt.Sprintf("unsupported state type: %s", state.Type))
			return
		}
	}
}

// runChoice evaluates Choices in order and returns the next state name.
func runChoice(state *aslState, input json.RawMessage) (string, error) {
	for _, rule := range state.Choices {
		ok, err := evalChoiceRule(rule, input)
		if err != nil {
			return "", err
		}
		if ok {
			return rule.Next, nil
		}
	}
	if state.Default != "" {
		return state.Default, nil
	}
	return "", fmt.Errorf("no choice rule matched and no Default defined")
}

// runPass applies InputPath → Result/ResultPath → OutputPath.
func runPass(state *aslState, input json.RawMessage) (output json.RawMessage, next string, end bool, err error) {
	effective, err := jsonPath(input, state.InputPath)
	if err != nil {
		return nil, "", false, fmt.Errorf("InputPath: %w", err)
	}
	result := effective
	if len(state.Result) > 0 {
		result = state.Result
	}
	merged, err := applyResultPath(effective, result, state.ResultPath)
	if err != nil {
		return nil, "", false, fmt.Errorf("ResultPath: %w", err)
	}
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
	case "Wait":
		return "WaitStateEntered"
	case "Choice":
		return "ChoiceStateEntered"
	case "Task":
		return "TaskStateEntered"
	case "Parallel":
		return "ParallelStateEntered"
	case "Map":
		return "MapStateEntered"
	default:
		return stateType + "StateEntered"
	}
}

// exitedDetails builds the stateExitedEventDetails map for addEvent calls.
func exitedDetails(name string, output json.RawMessage) map[string]interface{} {
	return map[string]interface{}{
		"stateExitedEventDetails": map[string]interface{}{
			"name":          name,
			"output":        string(output),
			"outputDetails": map[string]interface{}{"included": true, "truncated": false},
		},
	}
}

// runParallelState executes all Branches concurrently and returns their outputs as a JSON array.
func (s *Service) runParallelState(ctx context.Context, state *aslState, input json.RawMessage) (output json.RawMessage, next string, end bool, errCode, cause string) {
	effective, err := jsonPath(input, state.InputPath)
	if err != nil {
		return nil, "", false, "States.Runtime", fmt.Sprintf("InputPath: %s", err)
	}

	type branchResult struct {
		idx int
		out json.RawMessage
		err error
	}

	branchCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	ch := make(chan branchResult, len(state.Branches))
	for i, branch := range state.Branches {
		i, branch := i, branch
		go func() {
			out, err := s.runBranch(branchCtx, &branch, effective)
			ch <- branchResult{idx: i, out: out, err: err}
		}()
	}

	results := make([]json.RawMessage, len(state.Branches))
	for range state.Branches {
		r := <-ch
		if r.err != nil {
			cancel() // abort remaining branches
			return nil, "", false, "States.BranchFailed", r.err.Error()
		}
		results[r.idx] = r.out
	}

	arr, err := json.Marshal(results)
	if err != nil {
		return nil, "", false, "States.Runtime", err.Error()
	}
	merged, err := applyResultPath(input, json.RawMessage(arr), state.ResultPath)
	if err != nil {
		return nil, "", false, "States.Runtime", fmt.Sprintf("ResultPath: %s", err)
	}
	final, err := jsonPath(merged, state.OutputPath)
	if err != nil {
		return nil, "", false, "States.Runtime", fmt.Sprintf("OutputPath: %s", err)
	}
	return final, state.Next, state.End, "", ""
}

// runMapState iterates over the items array and runs the Iterator for each item.
func (s *Service) runMapState(ctx context.Context, state *aslState, input json.RawMessage) (output json.RawMessage, next string, end bool, errCode, cause string) {
	if state.Iterator == nil {
		return nil, "", false, "States.Runtime", "Map state has no Iterator"
	}

	// Resolve items array.
	itemsRaw, err := jsonPath(input, state.ItemsPath)
	if err != nil {
		return nil, "", false, "States.Runtime", fmt.Sprintf("ItemsPath: %s", err)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(itemsRaw, &items); err != nil {
		return nil, "", false, "States.Runtime", fmt.Sprintf("ItemsPath value is not an array: %s", err)
	}

	type itemResult struct {
		idx int
		out json.RawMessage
		err error
	}

	concurrency := state.MaxConcurrency
	if concurrency <= 0 {
		concurrency = len(items)
	}
	if concurrency == 0 {
		// Empty array — output is [].
		empty := json.RawMessage("[]")
		merged, err := applyResultPath(input, empty, state.ResultPath)
		if err != nil {
			return nil, "", false, "States.Runtime", fmt.Sprintf("ResultPath: %s", err)
		}
		final, err := jsonPath(merged, state.OutputPath)
		if err != nil {
			return nil, "", false, "States.Runtime", fmt.Sprintf("OutputPath: %s", err)
		}
		return final, state.Next, state.End, "", ""
	}

	mapCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	sem := make(chan struct{}, concurrency)
	ch := make(chan itemResult, len(items))

	for i, item := range items {
		i, item := i, item
		sem <- struct{}{}
		go func() {
			defer func() { <-sem }()
			out, err := s.runBranch(mapCtx, state.Iterator, item)
			ch <- itemResult{idx: i, out: out, err: err}
		}()
	}

	results := make([]json.RawMessage, len(items))
	for range items {
		r := <-ch
		if r.err != nil {
			cancel()
			return nil, "", false, "States.MapFailed", r.err.Error()
		}
		results[r.idx] = r.out
	}

	arr, err := json.Marshal(results)
	if err != nil {
		return nil, "", false, "States.Runtime", err.Error()
	}
	merged, err := applyResultPath(input, json.RawMessage(arr), state.ResultPath)
	if err != nil {
		return nil, "", false, "States.Runtime", fmt.Sprintf("ResultPath: %s", err)
	}
	final, err := jsonPath(merged, state.OutputPath)
	if err != nil {
		return nil, "", false, "States.Runtime", fmt.Sprintf("OutputPath: %s", err)
	}
	return final, state.Next, state.End, "", ""
}

// runBranch executes a sub-state machine (Parallel branch or Map iterator) and returns the output.
// Unlike runExecution it does not record history events — it is a pure compute helper.
func (s *Service) runBranch(ctx context.Context, asl *aslDefinition, input json.RawMessage) (json.RawMessage, error) {
	current := input
	stateName := asl.StartAt

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("execution aborted")
		default:
		}

		state, ok := asl.States[stateName]
		if !ok {
			return nil, fmt.Errorf("state not found: %s", stateName)
		}

		switch state.Type {
		case "Pass":
			output, next, end, err := runPass(state, current)
			if err != nil {
				return nil, err
			}
			if end {
				return output, nil
			}
			current = output
			stateName = next

		case "Succeed":
			return current, nil

		case "Fail":
			return nil, fmt.Errorf("%s: %s", state.Error, state.Cause)

		case "Choice":
			next, err := runChoice(state, current)
			if err != nil {
				return nil, err
			}
			stateName = next

		case "Wait":
			dur, err := waitDuration(state, current)
			if err != nil {
				return nil, err
			}
			select {
			case <-time.After(dur):
			case <-ctx.Done():
				return nil, fmt.Errorf("execution aborted")
			}
			if state.End {
				return current, nil
			}
			stateName = state.Next

		case "Task":
			output, next, end, errCode, cause := s.runTask(ctx, state, current)
			if errCode != "" {
				if catchNext, catchInput := applyCatch(state.Catch, errCode, cause, current); catchNext != "" {
					current = catchInput
					stateName = catchNext
					continue
				}
				return nil, fmt.Errorf("%s: %s", errCode, cause)
			}
			if end {
				return output, nil
			}
			current = output
			stateName = next

		case "Parallel":
			output, next, end, errCode, cause := s.runParallelState(ctx, state, current)
			if errCode != "" {
				if catchNext, catchInput := applyCatch(state.Catch, errCode, cause, current); catchNext != "" {
					current = catchInput
					stateName = catchNext
					continue
				}
				return nil, fmt.Errorf("%s: %s", errCode, cause)
			}
			if end {
				return output, nil
			}
			current = output
			stateName = next

		case "Map":
			output, next, end, errCode, cause := s.runMapState(ctx, state, current)
			if errCode != "" {
				if catchNext, catchInput := applyCatch(state.Catch, errCode, cause, current); catchNext != "" {
					current = catchInput
					stateName = catchNext
					continue
				}
				return nil, fmt.Errorf("%s: %s", errCode, cause)
			}
			if end {
				return output, nil
			}
			current = output
			stateName = next

		default:
			return nil, fmt.Errorf("unsupported state type: %s", state.Type)
		}
	}
}

// runTask invokes a Lambda function and returns (output, next, end, errCode, cause).
// errCode is non-empty on failure; retry logic is applied internally.
func (s *Service) runTask(ctx context.Context, state *aslState, input json.RawMessage) (output json.RawMessage, next string, end bool, errCode, cause string) {
	funcName := lambdaFuncName(state.Resource)
	if funcName == "" {
		return nil, "", false, "States.Runtime", fmt.Sprintf("unsupported Task resource: %s", state.Resource)
	}

	// Apply InputPath to get effective task input.
	effective, err := jsonPath(input, state.InputPath)
	if err != nil {
		return nil, "", false, "States.Runtime", fmt.Sprintf("InputPath: %s", err)
	}

	timeout := state.TimeoutSeconds
	if timeout <= 0 {
		timeout = 60
	}

	// Determine max attempts and retry configs.
	var lastErr, lastCause string
	maxAttempts := 1 // 1 = first attempt, no retries by default
	if len(state.Retry) > 0 {
		// Use the highest MaxAttempts across all retry configs as an upper bound.
		// (Real SFN applies per-error retry configs, but this covers the common case.)
		for _, rc := range state.Retry {
			ma := rc.MaxAttempts
			if ma <= 0 {
				ma = 3
			}
			if ma+1 > maxAttempts {
				maxAttempts = ma + 1
			}
		}
	}

	var attempt int
	for attempt = 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			// Compute backoff delay from matching retry config.
			delay := retryDelay(state.Retry, lastErr, attempt)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, "", false, "States.Runtime", "execution aborted"
			}
		}

		result, invokeErr, invokeCause := s.invokeLambda(ctx, funcName, effective, timeout)
		if invokeErr == "" {
			// Success — apply ResultPath and OutputPath.
			merged, err := applyResultPath(input, result, state.ResultPath)
			if err != nil {
				return nil, "", false, "States.Runtime", fmt.Sprintf("ResultPath: %s", err)
			}
			final, err := jsonPath(merged, state.OutputPath)
			if err != nil {
				return nil, "", false, "States.Runtime", fmt.Sprintf("OutputPath: %s", err)
			}
			return final, state.Next, state.End, "", ""
		}

		lastErr = invokeErr
		lastCause = invokeCause

		// Check if this error is retryable.
		if !isRetryable(state.Retry, lastErr, attempt) {
			break
		}
	}

	return nil, "", false, lastErr, lastCause
}

// invokeLambda calls the Lambda service via HTTP and returns (result, errCode, cause).
func (s *Service) invokeLambda(ctx context.Context, funcName string, payload json.RawMessage, timeoutSeconds int) (json.RawMessage, string, string) {
	if s.nimbusBaseURL == "" {
		return nil, "States.Runtime", "no Nimbus base URL configured for Task state"
	}

	url := fmt.Sprintf("%s/2015-03-31/functions/%s/invocations", s.nimbusBaseURL, funcName)

	body := payload
	if len(body) == 0 {
		body = json.RawMessage("null")
	}

	taskCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(taskCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, "States.Runtime", err.Error()
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Amz-Invocation-Type", "RequestResponse")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if taskCtx.Err() != nil {
			return nil, "States.Timeout", "Lambda invocation timed out"
		}
		return nil, "States.TaskFailed", err.Error()
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	// Lambda signals function errors with X-Amz-Function-Error regardless of HTTP status.
	if funcErr := resp.Header.Get("X-Amz-Function-Error"); funcErr != "" {
		return nil, "States.TaskFailed", string(respBody)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, "States.TaskFailed", string(respBody)
	}

	return json.RawMessage(respBody), "", ""
}

// lambdaFuncName extracts the function name from a Lambda ARN or plain name.
// Supports: "arn:aws:lambda:{region}:{account}:function:{name}" and bare "{name}".
func lambdaFuncName(resource string) string {
	const prefix = ":function:"
	if idx := strings.LastIndex(resource, prefix); idx >= 0 {
		return resource[idx+len(prefix):]
	}
	// If it's not an ARN, treat it as a plain function name.
	if !strings.HasPrefix(resource, "arn:") {
		return resource
	}
	return ""
}

// retryDelay returns the backoff delay for the given attempt number using the matching retry config.
func retryDelay(retries []retryConfig, errCode string, attempt int) time.Duration {
	for _, rc := range retries {
		if !errorMatches(rc.ErrorEquals, errCode) {
			continue
		}
		interval := rc.IntervalSeconds
		if interval <= 0 {
			interval = 1
		}
		rate := rc.BackoffRate
		if rate <= 0 {
			rate = 2.0
		}
		delay := float64(interval)
		for i := 1; i < attempt; i++ {
			delay *= rate
		}
		return time.Duration(delay * float64(time.Second))
	}
	return time.Second
}

// isRetryable checks whether the error matches any retry config that still has attempts left.
func isRetryable(retries []retryConfig, errCode string, attempt int) bool {
	for _, rc := range retries {
		if !errorMatches(rc.ErrorEquals, errCode) {
			continue
		}
		ma := rc.MaxAttempts
		if ma <= 0 {
			ma = 3
		}
		return attempt < ma
	}
	return false
}

// applyCatch checks Catch configs and returns (nextState, errorInput) or ("", nil) if no match.
func applyCatch(catches []catchConfig, errCode, cause string, input json.RawMessage) (string, json.RawMessage) {
	for _, c := range catches {
		if !errorMatches(c.ErrorEquals, errCode) {
			continue
		}
		errObj, _ := json.Marshal(map[string]string{"Error": errCode, "Cause": cause})
		merged, err := applyResultPath(input, json.RawMessage(errObj), c.ResultPath)
		if err != nil {
			merged = json.RawMessage(errObj)
		}
		return c.Next, merged
	}
	return "", nil
}

// errorMatches returns true if errCode matches any entry in the list (States.ALL matches everything).
func errorMatches(list []string, errCode string) bool {
	for _, e := range list {
		if e == "States.ALL" || e == errCode {
			return true
		}
	}
	return false
}
