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
	name      string
	arn       string
	createdAt int64 // unix ms
	tags      map[string]string
	streams   map[string]*logStream // streamName -> stream
}

type logStream struct {
	name                string
	arn                 string
	createdAt           int64
	firstEventTimestamp *int64
	lastEventTimestamp  *int64
	lastIngestionTime   *int64
	uploadSequenceToken string
}

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
	case "DescribeLogStreams":
		s.describeLogStreams(w, r)
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
