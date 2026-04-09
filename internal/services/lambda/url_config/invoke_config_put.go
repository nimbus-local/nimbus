package url_config

import (
	"fmt"
	"net/http"
	"time"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

type putInvokeConfigRequest struct {
	DestinationConfig        *DestinationConfig `json:"DestinationConfig,omitempty"`
	MaximumEventAgeInSeconds int                `json:"MaximumEventAgeInSeconds,omitempty" validate:"omitempty,min=60,max=21600"`
	MaximumRetryAttempts     int                `json:"MaximumRetryAttempts,omitempty"     validate:"omitempty,min=0,max=2"`
}

// PUT /2015-03-31/functions/{FunctionName}/event-invoke-config
func (s *Service) PutInvokeConfig(w http.ResponseWriter, r *http.Request, functionName string) {
	req, ok := jsonhttp.DecodeAndValidate[putInvokeConfigRequest](w, r)
	if !ok {
		return
	}

	qualifier := r.URL.Query().Get("Qualifier")

	if !s.checker.FunctionExists(functionName) {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", functionName))
		return
	}

	key := configKey(functionName, qualifier)

	s.mu.Lock()
	defer s.mu.Unlock()

	cfg := &EventInvokeConfig{
		DestinationConfig:        req.DestinationConfig,
		FunctionArn:              s.arn(functionName, qualifier),
		LastModified:             time.Now().UTC().Format(time.RFC3339Nano),
		MaximumEventAgeInSeconds: req.MaximumEventAgeInSeconds,
		MaximumRetryAttempts:     req.MaximumRetryAttempts,
		FunctionName:             functionName,
		Qualifier:                qualifier,
	}
	s.invokeConfigs[key] = cfg

	jsonhttp.Write(w, http.StatusOK, cfg)
}
