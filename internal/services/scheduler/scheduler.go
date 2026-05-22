package scheduler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

const accountID = "000000000000"

// Service implements the AWS EventBridge Scheduler emulator.
// Schedules and schedule groups are stored in-memory. The ticker fires schedules
// and invokes their Lambda targets via HTTP.
type Service struct {
	mu        sync.RWMutex
	groups    map[string]*scheduleGroup // name -> group
	schedules map[string]*schedule      // groupName+"/"+name -> schedule
	region    string
	baseURL   string // Nimbus endpoint (e.g. http://localhost:4566) for target invocations
}

type scheduleGroup struct {
	name             string
	arn              string
	state            string
	creationDate     time.Time
	lastModifiedDate time.Time
	tags             []tag
}

type tag struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

type flexibleTimeWindow struct {
	MaximumWindowInMinutes *int   `json:"MaximumWindowInMinutes,omitempty"`
	Mode                   string `json:"Mode"`
}

type schedule struct {
	name                       string
	arn                        string
	groupName                  string
	description                string
	scheduleExpression         string
	scheduleExpressionTimezone string
	state                      string
	flexibleTimeWindow         flexibleTimeWindow
	target                     json.RawMessage
	creationDate               time.Time
	lastModifiedDate           time.Time
	nextFire                   *time.Time
	lastFired                  *time.Time
}

func New(region, baseURL string) *Service {
	if region == "" {
		region = "us-east-1"
	}
	if baseURL == "" {
		baseURL = "http://localhost:4566"
	}
	s := &Service{
		region:    region,
		baseURL:   baseURL,
		groups:    map[string]*scheduleGroup{},
		schedules: map[string]*schedule{},
	}
	// The "default" group always exists.
	now := time.Now().UTC()
	s.groups["default"] = &scheduleGroup{
		name:             "default",
		arn:              s.groupARN("default"),
		state:            "ACTIVE",
		creationDate:     now,
		lastModifiedDate: now,
		tags:             []tag{},
	}
	go s.ticker()
	return s
}

func (s *Service) ticker() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for now := range t.C {
		s.tick(now)
	}
}

func (s *Service) tick(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sch := range s.schedules {
		if sch.state != "ENABLED" {
			continue
		}
		if sch.nextFire == nil {
			continue
		}
		if now.Before(*sch.nextFire) {
			continue
		}
		s.fire(sch, now)
		// Advance nextFire past now (handle bursts: keep stepping until future).
		next := *sch.nextFire
		for !next.After(now) {
			nf, err := nextFireTime(sch.scheduleExpression, next)
			if err != nil {
				sch.nextFire = nil
				break
			}
			next = nf
		}
		if sch.nextFire != nil {
			sch.nextFire = &next
		}
	}
}

// fire records the invocation time and dispatches the target call in a goroutine
// so the ticker never blocks waiting on an HTTP response.
func (s *Service) fire(sch *schedule, now time.Time) {
	t := now
	sch.lastFired = &t
	slog.Info("scheduler: fired", "schedule", sch.name, "group", sch.groupName,
		"expression", sch.scheduleExpression, "at", now.UTC().Format(time.RFC3339))
	// Copy target bytes so the goroutine reads stable data after mu is released.
	targetCopy := append([]byte(nil), sch.target...)
	name := sch.name
	go s.invokeTarget(name, targetCopy)
}

func (s *Service) Name() string { return "scheduler" }

