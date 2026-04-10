package url_config

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

type updateUrlRequest struct {
	AuthType string `json:"AuthType,omitempty"`
	Cors     *Cors  `json:"Cors,omitempty"`
}

func (r *updateUrlRequest) Validate() error {
	if r.AuthType != "" && r.AuthType != "AWS_IAM" && r.AuthType != "NONE" {
		return errors.New("AuthType must be AWS_IAM or NONE")
	}
	return nil
}

// PUT /2015-03-31/functions/{FunctionName}/url
func (s *Service) UpdateUrl(w http.ResponseWriter, r *http.Request, functionName string) {
	req, ok := jsonhttp.Decode[updateUrlRequest](w, r)
	if !ok {
		return
	}

	qualifier := r.URL.Query().Get("Qualifier")
	key := configKey(functionName, qualifier)

	s.mu.Lock()
	defer s.mu.Unlock()

	cfg, ok := s.urlConfigs[key]
	if !ok {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function URL config not found for function: %s", functionName))
		return
	}

	if req.AuthType != "" {
		cfg.AuthType = req.AuthType
	}
	if req.Cors != nil {
		cfg.Cors = req.Cors
	}
	cfg.LastModifiedTime = time.Now().UTC().Format(time.RFC3339Nano)

	jsonhttp.Write(w, http.StatusOK, cfg)
}
