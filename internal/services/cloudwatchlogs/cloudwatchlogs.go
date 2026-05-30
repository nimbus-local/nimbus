package cloudwatchlogs

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
	"github.com/nimbus-local/nimbus/internal/uid"
)

const accountID = "000000000000"

// Service implements the AWS CloudWatch Logs emulator.
// Log groups and streams are stored in-memory. PutLogEvents accepts
// log data but keeps only the most recent 10,000 events per stream.
type Service struct {
	mu     sync.RWMutex
	groups map[string]*logGroup // groupName -> group
	region string
}

type logGroup struct {
	name            string
	arn             string
	createdAt       int64 // unix ms
	tags            map[string]string
	retentionInDays int
	streams         map[string]*logStream // streamName -> stream
}

type logEvent struct {
	timestamp int64
	message   string
}

type logStream struct {
	name                string
	arn                 string
	createdAt           int64
	firstEventTimestamp *int64
	lastEventTimestamp  *int64
	lastIngestionTime   *int64
	uploadSequenceToken string
	events              []logEvent // capped at maxEvents
}

const maxEvents = 10_000

func New(region string) *Service {
	if region == "" {
		region = "us-east-1"
	}
	return &Service{
		region: region,
		groups: map[string]*logGroup{},
	}
}

func (s *Service) Name() string { return "cloudwatchlogs" }

// Reset clears all in-memory state.
func (s *Service) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.groups = map[string]*logGroup{}
}

// Detect identifies CloudWatch Logs requests by X-Amz-Target header.
func (s *Service) Detect(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), "Logs_20140328.")
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	target := r.Header.Get("X-Amz-Target")
	action := target[strings.LastIndex(target, ".")+1:]

	switch action {
	case "CreateLogGroup":
		s.createLogGroup(w, r)
	case "DeleteLogGroup":
		s.deleteLogGroup(w, r)
	case "DescribeLogGroups":
		s.describeLogGroups(w, r)
	case "CreateLogStream":
		s.createLogStream(w, r)
	case "DeleteLogStream":
		s.deleteLogStream(w, r)
	case "DescribeLogStreams":
		s.describeLogStreams(w, r)
	case "PutLogEvents":
		s.putLogEvents(w, r)
	case "GetLogEvents":
		s.getLogEvents(w, r)
	case "FilterLogEvents":
		s.filterLogEvents(w, r)
	case "PutRetentionPolicy":
		s.putRetentionPolicy(w, r)
	case "DeleteRetentionPolicy":
		s.deleteRetentionPolicy(w, r)
	case "ListTagsForResource", "ListTagsLogGroup":
		jsonhttp.Write(w, http.StatusOK, map[string]interface{}{"tags": map[string]string{}})
	default:
		jsonhttp.Error(w, http.StatusBadRequest, "InvalidParameterException",
			fmt.Sprintf("Action %s is not supported.", action))
	}
}

// --- Log groups ---

func (s *Service) createLogGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LogGroupName string            `json:"logGroupName"`
		Tags         map[string]string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.LogGroupName == "" {
		jsonhttp.Error(w, http.StatusBadRequest, "InvalidParameterException", "logGroupName is required")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.groups[req.LogGroupName]; exists {
		jsonhttp.Error(w, http.StatusBadRequest, "ResourceAlreadyExistsException",
			fmt.Sprintf("Log group %s already exists.", req.LogGroupName))
		return
	}

	tags := req.Tags
	if tags == nil {
		tags = map[string]string{}
	}
	s.groups[req.LogGroupName] = &logGroup{
		name:      req.LogGroupName,
		arn:       s.groupARN(req.LogGroupName),
		createdAt: nowMS(),
		tags:      tags,
		streams:   map[string]*logStream{},
	}
	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) deleteLogGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LogGroupName string `json:"logGroupName"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.Lock()
	if _, ok := s.groups[req.LogGroupName]; !ok {
		s.mu.Unlock()
		jsonhttp.Error(w, http.StatusBadRequest, "ResourceNotFoundException",
			fmt.Sprintf("Log group %s not found.", req.LogGroupName))
		return
	}
	delete(s.groups, req.LogGroupName)
	s.mu.Unlock()
	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) describeLogGroups(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LogGroupNamePrefix string `json:"logGroupNamePrefix"`
		Limit              int    `json:"limit"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.RLock()
	defer s.mu.RUnlock()

	type groupEntry struct {
		LogGroupName string `json:"logGroupName"`
		Arn          string `json:"arn"`
		CreationTime int64  `json:"creationTime"`
	}
	var results []groupEntry
	for _, g := range s.groups {
		if req.LogGroupNamePrefix != "" && !strings.HasPrefix(g.name, req.LogGroupNamePrefix) {
			continue
		}
		results = append(results, groupEntry{
			LogGroupName: g.name,
			Arn:          g.arn,
			CreationTime: g.createdAt,
		})
	}
	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{
		"logGroups": results,
	})
}

