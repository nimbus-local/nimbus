package url_config

import (
	"net/http"
	"sort"
	"strconv"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

type listInvokeConfigsResponse struct {
	FunctionEventInvokeConfigs []*EventInvokeConfig `json:"FunctionEventInvokeConfigs"`
	NextMarker                 string               `json:"NextMarker,omitempty"`
}

// GET /2015-03-31/event-invoke-config/functions
func (s *Service) ListInvokeConfigs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	functionFilter := q.Get("FunctionName")
	maxItems := 50
	if v := q.Get("MaxItems"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxItems = n
		}
	}
	marker := q.Get("Marker")

	s.mu.RLock()
	defer s.mu.RUnlock()

	var cfgs []*EventInvokeConfig
	for _, cfg := range s.invokeConfigs {
		if functionFilter != "" && cfg.FunctionName != functionFilter {
			continue
		}
		cfgs = append(cfgs, cfg)
	}

	sort.Slice(cfgs, func(i, j int) bool {
		return cfgs[i].FunctionArn < cfgs[j].FunctionArn
	})

	if marker != "" {
		for i, cfg := range cfgs {
			if cfg.FunctionArn == marker {
				cfgs = cfgs[i+1:]
				break
			}
		}
	}

	var nextMarker string
	if len(cfgs) > maxItems {
		nextMarker = cfgs[maxItems].FunctionArn
		cfgs = cfgs[:maxItems]
	}

	if cfgs == nil {
		cfgs = []*EventInvokeConfig{}
	}

	jsonhttp.Write(w, http.StatusOK, listInvokeConfigsResponse{
		FunctionEventInvokeConfigs: cfgs,
		NextMarker:                 nextMarker,
	})
}
