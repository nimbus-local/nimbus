package event_sources

import "strings"

// ListKinesisESMs returns all enabled event source mappings targeting Kinesis streams.
func (s *Service) ListKinesisESMs() []*EventSourceMapping {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*EventSourceMapping
	for _, m := range s.mappings {
		if m.State != "Enabled" {
			continue
		}
		if !strings.Contains(m.EventSourceArn, ":kinesis:") {
			continue
		}
		out = append(out, m)
	}
	return out
}
