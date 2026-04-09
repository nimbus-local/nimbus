package layers

import (
	"net/http"
	"sort"
	"strconv"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

type layerEntry struct {
	LatestMatchingVersion *LayerVersion `json:"LatestMatchingVersion"`
	LayerArn              string        `json:"LayerArn"`
	LayerName             string        `json:"LayerName"`
}

type listLayersResponse struct {
	Layers     []layerEntry `json:"Layers"`
	NextMarker string       `json:"NextMarker,omitempty"`
}

// GET /2015-03-31/layers
func (s *Service) ListLayers(w http.ResponseWriter, r *http.Request) {
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

	// Build map of layerName → latest version.
	latest := map[string]*LayerVersion{}
	for _, lv := range s.versions {
		cur, ok := latest[lv.LayerName]
		if !ok || lv.Version > cur.Version {
			latest[lv.LayerName] = lv
		}
	}

	// Collect layer names sorted for stable pagination.
	names := make([]string, 0, len(latest))
	for name := range latest {
		names = append(names, name)
	}
	sort.Strings(names)

	// Filter by runtime / architecture.
	entries := make([]layerEntry, 0, len(names))
	for _, name := range names {
		lv := latest[name]
		if compatRuntime != "" && !contains(lv.CompatibleRuntimes, compatRuntime) {
			continue
		}
		if compatArch != "" && !contains(lv.CompatibleArchitectures, compatArch) {
			continue
		}
		entries = append(entries, layerEntry{
			LatestMatchingVersion: lv,
			LayerArn:              s.layerArn(name),
			LayerName:             name,
		})
	}

	// Advance to marker (marker = first item of the next page).
	if marker != "" {
		for i, e := range entries {
			if e.LayerName == marker {
				entries = entries[i:]
				break
			}
		}
	}

	var nextMarker string
	if len(entries) > maxItems {
		nextMarker = entries[maxItems].LayerName
		entries = entries[:maxItems]
	}

	jsonhttp.Write(w, http.StatusOK, listLayersResponse{
		Layers:     entries,
		NextMarker: nextMarker,
	})
}

func contains(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}
