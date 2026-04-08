package lambda

import (
	"net/http"
	"sort"
	"strconv"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

type listFunctionsResponse struct {
	Functions  []*functionConfig `json:"Functions"`
	NextMarker string            `json:"NextMarker,omitempty"`
}

// GET /2015-03-31/functions
func (s *Service) listFunctions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	maxItems := 50
	if v := q.Get("MaxItems"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxItems = n
		}
	}
	marker := q.Get("Marker")

	s.mu.RLock()
	defer s.mu.RUnlock()

	// Collect and sort by name for stable, predictable pagination.
	fns := make([]*functionConfig, 0, len(s.functions))
	for _, fn := range s.functions {
		fns = append(fns, fn)
	}
	sort.Slice(fns, func(i, j int) bool {
		return fns[i].FunctionName < fns[j].FunctionName
	})

	// Advance past the marker.
	if marker != "" {
		for i, fn := range fns {
			if fn.FunctionName == marker {
				fns = fns[i+1:]
				break
			}
		}
	}

	// Apply page limit.
	var nextMarker string
	if len(fns) > maxItems {
		nextMarker = fns[maxItems].FunctionName
		fns = fns[:maxItems]
	}

	jsonhttp.Write(w, http.StatusOK, listFunctionsResponse{
		Functions:  fns,
		NextMarker: nextMarker,
	})
}
