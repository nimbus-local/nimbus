package function_crud

// GetTags returns a copy of the tags for the named function.
// The second return value is false if the function does not exist.
func (s *Service) GetTags(name string) (map[string]string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	fn, ok := s.functions[name]
	if !ok {
		return nil, false
	}
	out := make(map[string]string, len(fn.Tags))
	for k, v := range fn.Tags {
		out[k] = v
	}
	return out, true
}

// SetTags replaces the tags for the named function. Returns false if the function does not exist.
func (s *Service) SetTags(name string, tags map[string]string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn, ok := s.functions[name]
	if !ok {
		return false
	}
	fn.Tags = tags
	return true
}
