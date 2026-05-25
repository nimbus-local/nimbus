package sfn

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
	"github.com/nimbus-local/nimbus/internal/uid"
)

// Service implements the AWS Step Functions emulator.
// State machines and executions are stored in-memory. Accepts any credentials — local dev tool.
type Service struct {
	mu            sync.RWMutex
	stateMachines map[string]*stateMachine // keyed by ARN
	executions    map[string]*execution    // keyed by execution ARN
	region        string
	account       string
	nimbusBaseURL string // base URL for calling other Nimbus services (e.g. Lambda)
}

type stateMachine struct {
	name       string
	arn        string
	definition string // raw ASL JSON
	roleARN    string
	smType     string // STANDARD or EXPRESS
	tags       map[string]string
	createdAt  time.Time
	updatedAt  time.Time
}

const defaultAccount = "000000000000"

func New(region, nimbusBaseURL string) *Service {
	if region == "" {
		region = "us-east-1"
	}
	return &Service{
		stateMachines: map[string]*stateMachine{},
		executions:    map[string]*execution{},
		region:        region,
		account:       defaultAccount,
		nimbusBaseURL: nimbusBaseURL,
	}
}

func (s *Service) Name() string { return "sfn" }

func (s *Service) Detect(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), "AWSStepFunctions.")
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	target := r.Header.Get("X-Amz-Target")
	op := ""
	if idx := strings.LastIndex(target, "."); idx != -1 {
		op = target[idx+1:]
	}

	switch op {
	case "CreateStateMachine":
		s.createStateMachine(w, r)
	case "DescribeStateMachine":
		s.describeStateMachine(w, r)
	case "UpdateStateMachine":
		s.updateStateMachine(w, r)
	case "DeleteStateMachine":
		s.deleteStateMachine(w, r)
	case "ListStateMachines":
		s.listStateMachines(w, r)
	case "TagResource":
		s.tagResource(w, r)
	case "UntagResource":
		s.untagResource(w, r)
	case "ListTagsForResource":
		s.listTagsForResource(w, r)
	case "StartExecution":
		s.startExecution(w, r)
	case "DescribeExecution":
		s.describeExecution(w, r)
	case "GetExecutionHistory":
		s.getExecutionHistory(w, r)
	case "StopExecution":
		s.stopExecution(w, r)
	case "ValidateStateMachineDefinition":
		s.validateStateMachineDefinition(w, r)
	default:
		jsonhttp.Error(w, http.StatusBadRequest, "InvalidAction",
			fmt.Sprintf("Operation %s is not supported.", op))
	}
}

// --- ARN helpers ---

func (s *Service) smARN(name string) string {
	return fmt.Sprintf("arn:aws:states:%s:%s:stateMachine:%s", s.region, s.account, name)
}

// --- Operations ---

