package eventbridge

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nimbus-local/nimbus/internal/uid"
)

const accountID = "000000000000"

// Service implements the AWS EventBridge emulator.
// Events are captured in-memory and never routed to real targets.
// They are available for inspection via /_nimbus/eventbridge/events.
type Service struct {
	mu         sync.RWMutex
	events     []*CapturedEvent
	eventBuses map[string]*eventBus // name -> bus
	rules      map[string]*rule     // busName+"/"+ruleName -> rule
	targets    map[string][]*target // busName+"/"+ruleName -> targets
	region     string
}

type eventBus struct {
	Name string
	ARN  string
}

type rule struct {
	Name               string
	EventBusName       string
	State              string
	Description        string
	EventPattern       string
	ScheduleExpression string
	ARN                string
}

type target struct {
	ID  string
	ARN string
}

// CapturedEvent is an event that was "put" via PutEvents.
// Available at GET /_nimbus/eventbridge/events.
type CapturedEvent struct {
	EventID      string    `json:"EventId"`
	EventBusName string    `json:"EventBusName"`
	Source       string    `json:"Source"`
	DetailType   string    `json:"DetailType"`
	Detail       string    `json:"Detail"`
	Time         time.Time `json:"Time"`
	Resources    []string  `json:"Resources,omitempty"`
}

func New(region string) *Service {
	if region == "" {
		region = "us-east-1"
	}
	svc := &Service{
		region:     region,
		eventBuses: map[string]*eventBus{},
		rules:      map[string]*rule{},
		targets:    map[string][]*target{},
	}
	svc.eventBuses["default"] = &eventBus{
		Name: "default",
		ARN:  fmt.Sprintf("arn:aws:events:%s:%s:event-bus/default", region, accountID),
	}
	return svc
}

func (s *Service) Name() string { return "eventbridge" }

// Detect identifies EventBridge requests by X-Amz-Target header.
// Multiple prefixes are used by different callers:
//   - AmazonEventBridge.*       — aws_eventbridge_* Terraform resources, SDK v2
//   - AmazonCloudWatchEvents.*  — older CloudWatch Events SDK clients
//   - AWSEvents.*               — Terraform aws_cloudwatch_event_* resources (provider v5)
func (s *Service) Detect(r *http.Request) bool {
	target := r.Header.Get("X-Amz-Target")
	return strings.HasPrefix(target, "AmazonEventBridge.") ||
		strings.HasPrefix(target, "AmazonCloudWatchEvents.") ||
		strings.HasPrefix(target, "AWSEvents.")
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t := r.Header.Get("X-Amz-Target")
	action := ""
	if idx := strings.LastIndex(t, "."); idx != -1 {
		action = t[idx+1:]
	}

	switch action {
	case "PutEvents":
		s.putEvents(w, r)
	case "CreateEventBus":
		s.createEventBus(w, r)
	case "DeleteEventBus":
		s.deleteEventBus(w, r)
	case "DescribeEventBus":
		s.describeEventBus(w, r)
	case "ListEventBuses":
		s.listEventBuses(w, r)
	case "PutRule":
		s.putRule(w, r)
	case "DeleteRule":
		s.deleteRule(w, r)
	case "DescribeRule":
		s.describeRule(w, r)
	case "ListRules":
		s.listRules(w, r)
	case "EnableRule":
		s.enableRule(w, r)
	case "DisableRule":
		s.disableRule(w, r)
	case "PutTargets":
		s.putTargets(w, r)
	case "RemoveTargets":
		s.removeTargets(w, r)
	case "ListTargetsByRule":
		s.listTargetsByRule(w, r)
	case "ListTagsForResource":
		s.listTagsForResource(w, r)
	case "TagResource":
		s.tagResource(w, r)
	case "UntagResource":
		jsonWrite(w, http.StatusOK, map[string]interface{}{})
	default:
		s.jsonError(w, http.StatusBadRequest, "UnknownOperationException",
			fmt.Sprintf("Unknown operation: %s", action))
	}
}

