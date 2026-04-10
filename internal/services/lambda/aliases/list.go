package aliases

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

type listAliasesResponse struct {
	Aliases    []*AliasConfig `json:"Aliases"`
	NextMarker string         `json:"NextMarker,omitempty"`
}

// GET /2015-03-31/functions/{FunctionName}/aliases
func (s *Service) List(w http.ResponseWriter, r *http.Request, functionName string) {
	if !s.checker.FunctionExists(functionName) {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", functionName))
		return
	}

	q := r.URL.Query()
	versionFilter := q.Get("FunctionVersion")
	marker := q.Get("Marker")

	maxItems := 50
	if v := q.Get("MaxItems"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxItems = n
		}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// Collect aliases belonging to this function, optionally filtered by version.
	prefix := functionName + ":"
	result := make([]*AliasConfig, 0)
	for key, alias := range s.aliases {
		if len(key) <= len(prefix) || key[:len(prefix)] != prefix {
			continue
		}
		if versionFilter != "" && alias.FunctionVersion != versionFilter {
			continue
		}
		result = append(result, alias)
	}

	// Sort by alias name for stable pagination.
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})

	// Advance past the marker.
	if marker != "" {
		for i, a := range result {
			if a.Name == marker {
				result = result[i+1:]
				break
			}
		}
	}

	// Apply page limit.
	var nextMarker string
	if len(result) > maxItems {
		nextMarker = result[maxItems].Name
		result = result[:maxItems]
	}

	jsonhttp.Write(w, http.StatusOK, listAliasesResponse{
		Aliases:    result,
		NextMarker: nextMarker,
	})
}
