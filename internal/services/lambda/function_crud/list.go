package function_crud

import (
	"net/http"
	"sort"
	"strconv"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

type listFunctionsResponse struct {
	Functions  []*FunctionConfig `json:"Functions"`
	NextMarker string            `json:"NextMarker,omitempty"`
}

// GET /2015-03-31/functions
func (s *Service) List(w http.ResponseWriter, r *http.Request) {
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

	// Collect $LATEST entries only (skip published version snapshots keyed as "name:N").
	fns := make([]*FunctionConfig, 0, len(s.functions))
	for key, fn := range s.functions {
		if fn.Version == "$LATEST" || key == fn.FunctionName {
			fns = append(fns, fn)
		}
	}

	// Sort by name for stable, predictable pagination.
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
