package event_sources

import (
	"net/http"
	"strings"
	"time"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
	"github.com/nimbus-local/nimbus/internal/uid"
)

type createRequest struct {
	FunctionName                   string             `json:"FunctionName"                              validate:"required"`
	EventSourceArn                 string             `json:"EventSourceArn,omitempty"`
	BatchSize                      int                `json:"BatchSize,omitempty"                       validate:"omitempty,min=1,max=10000"`
	BisectBatchOnFunctionError     bool               `json:"BisectBatchOnFunctionError,omitempty"`
	DestinationConfig              *DestinationConfig `json:"DestinationConfig,omitempty"`
	FilterCriteria                 *FilterCriteria    `json:"FilterCriteria,omitempty"`
	MaximumBatchingWindowInSeconds int                `json:"MaximumBatchingWindowInSeconds,omitempty"  validate:"omitempty,min=0,max=300"`
	MaximumRecordAgeInSeconds      int                `json:"MaximumRecordAgeInSeconds,omitempty"`
	MaximumRetryAttempts           int                `json:"MaximumRetryAttempts,omitempty"`
	ParallelizationFactor          int                `json:"ParallelizationFactor,omitempty"           validate:"omitempty,min=1,max=10"`
	StartingPosition               string             `json:"StartingPosition,omitempty"                validate:"omitempty,oneof=TRIM_HORIZON LATEST AT_TIMESTAMP"`
	StartingPositionTimestamp      float64            `json:"StartingPositionTimestamp,omitempty"`
	TumblingWindowInSeconds        int                `json:"TumblingWindowInSeconds,omitempty"         validate:"omitempty,min=0,max=900"`
}

// POST /2015-03-31/event-source-mappings
func (s *Service) Create(w http.ResponseWriter, r *http.Request) {
	req, ok := jsonhttp.DecodeAndValidate[createRequest](w, r)
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