// --- Log streams ---

func (s *Service) createLogStream(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LogGroupName  string `json:"logGroupName"`
		LogStreamName string `json:"logStreamName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.LogGroupName == "" || req.LogStreamName == "" {
		jsonhttp.Error(w, http.StatusBadRequest, "InvalidParameterException",
			"logGroupName and logStreamName are required")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	g, ok := s.groups[req.LogGroupName]
	if !ok {
		jsonhttp.Error(w, http.StatusBadRequest, "ResourceNotFoundException",
			fmt.Sprintf("Log group %s not found.", req.LogGroupName))
		return
	}
	if _, exists := g.streams[req.LogStreamName]; exists {
		jsonhttp.Error(w, http.StatusBadRequest, "ResourceAlreadyExistsException",
			fmt.Sprintf("Log stream %s already exists.", req.LogStreamName))
		return
	}
	token := uid.New()
	g.streams[req.LogStreamName] = &logStream{
		name:                req.LogStreamName,
		arn:                 s.streamARN(req.LogGroupName, req.LogStreamName),
		createdAt:           nowMS(),
		uploadSequenceToken: token,
	}
	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) deleteLogStream(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LogGroupName  string `json:"logGroupName"`
		LogStreamName string `json:"logStreamName"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.Lock()
	g, ok := s.groups[req.LogGroupName]
	if ok {
		delete(g.streams, req.LogStreamName)
	}
	s.mu.Unlock()
	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) describeLogStreams(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LogGroupName        string `json:"logGroupName"`
		LogStreamNamePrefix string `json:"logStreamNamePrefix"`
		Limit               int    `json:"limit"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.RLock()
	defer s.mu.RUnlock()

	g, ok := s.groups[req.LogGroupName]
	if !ok {
		jsonhttp.Error(w, http.StatusBadRequest, "ResourceNotFoundException",
			fmt.Sprintf("Log group %s not found.", req.LogGroupName))
		return
	}

	type streamEntry struct {
		LogStreamName       string `json:"logStreamName"`
		Arn                 string `json:"arn"`
		CreationTime        int64  `json:"creationTime"`
		UploadSequenceToken string `json:"uploadSequenceToken"`
	}
	var results []streamEntry
	for _, st := range g.streams {
		if req.LogStreamNamePrefix != "" && !strings.HasPrefix(st.name, req.LogStreamNamePrefix) {
			continue
		}
		results = append(results, streamEntry{
			LogStreamName:       st.name,
			Arn:                 st.arn,
			CreationTime:        st.createdAt,
			UploadSequenceToken: st.uploadSequenceToken,
		})
	}
	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{
		"logStreams": results,
	})
}

// --- Log events ---

func (s *Service) putLogEvents(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LogGroupName  string `json:"logGroupName"`
		LogStreamName string `json:"logStreamName"`
		LogEvents     []struct {
			Timestamp int64  `json:"timestamp"`
			Message   string `json:"message"`
		} `json:"logEvents"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil ||
		req.LogGroupName == "" || req.LogStreamName == "" {
		jsonhttp.Error(w, http.StatusBadRequest, "InvalidParameterException",
			"logGroupName, logStreamName, and logEvents are required")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	g, ok := s.groups[req.LogGroupName]
	if !ok {
		jsonhttp.Error(w, http.StatusBadRequest, "ResourceNotFoundException",
			fmt.Sprintf("Log group %s not found.", req.LogGroupName))
		return
	}
	st, ok := g.streams[req.LogStreamName]
	if !ok {
		jsonhttp.Error(w, http.StatusBadRequest, "ResourceNotFoundException",
			fmt.Sprintf("Log stream %s not found.", req.LogStreamName))
		return
	}

	now := nowMS()
	for _, e := range req.LogEvents {
		st.events = append(st.events, logEvent{timestamp: e.Timestamp, message: e.Message})
		if st.firstEventTimestamp == nil || e.Timestamp < *st.firstEventTimestamp {
			ts := e.Timestamp
			st.firstEventTimestamp = &ts
		}
		if st.lastEventTimestamp == nil || e.Timestamp > *st.lastEventTimestamp {
			ts := e.Timestamp
			st.lastEventTimestamp = &ts
		}
	}
	// Cap to maxEvents, keeping the most recent
	if len(st.events) > maxEvents {
		st.events = st.events[len(st.events)-maxEvents:]
	}
	st.lastIngestionTime = &now
	token := uid.New()
	st.uploadSequenceToken = token

	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{
		"nextSequenceToken": token,
	})
}

