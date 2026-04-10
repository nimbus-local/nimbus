package code_signing

import (
	"net/http"
	"sort"
	"strconv"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

type listConfigsResponse struct {
	CodeSigningConfigs []*CodeSigningConfig `json:"CodeSigningConfigs"`
	NextMarker         string               `json:"NextMarker,omitempty"`
}

// GET /2015-03-31/code-signing-configs
func (s *Service) ListConfigs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	maxItems := 50
	if v := q.Get("MaxItems"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxItems = n
		}
	}
	marker := q.Get("Marker")

	s.mu.RLock()
	cfgs := make([]*CodeSigningConfig, 0, len(s.configs))
	for _, cfg := range s.configs {
		cfgs = append(cfgs, cfg)
	}
	s.mu.RUnlock()

	sort.Slice(cfgs, func(i, j int) bool {
		return cfgs[i].CodeSigningConfigArn < cfgs[j].CodeSigningConfigArn
	})

	if marker != "" {
		for i, cfg := range cfgs {
			if cfg.CodeSigningConfigArn == marker {
				cfgs = cfgs[i+1:]
				break
			}
		}
	}

	var nextMarker string
	if len(cfgs) > maxItems {
		nextMarker = cfgs[maxItems].CodeSigningConfigArn
		cfgs = cfgs[:maxItems]
	}

	jsonhttp.Write(w, http.StatusOK, listConfigsResponse{
		CodeSigningConfigs: cfgs,
		NextMarker:         nextMarker,
	})
}
