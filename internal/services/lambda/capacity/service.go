package capacity

import "sync"

// TagStore is satisfied by function_crud.Service via its GetTags/SetTags methods.
type TagStore interface {
	GetTags(functionName string) (map[string]string, bool)
	SetTags(functionName string, tags map[string]string) bool
}

type Service struct {
	mu    sync.RWMutex
	store TagStore
}

func New(store TagStore) *Service {
	return &Service{store: store}
}