// --- Log retrieval ---

func (s *Service) getLogEvents(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LogGroupName  string `json:"logGroupName"`
		LogStreamName string `json:"logStreamName"`
		StartTime     int64  `json:"startTime"`
		EndTime       int64  `json:"endTime"`
		Limit         int    `json:"limit"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.RLock()
	defer s.mu.RUnlock()

	g, ok := s.groups[req.LogGroupName]
	if !ok {
		jsonhttp.Error(w, http.StatusBadRequest, "ResourceNotFoundException",
			fmt.Sprintf("Log group %s not found.", req.LogGroupName))
		return
	}
	st, ok := g.streams[req.LogStreamName]
	if !ok {
		jsonhttp.Error(w, http.StatusBadRequest, "ResourceNotFoundException",
			fmt.Sprintf("Log stream %s not found.", req.LogStreamName))
		return
	}

	events := filterEvents(st.events, req.StartTime, req.EndTime, "", req.Limit)
	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{
		"events":            toOutputEvents(events),
		"nextForwardToken":  "f/0",
		"nextBackwardToken": "b/0",
	})
}

func (s *Service) filterLogEvents(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LogGroupName   string   `json:"logGroupName"`
		LogStreamNames []string `json:"logStreamNames"`
		StartTime      int64    `json:"startTime"`
		EndTime        int64    `json:"endTime"`
		FilterPattern  string   `json:"filterPattern"`
		Limit          int      `json:"limit"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.RLock()
	defer s.mu.RUnlock()

	g, ok := s.groups[req.LogGroupName]
	if !ok {
		jsonhttp.Error(w, http.StatusBadRequest, "ResourceNotFoundException",
			fmt.Sprintf("Log group %s not found.", req.LogGroupName))
		return
	}

	// Collect streams to search
	streams := g.streams
	if len(req.LogStreamNames) > 0 {
		streams = make(map[string]*logStream, len(req.LogStreamNames))
		for _, name := range req.LogStreamNames {
			if st, exists := g.streams[name]; exists {
				streams[name] = st
			}
		}
	}

	type filteredEvent struct {
		LogStreamName string `json:"logStreamName"`
		Timestamp     int64  `json:"timestamp"`
		Message       string `json:"message"`
		IngestionTime int64  `json:"ingestionTime"`
		EventId       string `json:"eventId"`
	}
	var results []filteredEvent
	for streamName, st := range streams {
		for _, e := range filterEvents(st.events, req.StartTime, req.EndTime, req.FilterPattern, 0) {
			results = append(results, filteredEvent{
				LogStreamName: streamName,
				Timestamp:     e.timestamp,
				Message:       e.message,
				IngestionTime: nowMS(),
				EventId:       uid.New(),
			})
		}
	}
	if req.Limit > 0 && len(results) > req.Limit {
		results = results[:req.Limit]
	}
	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{
		"events":    results,
		"nextToken": "",
	})
}

