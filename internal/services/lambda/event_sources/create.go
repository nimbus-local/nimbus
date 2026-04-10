package event_sources

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
	"github.com/nimbus-local/nimbus/internal/uid"
)

type createRequest struct {
	FunctionName                   string             `json:"FunctionName"`
	EventSourceArn                 string             `json:"EventSourceArn,omitempty"`
	BatchSize                      int                `json:"BatchSize,omitempty"`
	BisectBatchOnFunctionError     bool               `json:"BisectBatchOnFunctionError,omitempty"`
	DestinationConfig              *DestinationConfig `json:"DestinationConfig,omitempty"`
	FilterCriteria                 *FilterCriteria    `json:"FilterCriteria,omitempty"`
	MaximumBatchingWindowInSeconds int                `json:"MaximumBatchingWindowInSeconds,omitempty"`
	MaximumRecordAgeInSeconds      int                `json:"MaximumRecordAgeInSeconds,omitempty"`
	MaximumRetryAttempts           int                `json:"MaximumRetryAttempts,omitempty"`
	ParallelizationFactor          int                `json:"ParallelizationFactor,omitempty"`
	StartingPosition               string             `json:"StartingPosition,omitempty"`
	StartingPositionTimestamp      float64            `json:"StartingPositionTimestamp,omitempty"`
	TumblingWindowInSeconds        int                `json:"TumblingWindowInSeconds,omitempty"`
}

func (r *createRequest) Validate() error {
	if r.FunctionName == "" {
		return errors.New("FunctionName is required")
	}
	if r.BatchSize != 0 && (r.BatchSize < 1 || r.BatchSize > 10000) {
		return errors.New("BatchSize must be between 1 and 10000")
	}
	if r.MaximumBatchingWindowInSeconds < 0 || r.MaximumBatchingWindowInSeconds > 300 {
		return errors.New("MaximumBatchingWindowInSeconds must be between 0 and 300")
	}
	if r.ParallelizationFactor != 0 && (r.ParallelizationFactor < 1 || r.ParallelizationFactor > 10) {
		return errors.New("ParallelizationFactor must be between 1 and 10")
	}
	if r.StartingPosition != "" &&
		r.StartingPosition != "TRIM_HORIZON" &&
		r.StartingPosition != "LATEST" &&
		r.StartingPosition != "AT_TIMESTAMP" {
		return errors.New("StartingPosition must be TRIM_HORIZON, LATEST, or AT_TIMESTAMP")
	}
	if r.TumblingWindowInSeconds < 0 || r.TumblingWindowInSeconds > 900 {
		return errors.New("TumblingWindowInSeconds must be between 0 and 900")
	}
	return nil
}

// POST /2015-03-31/event-source-mappings
func (s *Service) Create(w http.ResponseWriter, r *http.Request) {
	req, ok := jsonhttp.Decode[createRequest](w, r)
	if !ok {
		return
	}

	// Strip qualifier (e.g. "name:alias") for existence check.
	baseName := req.FunctionName
	if idx := strings.Index(baseName, ":"); idx != -1 {
		baseName = baseName[:idx]
	}

	if !s.checker.FunctionExists(baseName) {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			"Function not found: "+req.FunctionName)
		return
	}

	m := &EventSourceMapping{
		BatchSize:                      req.BatchSize,
		BisectBatchOnFunctionError:     req.BisectBatchOnFunctionError,
		DestinationConfig:              req.DestinationConfig,
		EventSourceArn:                 req.EventSourceArn,
		FilterCriteria:                 req.FilterCriteria,
		FunctionArn:                    req.FunctionName,
		FunctionName:                   req.FunctionName,
		LastModified:                   float64(time.Now().Unix()),
		MaximumBatchingWindowInSeconds: req.MaximumBatchingWindowInSeconds,
		MaximumRecordAgeInSeconds:      req.MaximumRecordAgeInSeconds,
		MaximumRetryAttempts:           req.MaximumRetryAttempts,
		ParallelizationFactor:          req.ParallelizationFactor,
		StartingPosition:               req.StartingPosition,
		StartingPositionTimestamp:      req.StartingPositionTimestamp,
		State:                          "Enabled",
		StateTransitionReason:          "User action",
		TumblingWindowInSeconds:        req.TumblingWindowInSeconds,
		UUID:                           uid.New(),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.mappings[m.UUID] = m

	jsonhttp.Write(w, http.StatusAccepted, m)
}
