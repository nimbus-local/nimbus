package function_crud

import (
	"fmt"
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

// GET /2015-03-31/functions/{FunctionName}
func (s *Service) Get(w http.ResponseWriter, r *http.Request, name string) {
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

// GET /2015-03-31/functions/{FunctionName}/configuration
// In the real API this omits the Code object; our mock has no code to omit so it's identical.
func (s *Service) GetConfiguration(w http.ResponseWriter, r *http.Request, name string) {
	s.Get(w, r, name)
}