// filterEvents applies time-range and pattern filters to a slice of events.
func filterEvents(events []logEvent, start, end int64, pattern string, limit int) []logEvent {
	var out []logEvent
	for _, e := range events {
		if start > 0 && e.timestamp < start {
			continue
		}
		if end > 0 && e.timestamp > end {
			continue
		}
		if pattern != "" && !strings.Contains(e.message, pattern) {
			continue
		}
		out = append(out, e)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

type outputEvent struct {
	Timestamp     int64  `json:"timestamp"`
	Message       string `json:"message"`
	IngestionTime int64  `json:"ingestionTime"`
}

func toOutputEvents(events []logEvent) []outputEvent {
	now := nowMS()
	out := make([]outputEvent, len(events))
	for i, e := range events {
		out[i] = outputEvent{Timestamp: e.timestamp, Message: e.message, IngestionTime: now}
	}
	return out
}

// --- Retention policy ---

func (s *Service) putRetentionPolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LogGroupName    string `json:"logGroupName"`
		RetentionInDays int    `json:"retentionInDays"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.LogGroupName == "" {
		jsonhttp.Error(w, http.StatusBadRequest, "InvalidParameterException", "logGroupName is required")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.groups[req.LogGroupName]
	if !ok {
		jsonhttp.Error(w, http.StatusBadRequest, "ResourceNotFoundException",
			fmt.Sprintf("Log group %s not found.", req.LogGroupName))
		return
	}
	g.retentionInDays = req.RetentionInDays
	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) deleteRetentionPolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LogGroupName string `json:"logGroupName"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	s.mu.Lock()
	if g, ok := s.groups[req.LogGroupName]; ok {
		g.retentionInDays = 0
	}
	s.mu.Unlock()
	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{})
}

// LogsHandler serves /_nimbus/logs/{group}/{stream} — streams recent events as plain text.
func (s *Service) LogsHandler(w http.ResponseWriter, r *http.Request) {
	// Path: /_nimbus/logs/{group...}/{stream}
	path := strings.TrimPrefix(r.URL.Path, "/_nimbus/logs/")
	idx := strings.LastIndex(path, "/")
	if idx < 0 {
		http.Error(w, "path must be /_nimbus/logs/{group}/{stream}", http.StatusBadRequest)
		return
	}
	groupName := "/" + path[:idx]
	streamName := path[idx+1:]

	s.mu.RLock()
	defer s.mu.RUnlock()

	g, ok := s.groups[groupName]
	if !ok {
		http.Error(w, fmt.Sprintf("log group %s not found", groupName), http.StatusNotFound)
		return
	}
	st, ok := g.streams[streamName]
	if !ok {
		http.Error(w, fmt.Sprintf("log stream %s not found", streamName), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	for _, e := range st.events {
		t := time.UnixMilli(e.timestamp).UTC().Format(time.RFC3339)
		fmt.Fprintf(w, "%s %s\n", t, e.message)
	}
}

// --- ARN helpers ---

func (s *Service) groupARN(name string) string {
	return fmt.Sprintf("arn:aws:logs:%s:%s:log-group:%s", s.region, accountID, name)
}

func (s *Service) streamARN(group, stream string) string {
	return fmt.Sprintf("arn:aws:logs:%s:%s:log-group:%s:log-stream:%s",
		s.region, accountID, group, stream)
}

func nowMS() int64 {
	return time.Now().UnixMilli()
}
