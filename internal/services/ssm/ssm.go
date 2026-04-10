package ssm

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

// Service implements the AWS SSM Parameter Store emulator.
// All parameters are stored in-memory. Supports String, StringList,
// and SecureString types. Parameters use path-based hierarchy (e.g.
// /myapp/prod/db-password) which is the standard pattern for real apps.
type Service struct {
	mu         sync.RWMutex
	parameters map[string]*parameter // keyed by name
	region     string
	account    string
}

type parameter struct {
	name        string
	value       string
	paramType   string // String, StringList, SecureString
	description string
	version     int64
	createdAt   time.Time
	updatedAt   time.Time
	arn         string
}

const (
	defaultAccount   = "000000000000"
	typeString       = "String"
	typeStringList   = "StringList"
	typeSecureString = "SecureString"
)

func New(region string) *Service {
	if region == "" {
		region = "us-east-1"
	}
	return &Service{
		parameters: map[string]*parameter{},
		region:     region,
		account:    defaultAccount,
	}
}

func (s *Service) Name() string { return "ssm" }

// Detect identifies SSM requests by X-Amz-Target header.
// All SSM operations use AmazonSSM.<OperationName>
func (s *Service) Detect(r *http.Request) bool {
	target := r.Header.Get("X-Amz-Target")
	return strings.HasPrefix(target, "AmazonSSM.")
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	target := r.Header.Get("X-Amz-Target")
	operation := ""
	if idx := strings.LastIndex(target, "."); idx != -1 {
		operation = target[idx+1:]
	}

	switch operation {
	case "PutParameter":
		s.putParameter(w, r)
	case "GetParameter":
		s.getParameter(w, r)
	case "GetParameters":
		s.getParameters(w, r)
	case "GetParametersByPath":
		s.getParametersByPath(w, r)
	case "DeleteParameter":
		s.deleteParameter(w, r)
	case "DeleteParameters":
		s.deleteParameters(w, r)
	case "DescribeParameters":
		s.describeParameters(w, r)
	default:
		s.jsonError(w, http.StatusBadRequest, "InvalidAction",
			fmt.Sprintf("Operation %s is not supported.", operation))
	}
}

// --- ARN helper ---

func (s *Service) arn(name string) string {
	return fmt.Sprintf("arn:aws:ssm:%s:%s:parameter%s",
		s.region, s.account, name)
}

// --- Operations ---

// PutParameter — creates or updates a parameter
func (s *Service) putParameter(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"Name"`
		Value       string `json:"Value"`
		Type        string `json:"Type"`
		Description string `json:"Description"`
		Overwrite   bool   `json:"Overwrite"`
	}
	if !s.decode(w, r, &req) {
		return
	}

	if req.Name == "" {
		s.jsonError(w, http.StatusBadRequest, "ValidationException",
			"Parameter name is required.")
		return
	}

	if req.Value == "" {
		s.jsonError(w, http.StatusBadRequest, "ValidationException",
			"Parameter value is required.")
		return
	}

	// Default type is String
	if req.Type == "" {
		req.Type = typeString
	}

	if !validType(req.Type) {
		s.jsonError(w, http.StatusBadRequest, "ValidationException",
			fmt.Sprintf("Invalid parameter type: %s. Must be String, StringList, or SecureString.", req.Type))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, exists := s.parameters[req.Name]
	if exists && !req.Overwrite {
		s.jsonError(w, http.StatusBadRequest, "ParameterAlreadyExists",
			fmt.Sprintf("Parameter %s already exists. Use Overwrite to update.", req.Name))
		return
	}

	version := int64(1)
	if exists {
		version = existing.version + 1
	}

	p := &parameter{
		name:        req.Name,
		value:       req.Value,
		paramType:   req.Type,
		description: req.Description,
		version:     version,
		updatedAt:   time.Now().UTC(),
		arn:         s.arn(req.Name),
	}

	if !exists {
		p.createdAt = p.updatedAt
	} else {
		p.createdAt = existing.createdAt
	}

	s.parameters[req.Name] = p

	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{
		"Version": version,
	})
}

// GetParameter — retrieves a single parameter by name
func (s *Service) getParameter(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name           string `json:"Name"`
		WithDecryption bool   `json:"WithDecryption"`
	}
	if !s.decode(w, r, &req) {
		return
	}

	if req.Name == "" {
		s.jsonError(w, http.StatusBadRequest, "ValidationException",
			"Parameter name is required.")
		return
	}

	s.mu.RLock()
	p, ok := s.parameters[req.Name]
	s.mu.RUnlock()

	if !ok {
		s.jsonError(w, http.StatusBadRequest, "ParameterNotFound",
			fmt.Sprintf("Parameter %s not found.", req.Name))
		return
	}

	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{
		"Parameter": parameterResponse(p),
	})
}

// GetParameters — retrieves multiple parameters by name
func (s *Service) getParameters(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Names          []string `json:"Names"`
		WithDecryption bool     `json:"WithDecryption"`
	}
	if !s.decode(w, r, &req) {
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var found []map[string]interface{}
	var invalid []string

	for _, name := range req.Names {
		if p, ok := s.parameters[name]; ok {
			found = append(found, parameterResponse(p))
		} else {
			invalid = append(invalid, name)
		}
	}

	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{
		"Parameters":        found,
		"InvalidParameters": invalid,
	})
}

// GetParametersByPath — retrieves all parameters under a path hierarchy
func (s *Service) getParametersByPath(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path      string `json:"Path"`
		Recursive bool   `json:"Recursive"`
	}
	if !s.decode(w, r, &req) {
		return
	}

	if req.Path == "" {
		s.jsonError(w, http.StatusBadRequest, "ValidationException",
			"Path is required.")
		return
	}

	// Ensure path ends with / for prefix matching
	prefix := req.Path
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []map[string]interface{}
	for name, p := range s.parameters {
		if !strings.HasPrefix(name, prefix) {
			continue
		}

		// Non-recursive: only return direct children (no further slashes after prefix)
		if !req.Recursive {
			remainder := name[len(prefix):]
			if strings.Contains(remainder, "/") {
				continue
			}
		}

		results = append(results, parameterResponse(p))
	}

	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{
		"Parameters": results,
	})
}

// DeleteParameter — deletes a single parameter
func (s *Service) deleteParameter(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"Name"`
	}
	if !s.decode(w, r, &req) {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.parameters[req.Name]; !ok {
		s.jsonError(w, http.StatusBadRequest, "ParameterNotFound",
			fmt.Sprintf("Parameter %s not found.", req.Name))
		return
	}

	delete(s.parameters, req.Name)
	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{})
}

