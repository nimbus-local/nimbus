package event_sources

import (
	"net/http"
	"sort"
	"strconv"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

type listResponse struct {
	EventSourceMappings []*EventSourceMapping `json:"EventSourceMappings"`
	NextMarker          string                `json:"NextMarker,omitempty"`
}

// GET /2015-03-31/event-source-mappings
func (s *Service) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	functionName := q.Get("FunctionName")
	eventSourceArn := q.Get("EventSourceArn")
	marker := q.Get("Marker")

	maxItems := 100
	if v := q.Get("MaxItems"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxItems = n
		}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	items := make([]*EventSourceMapping, 0, len(s.mappings))
	for _, m := range s.mappings {
		if functionName != "" && m.FunctionName != functionName {
			continue
		}
		if eventSourceArn != "" && m.EventSourceArn != eventSourceArn {
			continue
		}
		items = append(items, m)
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].UUID < items[j].UUID
	})

	// Advance past marker.
	if marker != "" {
		for i, m := range items {
			if m.UUID == marker {
				items = items[i+1:]
				break
			}
		}
	}

	var nextMarker string
	if len(items) > maxItems {
		nextMarker = items[maxItems].UUID
		items = items[:maxItems]
	}

	jsonhttp.Write(w, http.StatusOK, listResponse{
		EventSourceMappings: items,
		NextMarker:          nextMarker,
	})
}