// --- PutEvents ---

func (s *Service) putEvents(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Entries []struct {
			EventBusName string   `json:"EventBusName"`
			Source       string   `json:"Source"`
			DetailType   string   `json:"DetailType"`
			Detail       string   `json:"Detail"`
			Time         string   `json:"Time"`
			Resources    []string `json:"Resources"`
		} `json:"Entries"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, http.StatusBadRequest, "InvalidParameterValue", "could not parse request body")
		return
	}

	type resultEntry struct {
		EventID string `json:"EventId,omitempty"`
	}
	var entries []resultEntry

	s.mu.Lock()
	for _, e := range req.Entries {
		busName := e.EventBusName
		if busName == "" {
			busName = "default"
		}
		eventID := uid.New()
		t := time.Now().UTC()
		if e.Time != "" {
			if parsed, err := time.Parse(time.RFC3339, e.Time); err == nil {
				t = parsed
			}
		}
		s.events = append(s.events, &CapturedEvent{
			EventID:      eventID,
			EventBusName: busName,
			Source:       e.Source,
			DetailType:   e.DetailType,
			Detail:       e.Detail,
			Time:         t,
			Resources:    e.Resources,
		})
		entries = append(entries, resultEntry{EventID: eventID})
	}
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{
		"FailedEntryCount": 0,
		"Entries":          entries,
	})
}

// --- Event bus operations ---

func (s *Service) createEventBus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"Name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		s.jsonError(w, http.StatusBadRequest, "InvalidParameterValue", "Name is required")
		return
	}

	arn := fmt.Sprintf("arn:aws:events:%s:%s:event-bus/%s", s.region, accountID, req.Name)

	s.mu.Lock()
	s.eventBuses[req.Name] = &eventBus{Name: req.Name, ARN: arn}
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]string{"EventBusArn": arn})
}

func (s *Service) deleteEventBus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"Name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		s.jsonError(w, http.StatusBadRequest, "InvalidParameterValue", "Name is required")
		return
	}
	if req.Name == "default" {
		s.jsonError(w, http.StatusBadRequest, "InvalidParameterValue", "Cannot delete the default event bus.")
		return
	}

	s.mu.Lock()
	delete(s.eventBuses, req.Name)
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) describeEventBus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"Name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, http.StatusBadRequest, "InvalidParameterValue", "could not parse request body")
		return
	}
	name := req.Name
	if name == "" {
		name = "default"
	}

	s.mu.RLock()
	bus := s.findEventBus(name)
	s.mu.RUnlock()

	if bus == nil {
		s.jsonError(w, http.StatusBadRequest, "ResourceNotFoundException",
			fmt.Sprintf("Event bus %s does not exist.", name))
		return
	}

	jsonWrite(w, http.StatusOK, map[string]interface{}{
		"Name":   bus.Name,
		"Arn":    bus.ARN,
		"Policy": "",
	})
}

// findEventBus looks up a bus by name or ARN (must be called with mu held).
func (s *Service) findEventBus(nameOrARN string) *eventBus {
	if bus, ok := s.eventBuses[nameOrARN]; ok {
		return bus
	}
	// ARN lookup: arn:aws:events:{region}:{account}:event-bus/{name}
	for _, bus := range s.eventBuses {
		if bus.ARN == nameOrARN {
			return bus
		}
	}
	return nil
}

func (s *Service) listEventBuses(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NamePrefix string `json:"NamePrefix"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.RLock()
	defer s.mu.RUnlock()

	type busEntry struct {
		Name string `json:"Name"`
		Arn  string `json:"Arn"`
	}
	var buses []busEntry
	for _, bus := range s.eventBuses {
		if req.NamePrefix == "" || strings.HasPrefix(bus.Name, req.NamePrefix) {
			buses = append(buses, busEntry{Name: bus.Name, Arn: bus.ARN})
		}
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{"EventBuses": buses})
}

