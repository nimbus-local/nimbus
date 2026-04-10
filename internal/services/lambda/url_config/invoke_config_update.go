package url_config

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

type updateInvokeConfigRequest struct {
	DestinationConfig        *DestinationConfig `json:"DestinationConfig,omitempty"`
	MaximumEventAgeInSeconds int                `json:"MaximumEventAgeInSeconds,omitempty"`
	MaximumRetryAttempts     int                `json:"MaximumRetryAttempts,omitempty"`
}

func (r *updateInvokeConfigRequest) Validate() error {
	if r.MaximumEventAgeInSeconds != 0 && (r.MaximumEventAgeInSeconds < 60 || r.MaximumEventAgeInSeconds > 21600) {
		return errors.New("MaximumEventAgeInSeconds must be between 60 and 21600")
	}
	if r.MaximumRetryAttempts < 0 || r.MaximumRetryAttempts > 2 {
		return errors.New("MaximumRetryAttempts must be between 0 and 2")
	}
	return nil
}

// POST /2015-03-31/functions/{FunctionName}/event-invoke-config
func (s *Service) UpdateInvokeConfig(w http.ResponseWriter, r *http.Request, functionName string) {
	req, ok := jsonhttp.Decode[updateInvokeConfigRequest](w, r)
	if !ok {
		return
	}

	qualifier := r.URL.Query().Get("Qualifier")
	key := configKey(functionName, qualifier)

	s.mu.Lock()
	defer s.mu.Unlock()

	cfg, ok := s.invokeConfigs[key]
	if !ok {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function event invoke config not found for function: %s", functionName))
		return
	}

	if req.DestinationConfig != nil {
		cfg.DestinationConfig = req.DestinationConfig
	}
	if req.MaximumEventAgeInSeconds != 0 {
		cfg.MaximumEventAgeInSeconds = req.MaximumEventAgeInSeconds
	}
	if req.MaximumRetryAttempts != 0 {
		cfg.MaximumRetryAttempts = req.MaximumRetryAttempts
	}
	cfg.LastModified = time.Now().UTC().Format(time.RFC3339Nano)

	jsonhttp.Write(w, http.StatusOK, cfg)
}