// Detect identifies EventBridge Scheduler requests by path.
// The Scheduler API uses bare /schedules, /schedule-groups, and /tags/{schedulerArn} paths.
func (s *Service) Detect(r *http.Request) bool {
	p := r.URL.Path
	return strings.HasPrefix(p, "/schedules") ||
		strings.HasPrefix(p, "/schedule-groups") ||
		(strings.HasPrefix(p, "/tags/") && strings.Contains(p, "scheduler"))
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	switch {
	case path == "/schedule-groups" && r.Method == http.MethodGet:
		s.listScheduleGroups(w, r)
	case strings.HasPrefix(path, "/schedule-groups/"):
		name := strings.TrimPrefix(path, "/schedule-groups/")
		switch r.Method {
		case http.MethodPost:
			s.createScheduleGroup(w, r, name)
		case http.MethodGet:
			s.getScheduleGroup(w, r, name)
		case http.MethodDelete:
			s.deleteScheduleGroup(w, r, name)
		default:
			jsonError(w, http.StatusMethodNotAllowed, "MethodNotAllowedException", "Method not allowed")
		}

	case path == "/schedules" && r.Method == http.MethodGet:
		s.listSchedules(w, r)
	case strings.HasPrefix(path, "/schedules/"):
		name := strings.TrimPrefix(path, "/schedules/")
		switch r.Method {
		case http.MethodPost:
			s.createSchedule(w, r, name)
		case http.MethodGet:
			s.getSchedule(w, r, name)
		case http.MethodPut:
			s.updateSchedule(w, r, name)
		case http.MethodDelete:
			s.deleteSchedule(w, r, name)
		default:
			jsonError(w, http.StatusMethodNotAllowed, "MethodNotAllowedException", "Method not allowed")
		}

	case strings.HasPrefix(path, "/tags/"):
		arn := strings.TrimPrefix(path, "/tags/")
		switch r.Method {
		case http.MethodGet:
			s.listTagsForResource(w, r, arn)
		case http.MethodPost:
			jsonWrite(w, http.StatusOK, map[string]interface{}{})
		case http.MethodDelete:
			jsonWrite(w, http.StatusOK, map[string]interface{}{})
		default:
			jsonError(w, http.StatusMethodNotAllowed, "MethodNotAllowedException", "Method not allowed")
		}

	default:
		jsonError(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Path not found: %s", path))
	}
}

// --- Schedule groups ---

func (s *Service) createScheduleGroup(w http.ResponseWriter, r *http.Request, name string) {
	var req struct {
		Tags []tag `json:"Tags"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.Tags == nil {
		req.Tags = []tag{}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.groups[name]; exists {
		jsonError(w, http.StatusConflict, "ConflictException",
			fmt.Sprintf("Schedule group %s already exists.", name))
		return
	}

	now := time.Now().UTC()
	arn := s.groupARN(name)
	s.groups[name] = &scheduleGroup{
		name:             name,
		arn:              arn,
		state:            "ACTIVE",
		creationDate:     now,
		lastModifiedDate: now,
		tags:             req.Tags,
	}
	jsonWrite(w, http.StatusCreated, map[string]string{"ScheduleGroupArn": arn})
}

func (s *Service) getScheduleGroup(w http.ResponseWriter, r *http.Request, name string) {
	s.mu.RLock()
	g, ok := s.groups[name]
	s.mu.RUnlock()

	if !ok {
		jsonError(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Schedule group %s does not exist.", name))
		return
	}

	jsonWrite(w, http.StatusOK, map[string]interface{}{
		"Arn":                  g.arn,
		"CreationDate":         float64(g.creationDate.Unix()),
		"LastModificationDate": float64(g.lastModifiedDate.Unix()),
		"Name":                 g.name,
		"State":                g.state,
		"Tags":                 g.tags,
	})
}

func (s *Service) deleteScheduleGroup(w http.ResponseWriter, r *http.Request, name string) {
	if name == "default" {
		jsonError(w, http.StatusConflict, "ConflictException", "Cannot delete the default schedule group.")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.groups[name]; !ok {
		jsonError(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Schedule group %s does not exist.", name))
		return
	}

	// Delete all schedules in this group.
	prefix := name + "/"
	for key := range s.schedules {
		if strings.HasPrefix(key, prefix) {
			delete(s.schedules, key)
		}
	}
	delete(s.groups, name)
	w.WriteHeader(http.StatusOK)
}

func (s *Service) listScheduleGroups(w http.ResponseWriter, r *http.Request) {
	namePrefix := r.URL.Query().Get("NamePrefix")

	s.mu.RLock()
	defer s.mu.RUnlock()

	type entry struct {
		Arn                  string  `json:"Arn"`
		CreationDate         float64 `json:"CreationDate"`
		LastModificationDate float64 `json:"LastModificationDate"`
		Name                 string  `json:"Name"`
		State                string  `json:"State"`
	}
	var groups []entry
	for _, g := range s.groups {
		if namePrefix != "" && !strings.HasPrefix(g.name, namePrefix) {
			continue
		}
		groups = append(groups, entry{
			Arn:                  g.arn,
			CreationDate:         float64(g.creationDate.Unix()),
			LastModificationDate: float64(g.lastModifiedDate.Unix()),
			Name:                 g.name,
			State:                g.state,
		})
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{
		"NextToken":      "",
		"ScheduleGroups": groups,
	})
}

// --- Schedules ---

func scheduleKey(groupName, name string) string {
	if groupName == "" {
		groupName = "default"
	}
	return groupName + "/" + name
}

func (s *Service) createSchedule(w http.ResponseWriter, r *http.Request, name string) {
	var req struct {
		Description                string             `json:"Description"`
		FlexibleTimeWindow         flexibleTimeWindow `json:"FlexibleTimeWindow"`
		GroupName                  string             `json:"GroupName"`
		ScheduleExpression         string             `json:"ScheduleExpression"`
		ScheduleExpressionTimezone string             `json:"ScheduleExpressionTimezone"`
		State                      string             `json:"State"`
		Target                     json.RawMessage    `json:"Target"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "ValidationException", "could not parse request body")
		return
	}
	if req.GroupName == "" {
		req.GroupName = "default"
	}
	if req.State == "" {
		req.State = "ENABLED"
	}
	if req.ScheduleExpressionTimezone == "" {
		req.ScheduleExpressionTimezone = "UTC"
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.groups[req.GroupName]; !ok {
		jsonError(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Schedule group %s does not exist.", req.GroupName))
		return
	}

	key := scheduleKey(req.GroupName, name)
	if _, exists := s.schedules[key]; exists {
		jsonError(w, http.StatusConflict, "ConflictException",
			fmt.Sprintf("Schedule %s already exists in group %s.", name, req.GroupName))
		return
	}

	now := time.Now().UTC()
	arn := s.scheduleARN(req.GroupName, name)
	sch := &schedule{
		name:                       name,
		arn:                        arn,
		groupName:                  req.GroupName,
		description:                req.Description,
		scheduleExpression:         req.ScheduleExpression,
		scheduleExpressionTimezone: req.ScheduleExpressionTimezone,
		state:                      req.State,
		flexibleTimeWindow:         req.FlexibleTimeWindow,
		target:                     req.Target,
		creationDate:               now,
		lastModifiedDate:           now,
	}
	if nf, err := nextFireTime(req.ScheduleExpression, now); err == nil {
		sch.nextFire = &nf
	}
	s.schedules[key] = sch
	jsonWrite(w, http.StatusCreated, map[string]string{"ScheduleArn": arn})
}

func (s *Service) getSchedule(w http.ResponseWriter, r *http.Request, name string) {
	groupName := r.URL.Query().Get("groupName")
	if groupName == "" {
		groupName = "default"
	}

	key := scheduleKey(groupName, name)
	s.mu.RLock()
	sch, ok := s.schedules[key]
	s.mu.RUnlock()

	if !ok {
		jsonError(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Schedule %s does not exist in group %s.", name, groupName))
		return
	}

	jsonWrite(w, http.StatusOK, map[string]interface{}{
		"Arn":                        sch.arn,
		"CreationDate":               float64(sch.creationDate.Unix()),
		"Description":                sch.description,
		"FlexibleTimeWindow":         sch.flexibleTimeWindow,
		"GroupName":                  sch.groupName,
		"LastModificationDate":       float64(sch.lastModifiedDate.Unix()),
		"Name":                       sch.name,
		"ScheduleExpression":         sch.scheduleExpression,
		"ScheduleExpressionTimezone": sch.scheduleExpressionTimezone,
		"State":                      sch.state,
		"Target":                     sch.target,
	})
}

func (s *Service) updateSchedule(w http.ResponseWriter, r *http.Request, name string) {
	var req struct {
		Description                string             `json:"Description"`
		FlexibleTimeWindow         flexibleTimeWindow `json:"FlexibleTimeWindow"`
		GroupName                  string             `json:"GroupName"`
		ScheduleExpression         string             `json:"ScheduleExpression"`
		ScheduleExpressionTimezone string             `json:"ScheduleExpressionTimezone"`
		State                      string             `json:"State"`
		Target                     json.RawMessage    `json:"Target"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "ValidationException", "could not parse request body")
		return
	}
	if req.GroupName == "" {
		req.GroupName = "default"
	}

	key := scheduleKey(req.GroupName, name)
	s.mu.Lock()
	defer s.mu.Unlock()

	sch, ok := s.schedules[key]
	if !ok {
		jsonError(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Schedule %s does not exist in group %s.", name, req.GroupName))
		return
	}

	sch.description = req.Description
	sch.scheduleExpression = req.ScheduleExpression
	if req.ScheduleExpressionTimezone != "" {
		sch.scheduleExpressionTimezone = req.ScheduleExpressionTimezone
	}
	if req.State != "" {
		sch.state = req.State
	}
	sch.flexibleTimeWindow = req.FlexibleTimeWindow
	if req.Target != nil {
		sch.target = req.Target
	}
	now := time.Now().UTC()
	sch.lastModifiedDate = now
	// Recompute next fire after expression change.
	if nf, err := nextFireTime(sch.scheduleExpression, now); err == nil {
		sch.nextFire = &nf
	} else {
		sch.nextFire = nil
	}

	jsonWrite(w, http.StatusOK, map[string]string{"ScheduleArn": sch.arn})
}

func (s *Service) deleteSchedule(w http.ResponseWriter, r *http.Request, name string) {
	groupName := r.URL.Query().Get("groupName")
	if groupName == "" {
		groupName = "default"
	}

	key := scheduleKey(groupName, name)
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.schedules[key]; !ok {
		jsonError(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Schedule %s does not exist in group %s.", name, groupName))
		return
	}

	delete(s.schedules, key)
	w.WriteHeader(http.StatusOK)
}

func (s *Service) listSchedules(w http.ResponseWriter, r *http.Request) {
	groupName := r.URL.Query().Get("ScheduleGroup")
	namePrefix := r.URL.Query().Get("NamePrefix")

	s.mu.RLock()
	defer s.mu.RUnlock()

	type entry struct {
		Arn                  string  `json:"Arn"`
		CreationDate         float64 `json:"CreationDate"`
		GroupName            string  `json:"GroupName"`
		LastModificationDate float64 `json:"LastModificationDate"`
		Name                 string  `json:"Name"`
		State                string  `json:"State"`
		Target               struct {
			Arn string `json:"Arn"`
		} `json:"Target"`
	}
	var schedules []entry
	for _, sch := range s.schedules {
		if groupName != "" && sch.groupName != groupName {
			continue
		}
		if namePrefix != "" && !strings.HasPrefix(sch.name, namePrefix) {
			continue
		}
		// Extract just the target ARN for the list response.
		var tgt struct {
			Arn string `json:"Arn"`
		}
		json.Unmarshal(sch.target, &tgt)
		schedules = append(schedules, entry{
			Arn:                  sch.arn,
			CreationDate:         float64(sch.creationDate.Unix()),
			GroupName:            sch.groupName,
			LastModificationDate: float64(sch.lastModifiedDate.Unix()),
			Name:                 sch.name,
			State:                sch.state,
			Target:               tgt,
		})
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{
		"NextToken": "",
		"Schedules": schedules,
	})
}

// --- Tag operations ---

func (s *Service) listTagsForResource(w http.ResponseWriter, r *http.Request, arn string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Scheduler uses a list [{Key,Value}] not a map — SDK fails on map type.
	var tags []tag
	for _, g := range s.groups {
		if g.arn == arn {
			tags = g.tags
			break
		}
	}
	if tags == nil {
		tags = []tag{}
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{"Tags": tags})
}

// --- Nimbus inspection endpoint ---

// SchedulesHandler serves GET /_nimbus/scheduler/schedules — lists all schedules.
func (s *Service) SchedulesHandler(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	type entry struct {
		Name               string          `json:"Name"`
		GroupName          string          `json:"GroupName"`
		Arn                string          `json:"Arn"`
		State              string          `json:"State"`
		ScheduleExpression string          `json:"ScheduleExpression"`
		Target             json.RawMessage `json:"Target"`
		NextFire           string          `json:"NextFire,omitempty"`
		LastFired          string          `json:"LastFired,omitempty"`
	}
	schedules := make([]entry, 0, len(s.schedules))
	for _, sch := range s.schedules {
		e := entry{
			Name:               sch.name,
			GroupName:          sch.groupName,
			Arn:                sch.arn,
			State:              sch.state,
			ScheduleExpression: sch.scheduleExpression,
			Target:             sch.target,
		}
		if sch.nextFire != nil {
			e.NextFire = sch.nextFire.UTC().Format(time.RFC3339)
		}
		if sch.lastFired != nil {
			e.LastFired = sch.lastFired.UTC().Format(time.RFC3339)
		}
		schedules = append(schedules, e)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(schedules)
}

// --- ARN helpers ---

func (s *Service) groupARN(name string) string {
	return fmt.Sprintf("arn:aws:scheduler:%s:%s:schedule-group/%s", s.region, accountID, name)
}

func (s *Service) scheduleARN(group, name string) string {
	return fmt.Sprintf("arn:aws:scheduler:%s:%s:schedule/%s/%s", s.region, accountID, group, name)
}

// --- HTTP helpers ---

func jsonWrite(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{
		"message": message,
		"code":    code,
	})
}