// --- Rule operations ---

func ruleKey(busName, ruleName string) string {
	if busName == "" {
		busName = "default"
	}
	return busName + "/" + ruleName
}

func (s *Service) putRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name               string `json:"Name"`
		EventBusName       string `json:"EventBusName"`
		EventPattern       string `json:"EventPattern"`
		ScheduleExpression string `json:"ScheduleExpression"`
		State              string `json:"State"`
		Description        string `json:"Description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		s.jsonError(w, http.StatusBadRequest, "InvalidParameterValue", "Name is required")
		return
	}
	if req.EventBusName == "" {
		req.EventBusName = "default"
	}
	if req.State == "" {
		req.State = "ENABLED"
	}

	arn := fmt.Sprintf("arn:aws:events:%s:%s:rule/%s/%s", s.region, accountID, req.EventBusName, req.Name)
	key := ruleKey(req.EventBusName, req.Name)

	s.mu.Lock()
	s.rules[key] = &rule{
		Name:               req.Name,
		EventBusName:       req.EventBusName,
		State:              req.State,
		Description:        req.Description,
		EventPattern:       req.EventPattern,
		ScheduleExpression: req.ScheduleExpression,
		ARN:                arn,
	}
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]string{"RuleArn": arn})
}

func (s *Service) deleteRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name         string `json:"Name"`
		EventBusName string `json:"EventBusName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		s.jsonError(w, http.StatusBadRequest, "InvalidParameterValue", "Name is required")
		return
	}

	key := ruleKey(req.EventBusName, req.Name)
	s.mu.Lock()
	delete(s.rules, key)
	delete(s.targets, key)
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) describeRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name         string `json:"Name"`
		EventBusName string `json:"EventBusName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		s.jsonError(w, http.StatusBadRequest, "InvalidParameterValue", "Name is required")
		return
	}

	key := ruleKey(req.EventBusName, req.Name)
	s.mu.RLock()
	rl, ok := s.rules[key]
	s.mu.RUnlock()

	if !ok {
		s.jsonError(w, http.StatusBadRequest, "ResourceNotFoundException",
			fmt.Sprintf("Rule %s does not exist.", req.Name))
		return
	}

	jsonWrite(w, http.StatusOK, map[string]string{
		"Name":               rl.Name,
		"Arn":                rl.ARN,
		"EventBusName":       rl.EventBusName,
		"State":              rl.State,
		"Description":        rl.Description,
		"EventPattern":       rl.EventPattern,
		"ScheduleExpression": rl.ScheduleExpression,
	})
}

func (s *Service) listRules(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EventBusName string `json:"EventBusName"`
		NamePrefix   string `json:"NamePrefix"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.EventBusName == "" {
		req.EventBusName = "default"
	}

	prefix := req.EventBusName + "/"

	s.mu.RLock()
	defer s.mu.RUnlock()

	type ruleEntry struct {
		Name         string `json:"Name"`
		Arn          string `json:"Arn"`
		EventBusName string `json:"EventBusName"`
		State        string `json:"State"`
	}
	var rules []ruleEntry
	for key, rl := range s.rules {
		if strings.HasPrefix(key, prefix) {
			if req.NamePrefix == "" || strings.HasPrefix(rl.Name, req.NamePrefix) {
				rules = append(rules, ruleEntry{
					Name:         rl.Name,
					Arn:          rl.ARN,
					EventBusName: rl.EventBusName,
					State:        rl.State,
				})
			}
		}
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{"Rules": rules})
}

func (s *Service) enableRule(w http.ResponseWriter, r *http.Request) {
	s.setRuleState(w, r, "ENABLED")
}

func (s *Service) disableRule(w http.ResponseWriter, r *http.Request) {
	s.setRuleState(w, r, "DISABLED")
}

