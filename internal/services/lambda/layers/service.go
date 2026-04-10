package layers

import (
	"fmt"
	"sync"
)

// Service manages in-memory state for Lambda layer operations.
type Service struct {
	mu             sync.RWMutex
	versions       map[string]*LayerVersion // key = "layerName:N"
	versionCounter map[string]int           // layerName → latest version number
	region         string
	account        string
}

func New(region, account string) *Service {
	return &Service{
		versions:       map[string]*LayerVersion{},
		versionCounter: map[string]int{},
		region:         region,
		account:        account,
	}
}

func (s *Service) layerArn(layerName string) string {
	return fmt.Sprintf("arn:aws:lambda:%s:%s:layer:%s", s.region, s.account, layerName)
}

func (s *Service) layerVersionArn(layerName string, version int64) string {
	return fmt.Sprintf("arn:aws:lambda:%s:%s:layer:%s:%d", s.region, s.account, layerName, version)
}

func versionKey(layerName string, version int64) string {
	return fmt.Sprintf("%s:%d", layerName, version)
}
