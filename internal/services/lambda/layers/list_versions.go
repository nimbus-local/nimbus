package layers

import (
	"net/http"
	"sort"
	"strconv"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

type listLayerVersionsResponse struct {
	LayerVersions []*LayerVersion `json:"LayerVersions"`
	NextMarker    string          `json:"NextMarker,omitempty"`
}

// GET /2015-03-31/layers/{LayerName}/versions
func (s *Service) ListVersions(w http.ResponseWriter, r *http.Request, layerName string) {
	q := r.URL.Query()
	compatRuntime := q.Get("CompatibleRuntime")
	compatArch := q.Get("CompatibleArchitecture")
	marker := q.Get("Marker")
	maxItems := 50
	if v := q.Get("MaxItems"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxItems = n
		}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var matched []*LayerVersion
	for _, lv := range s.versions {
		if lv.LayerName != layerName {
			continue
		}
		if compatRuntime != "" && !contains(lv.CompatibleRuntimes, compatRuntime) {
			continue
		}
		if compatArch != "" && !contains(lv.CompatibleArchitectures, compatArch) {
			continue
		}
		matched = append(matched, lv)
	}

	// Sort descending by version number.
	sort.Slice(matched, func(i, j int) bool {
		return matched[i].Version > matched[j].Version
	})

	// Advance past marker (marker is a version number string).
	if marker != "" {
		if markerVer, err := strconv.ParseInt(marker, 10, 64); err == nil {
			for i, lv := range matched {
				if lv.Version == markerVer {
					matched = matched[i+1:]
					break
				}
			}
		}
	}

	var nextMarker string
	if len(matched) > maxItems {
		nextMarker = strconv.FormatInt(matched[maxItems].Version, 10)
		matched = matched[:maxItems]
	}

	if matched == nil {
		matched = []*LayerVersion{}
	}

	jsonhttp.Write(w, http.StatusOK, listLayerVersionsResponse{
		LayerVersions: matched,
		NextMarker:    nextMarker,
	})
}
