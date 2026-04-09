package event_sources

import (
	"net/http"
	"time"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

type updateRequest struct {
	BatchSize                      int                `json:"BatchSize,omitempty"`
	BisectBatchOnFunctionError     bool               `json:"BisectBatchOnFunctionError,omitempty"`
	DestinationConfig              *DestinationConfig `json:"DestinationConfig,omitempty"`
	FilterCriteria                 *FilterCriteria    `json:"FilterCriteria,omitempty"`
	FunctionName                   string             `json:"FunctionName,omitempty"`
	MaximumBatchingWindowInSeconds int                `json:"MaximumBatchingWindowInSeconds,omitempty"`
	MaximumRecordAgeInSeconds      int                `json:"MaximumRecordAgeInSeconds,omitempty"`
	MaximumRetryAttempts           int                `json:"MaximumRetryAttempts,omitempty"`
	ParallelizationFactor          int                `json:"ParallelizationFactor,omitempty"`
	TumblingWindowInSeconds        int                `json:"TumblingWindowInSeconds,omitempty"`
}

// PUT /2015-03-31/event-source-mappings/{UUID}
func (s *Service) Update(w http.ResponseWriter, r *http.Request, uuid string) {
	req, ok := jsonhttp.DecodeAndValidate[updateRequest](w, r)
	if !ok {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	m, ok := s.mappings[uuid]
	if !ok {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			"The resource you requested does not exist: "+uuid)
		return
	}

	if req.BatchSize != 0 {
		m.BatchSize = req.BatchSize
	}
	if req.BisectBatchOnFunctionError {
		m.BisectBatchOnFunctionError = req.BisectBatchOnFunctionError
	}
	if req.DestinationConfig != nil {
		m.DestinationConfig = req.DestinationConfig
	}
	if req.FilterCriteria != nil {
		m.FilterCriteria = req.FilterCriteria
	}
	if req.FunctionName != "" {
		m.FunctionName = req.FunctionName
		m.FunctionArn = req.FunctionName
	}
	if req.MaximumBatchingWindowInSeconds != 0 {
		m.MaximumBatchingWindowInSeconds = req.MaximumBatchingWindowInSeconds
	}
	if req.MaximumRecordAgeInSeconds != 0 {
		m.MaximumRecordAgeInSeconds = req.MaximumRecordAgeInSeconds
	}
	if req.MaximumRetryAttempts != 0 {
		m.MaximumRetryAttempts = req.MaximumRetryAttempts
	}
	if req.ParallelizationFactor != 0 {
		m.ParallelizationFactor = req.ParallelizationFactor
	}
	if req.TumblingWindowInSeconds != 0 {
		m.TumblingWindowInSeconds = req.TumblingWindowInSeconds
	}

	m.LastModified = float64(time.Now().Unix())

	jsonhttp.Write(w, http.StatusAccepted, m)
}