func (s *Service) setRuleState(w http.ResponseWriter, r *http.Request, state string) {
	var req struct {
		Name         string `json:"Name"`
		EventBusName string `json:"EventBusName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		s.jsonError(w, http.StatusBadRequest, "InvalidParameterValue", "Name is required")
		return
	}

	key := ruleKey(req.EventBusName, req.Name)
	s.mu.Lock()
	if rl, ok := s.rules[key]; ok {
		rl.State = state
	}
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{})
}

// --- Target operations ---

func (s *Service) putTargets(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Rule         string `json:"Rule"`
		EventBusName string `json:"EventBusName"`
		Targets      []struct {
			ID  string `json:"Id"`
			ARN string `json:"Arn"`
		} `json:"Targets"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Rule == "" {
		s.jsonError(w, http.StatusBadRequest, "InvalidParameterValue", "Rule is required")
		return
	}

	key := ruleKey(req.EventBusName, req.Rule)
	s.mu.Lock()
	existing := s.targets[key]
	byID := map[string]int{}
	for i, t := range existing {
		byID[t.ID] = i
	}
	for _, t := range req.Targets {
		if i, ok := byID[t.ID]; ok {
			existing[i].ARN = t.ARN
		} else {
			existing = append(existing, &target{ID: t.ID, ARN: t.ARN})
		}
	}
	s.targets[key] = existing
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{
		"FailedEntryCount": 0,
		"FailedEntries":    []interface{}{},
	})
}

func (s *Service) removeTargets(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Rule         string   `json:"Rule"`
		EventBusName string   `json:"EventBusName"`
		Ids          []string `json:"Ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Rule == "" {
		s.jsonError(w, http.StatusBadRequest, "InvalidParameterValue", "Rule is required")
		return
	}

	remove := map[string]bool{}
	for _, id := range req.Ids {
		remove[id] = true
	}

	key := ruleKey(req.EventBusName, req.Rule)
	s.mu.Lock()
	existing := s.targets[key]
	var remaining []*target
	for _, t := range existing {
		if !remove[t.ID] {
			remaining = append(remaining, t)
		}
	}
	s.targets[key] = remaining
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{
		"FailedEntryCount": 0,
		"FailedEntries":    []interface{}{},
	})
}

func (s *Service) listTargetsByRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Rule         string `json:"Rule"`
		EventBusName string `json:"EventBusName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Rule == "" {
		s.jsonError(w, http.StatusBadRequest, "InvalidParameterValue", "Rule is required")
		return
	}

	key := ruleKey(req.EventBusName, req.Rule)
	s.mu.RLock()
	ts := s.targets[key]
	s.mu.RUnlock()

	type targetEntry struct {
		ID  string `json:"Id"`
		Arn string `json:"Arn"`
	}
	entries := make([]targetEntry, 0, len(ts))
	for _, t := range ts {
		entries = append(entries, targetEntry{ID: t.ID, Arn: t.ARN})
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{"Targets": entries})
}

// --- Tag operations ---

func (s *Service) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	// SDK v2 cannot decode an empty map {} for Tags; use null to indicate no tags.
	jsonWrite(w, http.StatusOK, map[string]interface{}{
		"Tags": nil,
	})
}

func (s *Service) tagResource(w http.ResponseWriter, r *http.Request) {
	jsonWrite(w, http.StatusOK, map[string]interface{}{})
}

// --- Nimbus inspection endpoints ---

// EventsHandler serves captured events at GET /_nimbus/eventbridge/events.
func (s *Service) EventsHandler(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	if s.events == nil {
		w.Write([]byte("[]"))
		return
	}
	json.NewEncoder(w).Encode(s.events)
}

// ClearEventsHandler clears all captured events. DELETE /_nimbus/eventbridge/events
func (s *Service) ClearEventsHandler(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.events = nil
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// EventCount returns the number of captured events.
func (s *Service) EventCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.events)
}

// --- Helpers ---

func jsonWrite(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (s *Service) jsonError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.Header().Set("x-amzn-ErrorType", code)
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{
		"__type":  code,
		"message": message,
	})
}