// DeleteParameters — deletes multiple parameters, returns which were deleted and which were invalid
func (s *Service) deleteParameters(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Names []string `json:"Names"`
	}
	if !s.decode(w, r, &req) {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var deleted []string
	var invalid []string

	for _, name := range req.Names {
		if _, ok := s.parameters[name]; ok {
			delete(s.parameters, name)
			deleted = append(deleted, name)
		} else {
			invalid = append(invalid, name)
		}
	}

	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{
		"DeletedParameters": deleted,
		"InvalidParameters": invalid,
	})
}

// DescribeParameters — returns metadata about parameters (not values)
func (s *Service) describeParameters(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MaxResults int `json:"MaxResults"`
	}
	// Ignore decode errors — request body is optional for DescribeParameters
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.RLock()
	defer s.mu.RUnlock()

	type paramMeta struct {
		Name             string  `json:"Name"`
		Type             string  `json:"Type"`
		Description      string  `json:"Description,omitempty"`
		Version          int64   `json:"Version"`
		ARN              string  `json:"ARN"`
		LastModifiedDate float64 `json:"LastModifiedDate"`
	}

	var results []paramMeta
	for _, p := range s.parameters {
		results = append(results, paramMeta{
			Name:             p.name,
			Type:             p.paramType,
			Description:      p.description,
			Version:          p.version,
			ARN:              p.arn,
			LastModifiedDate: float64(p.updatedAt.Unix()),
		})
	}

	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{
		"Parameters": results,
	})
}

// --- Helpers ---

func parameterResponse(p *parameter) map[string]interface{} {
	return map[string]interface{}{
		"Name":             p.name,
		"Value":            p.value,
		"Type":             p.paramType,
		"Version":          p.version,
		"ARN":              p.arn,
		"LastModifiedDate": float64(p.updatedAt.Unix()),
	}
}

func validType(t string) bool {
	return t == typeString || t == typeStringList || t == typeSecureString
}

func (s *Service) decode(w http.ResponseWriter, r *http.Request, v interface{}) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		s.jsonError(w, http.StatusBadRequest, "ValidationException",
			fmt.Sprintf("Could not parse request body: %s", err.Error()))
		return false
	}
	return true
}

func (s *Service) jsonResponse(w http.ResponseWriter, status int, v interface{}) {
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

// newID generates a unique ID — used for parameter versioning context
func newID() string {
	return uid.New()
}
