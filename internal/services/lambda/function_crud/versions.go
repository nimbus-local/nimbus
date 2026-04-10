package function_crud

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
	"github.com/nimbus-local/nimbus/internal/uid"
)

// GET /2015-03-31/functions/{FunctionName}/versions
func (s *Service) ListVersions(w http.ResponseWriter, r *http.Request, name string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	fn, ok := s.functions[name]
	if !ok {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", name))
		return
	}

	// Collect $LATEST plus any published snapshots keyed as "name:N".
	versions := []*FunctionConfig{fn}
	prefix := name + ":"
	for key, v := range s.functions {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			versions = append(versions, v)
		}
	}

	jsonhttp.Write(w, http.StatusOK, map[string]any{
		"Versions": versions,
	})
}

// POST /2015-03-31/functions/{FunctionName}/versions
func (s *Service) PublishVersion(w http.ResponseWriter, r *http.Request, name string) {
	var req struct {
		CodeSha256  string `json:"CodeSha256,omitempty"`
		Description string `json:"Description,omitempty"`
		RevisionId  string `json:"RevisionId,omitempty"`
	}
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonhttp.Error(w, http.StatusBadRequest, "InvalidParameterValueException", err.Error())
			return
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	fn, ok := s.functions[name]
	if !ok {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", name))
		return
	}

	if req.RevisionId != "" && req.RevisionId != fn.RevisionId {
		jsonhttp.Error(w, http.StatusPreconditionFailed, "PreconditionFailedException",
			"The RevisionId provided does not match the latest RevisionId for the Lambda function.")
		return
	}

	s.versionCounter[name]++
	version := fmt.Sprintf("%d", s.versionCounter[name])

	published := *fn // copy $LATEST snapshot
	published.Version = version
	published.RevisionId = uid.New()
	if req.Description != "" {
		published.Description = req.Description
	}

	s.functions[name+":"+version] = &published

	jsonhttp.Write(w, http.StatusOK, &published)
}
