package url_config

import (
	"fmt"
	"net/http"
	"time"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
	"github.com/nimbus-local/nimbus/internal/uid"
)

type createUrlRequest struct {
	AuthType string `json:"AuthType" validate:"required,oneof=AWS_IAM NONE"`
	Cors     *Cors  `json:"Cors,omitempty"`
}

// POST /2015-03-31/functions/{FunctionName}/url
func (s *Service) CreateUrl(w http.ResponseWriter, r *http.Request, functionName string) {
	req, ok := jsonhttp.DecodeAndValidate[createUrlRequest](w, r)
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

	if _, exists := s.urlConfigs[key]; exists {
		jsonhttp.Error(w, http.StatusConflict, "ResourceConflictException",
			fmt.Sprintf("Function URL config already exists for function: %s", functionName))
		return
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	urlID := uid.New()[:8]
	cfg := &FunctionUrlConfig{
		AuthType:         req.AuthType,
		Cors:             req.Cors,
		CreationTime:     now,
		FunctionArn:      s.arn(functionName, qualifier),
		FunctionUrl:      fmt.Sprintf("https://%s.lambda-url.%s.on.aws/", urlID, s.region),
		LastModifiedTime: now,
		FunctionName:     functionName,
		Qualifier:        qualifier,
	}
	s.urlConfigs[key] = cfg

	jsonhttp.Write(w, http.StatusCreated, cfg)
}
