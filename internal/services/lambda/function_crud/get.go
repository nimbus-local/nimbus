package function_crud

import (
	"fmt"
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

// GET /2015-03-31/functions/{FunctionName}
// Returns the AWS GetFunction envelope: {"Configuration": {...}, "Code": {...}, "Tags": {...}}
func (s *Service) Get(w http.ResponseWriter, r *http.Request, name string) {
	s.mu.RLock()
	fn, ok := s.functions[name]
	s.mu.RUnlock()

	if !ok {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", name))
		return
	}

	tags := fn.Tags
	if tags == nil {
		tags = map[string]string{}
	}
	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{
		"Configuration": fn,
		"Code":          map[string]string{"Location": "", "RepositoryType": "S3"},
		"Tags":          tags,
	})
}

// GET /2015-03-31/functions/{FunctionName}/configuration
// Returns the flat FunctionConfiguration (no envelope), used by SDK waiters.
func (s *Service) GetConfiguration(w http.ResponseWriter, r *http.Request, name string) {
	s.mu.RLock()
	fn, ok := s.functions[name]
	s.mu.RUnlock()

	if !ok {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", name))
		return
	}

	jsonhttp.Write(w, http.StatusOK, fn)
}
