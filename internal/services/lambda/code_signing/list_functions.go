package code_signing

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

type listFunctionsByConfigResponse struct {
	FunctionArns []string `json:"FunctionArns"`
	NextMarker   string   `json:"NextMarker,omitempty"`
}

// GET /2015-03-31/code-signing-configs/{CodeSigningConfigArn}/functions
func (s *Service) ListFunctionsByConfig(w http.ResponseWriter, r *http.Request, arn string) {
	s.mu.RLock()
	_, ok := s.configs[arn]
	if !ok {
		s.mu.RUnlock()
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("CodeSigningConfig not found: %s", arn))
		return
	}

	fns := make([]string, 0)
	for fn, boundArn := range s.functionBinding {
		if boundArn == arn {
			fns = append(fns, fn)
		}
	}
	s.mu.RUnlock()

	sort.Strings(fns)

	q := r.URL.Query()
	maxItems := 50
	if v := q.Get("MaxItems"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxItems = n
		}
	}
	marker := q.Get("Marker")

	if marker != "" {
		for i, fn := range fns {
			if fn == marker {
				fns = fns[i+1:]
				break
			}
		}
	}

	var nextMarker string
	if len(fns) > maxItems {
		nextMarker = fns[maxItems]
		fns = fns[:maxItems]
	}

	jsonhttp.Write(w, http.StatusOK, listFunctionsByConfigResponse{
		FunctionArns: fns,
		NextMarker:   nextMarker,
	})
}