func (s *Service) createStateMachine(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name       string              `json:"name"`
		Definition string              `json:"definition"`
		RoleArn    string              `json:"roleArn"`
		Type       string              `json:"type"`
		Tags       []map[string]string `json:"tags"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Name == "" {
		jsonhttp.Error(w, http.StatusBadRequest, "InvalidName", "State machine name is required.")
		return
	}
	if req.Definition == "" {
		jsonhttp.Error(w, http.StatusBadRequest, "InvalidDefinition", "State machine definition is required.")
		return
	}
	if !json.Valid([]byte(req.Definition)) {
		jsonhttp.Error(w, http.StatusBadRequest, "InvalidDefinition", "Definition must be valid JSON.")
		return
	}
	smType := req.Type
	if smType == "" {
		smType = "STANDARD"
	}

	arn := s.smARN(req.Name)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.stateMachines[arn]; exists {
		jsonhttp.Error(w, http.StatusBadRequest, "StateMachineAlreadyExists",
			fmt.Sprintf("State machine already exists: %s", arn))
		return
	}

	tags := map[string]string{}
	for _, kv := range req.Tags {
		if k, ok := kv["key"]; ok {
			tags[k] = kv["value"]
		}
	}

	now := time.Now().UTC()
	sm := &stateMachine{
		name:       req.Name,
		arn:        arn,
		definition: req.Definition,
		roleARN:    req.RoleArn,
		smType:     smType,
		tags:       tags,
		createdAt:  now,
		updatedAt:  now,
	}
	s.stateMachines[arn] = sm

	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{
		"stateMachineArn": arn,
		"creationDate":    now.Unix(),
	})
}

func (s *Service) describeStateMachine(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StateMachineArn string `json:"stateMachineArn"`
	}
	if !decode(w, r, &req) {
		return
	}

	sm := s.findSM(req.StateMachineArn)
	if sm == nil {
		jsonhttp.Error(w, http.StatusBadRequest, "StateMachineDoesNotExist",
			fmt.Sprintf("State machine does not exist: %s", req.StateMachineArn))
		return
	}

	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{
		"stateMachineArn": sm.arn,
		"name":            sm.name,
		"definition":      sm.definition,
		"roleArn":         sm.roleARN,
		"type":            sm.smType,
		"status":          "ACTIVE",
		"creationDate":    sm.createdAt.Unix(),
	})
}

func (s *Service) updateStateMachine(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StateMachineArn string `json:"stateMachineArn"`
		Definition      string `json:"definition"`
		RoleArn         string `json:"roleArn"`
	}
	if !decode(w, r, &req) {
		return
	}

	sm := s.findSM(req.StateMachineArn)
	if sm == nil {
		jsonhttp.Error(w, http.StatusBadRequest, "StateMachineDoesNotExist",
			fmt.Sprintf("State machine does not exist: %s", req.StateMachineArn))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if req.Definition != "" {
		if !json.Valid([]byte(req.Definition)) {
			jsonhttp.Error(w, http.StatusBadRequest, "InvalidDefinition", "Definition must be valid JSON.")
			return
		}
		sm.definition = req.Definition
	}
	if req.RoleArn != "" {
		sm.roleARN = req.RoleArn
	}
	sm.updatedAt = time.Now().UTC()

	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{
		"stateMachineArn": sm.arn,
		"updateDate":      sm.updatedAt.Unix(),
	})
}

func (s *Service) deleteStateMachine(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StateMachineArn string `json:"stateMachineArn"`
	}
	if !decode(w, r, &req) {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.stateMachines[req.StateMachineArn]; !exists {
		// AWS returns success even if the state machine doesn't exist
		jsonhttp.Write(w, http.StatusOK, map[string]interface{}{})
		return
	}

	delete(s.stateMachines, req.StateMachineArn)
	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) listStateMachines(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	type smEntry struct {
		StateMachineArn string `json:"stateMachineArn"`
		Name            string `json:"name"`
		Type            string `json:"type"`
		CreationDate    int64  `json:"creationDate"`
	}

	var entries []smEntry
	for _, sm := range s.stateMachines {
		entries = append(entries, smEntry{
			StateMachineArn: sm.arn,
			Name:            sm.name,
			Type:            sm.smType,
			CreationDate:    sm.createdAt.Unix(),
		})
	}
	if entries == nil {
		entries = []smEntry{}
	}

	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{
		"stateMachines": entries,
	})
}

func (s *Service) tagResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceArn string              `json:"resourceArn"`
		Tags        []map[string]string `json:"tags"`
	}
	if !decode(w, r, &req) {
		return
	}

	sm := s.findSM(req.ResourceArn)
	if sm == nil {
		jsonhttp.Error(w, http.StatusBadRequest, "ResourceNotFound",
			fmt.Sprintf("Resource not found: %s", req.ResourceArn))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, kv := range req.Tags {
		if k, ok := kv["key"]; ok {
			sm.tags[k] = kv["value"]
		}
	}

	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) untagResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceArn string   `json:"resourceArn"`
		TagKeys     []string `json:"tagKeys"`
	}
	if !decode(w, r, &req) {
		return
	}

	sm := s.findSM(req.ResourceArn)
	if sm == nil {
		jsonhttp.Error(w, http.StatusBadRequest, "ResourceNotFound",
			fmt.Sprintf("Resource not found: %s", req.ResourceArn))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, k := range req.TagKeys {
		delete(sm.tags, k)
	}

	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceArn string `json:"resourceArn"`
	}
	if !decode(w, r, &req) {
		return
	}

	sm := s.findSM(req.ResourceArn)
	if sm == nil {
		jsonhttp.Error(w, http.StatusBadRequest, "ResourceNotFound",
			fmt.Sprintf("Resource not found: %s", req.ResourceArn))
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	type kv struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	var tags []kv
	for k, v := range sm.tags {
		tags = append(tags, kv{Key: k, Value: v})
	}
	if tags == nil {
		tags = []kv{}
	}

	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{
		"tags": tags,
	})
}

// --- Execution handlers ---

func (s *Service) startExecution(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StateMachineArn string `json:"stateMachineArn"`
		Name            string `json:"name"`
		Input           string `json:"input"`
	}
	if !decode(w, r, &req) {
		return
	}

	sm := s.findSM(req.StateMachineArn)
	if sm == nil {
		jsonhttp.Error(w, http.StatusBadRequest, "StateMachineDoesNotExist",
			fmt.Sprintf("State machine does not exist: %s", req.StateMachineArn))
		return
	}

	name := req.Name
	if name == "" {
		name = uid.New()
	}
	input := req.Input
	if input == "" {
		input = "{}"
	}

	execArn := s.execARN(sm.name, name)
	now := time.Now().UTC()
	ctx, cancel := context.WithCancel(context.Background())

	exec := &execution{
		ctx:       ctx,
		cancel:    cancel,
		name:      name,
		arn:       execArn,
		smARN:     sm.arn,
		smName:    sm.name,
		input:     input,
		status:    "RUNNING",
		startedAt: now,
	}

	s.mu.Lock()
	s.executions[execArn] = exec
	s.mu.Unlock()

	go s.runExecution(exec, sm)

	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{
		"executionArn": execArn,
		"startDate":    float64(now.UnixNano()) / 1e9,
	})
}

func (s *Service) describeExecution(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ExecutionArn string `json:"executionArn"`
	}
	if !decode(w, r, &req) {
		return
	}

	exec := s.findExec(req.ExecutionArn)
	if exec == nil {
		jsonhttp.Error(w, http.StatusBadRequest, "ExecutionDoesNotExist",
			fmt.Sprintf("Execution does not exist: %s", req.ExecutionArn))
		return
	}

	exec.mu.Lock()
	resp := map[string]interface{}{
		"executionArn":    exec.arn,
		"stateMachineArn": exec.smARN,
		"name":            exec.name,
		"status":          exec.status,
		"startDate":       float64(exec.startedAt.UnixNano()) / 1e9,
		"input":           exec.input,
	}
	if exec.stoppedAt != nil {
		resp["stopDate"] = float64(exec.stoppedAt.UnixNano()) / 1e9
	}
	if exec.output != "" {
		resp["output"] = exec.output
	}
	if exec.errCode != "" {
		resp["error"] = exec.errCode
		resp["cause"] = exec.cause
	}
	exec.mu.Unlock()

	jsonhttp.Write(w, http.StatusOK, resp)
}

func (s *Service) getExecutionHistory(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ExecutionArn string `json:"executionArn"`
	}
	if !decode(w, r, &req) {
		return
	}

	exec := s.findExec(req.ExecutionArn)
	if exec == nil {
		jsonhttp.Error(w, http.StatusBadRequest, "ExecutionDoesNotExist",
			fmt.Sprintf("Execution does not exist: %s", req.ExecutionArn))
		return
	}

	exec.mu.Lock()
	events := make([]historyEvent, len(exec.history))
	copy(events, exec.history)
	exec.mu.Unlock()

	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{
		"events": events,
	})
}

func (s *Service) stopExecution(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ExecutionArn string `json:"executionArn"`
		Error        string `json:"error"`
		Cause        string `json:"cause"`
	}
	if !decode(w, r, &req) {
		return
	}

	exec := s.findExec(req.ExecutionArn)
	if exec == nil {
		jsonhttp.Error(w, http.StatusBadRequest, "ExecutionDoesNotExist",
			fmt.Sprintf("Execution does not exist: %s", req.ExecutionArn))
		return
	}

	if !exec.abort(req.Error, req.Cause) {
		jsonhttp.Error(w, http.StatusBadRequest, "ExecutionNotRunning",
			"Execution is not in a running state.")
		return
	}
	exec.cancel()

	exec.mu.Lock()
	stopDate := float64(exec.stoppedAt.UnixNano()) / 1e9
	exec.mu.Unlock()

	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{
		"stopDate": stopDate,
	})
}

func (s *Service) validateStateMachineDefinition(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Definition string `json:"definition"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Definition != "" && !json.Valid([]byte(req.Definition)) {
		jsonhttp.Write(w, http.StatusOK, map[string]interface{}{
			"result":      "FAIL",
			"diagnostics": []map[string]interface{}{{"message": "Definition is not valid JSON", "code": "SCHEMA_VALIDATION_FAILED"}},
		})
		return
	}
	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{
		"result":      "OK",
		"diagnostics": []interface{}{},
	})
}

// --- Helpers ---

// findSM looks up a state machine by ARN or by name-derived ARN.
func (s *Service) findSM(arnOrName string) *stateMachine {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if sm, ok := s.stateMachines[arnOrName]; ok {
		return sm
	}
	derived := s.smARN(arnOrName)
	if sm, ok := s.stateMachines[derived]; ok {
		return sm
	}
	return nil
}

// findExec looks up an execution by ARN.
func (s *Service) findExec(arn string) *execution {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.executions[arn]
}

func decode(w http.ResponseWriter, r *http.Request, v interface{}) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		jsonhttp.Error(w, http.StatusBadRequest, "InvalidRequest",
			fmt.Sprintf("Could not parse request body: %s", err.Error()))
		return false
	}
	return true
}

func (s *Service) execARN(smName, execName string) string {
	return fmt.Sprintf("arn:aws:states:%s:%s:execution:%s:%s", s.region, s.account, smName, execName)
}
