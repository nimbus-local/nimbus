package url_config

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

type listUrlsResponse struct {
	FunctionUrlConfigs []*FunctionUrlConfig `json:"FunctionUrlConfigs"`
	NextMarker         string               `json:"NextMarker,omitempty"`
}

// GET /2015-03-31/functions/{FunctionName}/urls
func (s *Service) ListUrls(w http.ResponseWriter, r *http.Request, functionName string) {
	if !s.checker.FunctionExists(functionName) {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", functionName))
		return
	}

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

	var cfgs []*FunctionUrlConfig
	for _, cfg := range s.urlConfigs {
		if cfg.FunctionName == functionName {
			cfgs = append(cfgs, cfg)
		}
	}

	sort.Slice(cfgs, func(i, j int) bool {
		return cfgs[i].FunctionUrl < cfgs[j].FunctionUrl
	})

	if marker != "" {
		for i, cfg := range cfgs {
			if cfg.FunctionUrl == marker {
				cfgs = cfgs[i+1:]
				break
			}
		}
	}

	var nextMarker string
	if len(cfgs) > maxItems {
		nextMarker = cfgs[maxItems].FunctionUrl
		cfgs = cfgs[:maxItems]
	}

	if cfgs == nil {
		cfgs = []*FunctionUrlConfig{}
	}

	jsonhttp.Write(w, http.StatusOK, listUrlsResponse{
		FunctionUrlConfigs: cfgs,
		NextMarker:         nextMarker,
	})
}
