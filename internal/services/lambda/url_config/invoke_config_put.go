package url_config

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

type putInvokeConfigRequest struct {
	DestinationConfig        *DestinationConfig `json:"DestinationConfig,omitempty"`
	MaximumEventAgeInSeconds int                `json:"MaximumEventAgeInSeconds,omitempty"`
	MaximumRetryAttempts     int                `json:"MaximumRetryAttempts,omitempty"`
}

func (r *putInvokeConfigRequest) Validate() error {
	if r.MaximumEventAgeInSeconds != 0 && (r.MaximumEventAgeInSeconds < 60 || r.MaximumEventAgeInSeconds > 21600) {
		return errors.New("MaximumEventAgeInSeconds must be between 60 and 21600")
	}
	if r.MaximumRetryAttempts < 0 || r.MaximumRetryAttempts > 2 {
		return errors.New("MaximumRetryAttempts must be between 0 and 2")
	}
	return nil
}

// PUT /2015-03-31/functions/{FunctionName}/event-invoke-config
func (s *Service) PutInvokeConfig(w http.ResponseWriter, r *http.Request, functionName string) {
	req, ok := jsonhttp.Decode[putInvokeConfigRequest](w, r)
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
