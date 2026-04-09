package code_signing

import (
	"net/http"
	"time"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

type createConfigRequest struct {
	AllowedPublishers   AllowedPublishers   `json:"AllowedPublishers" validate:"required"`
	CodeSigningPolicies CodeSigningPolicies `json:"CodeSigningPolicies,omitempty"`
	Description         string              `json:"Description,omitempty"`
}

// POST /2015-03-31/code-signing-configs
func (s *Service) Create(w http.ResponseWriter, r *http.Request) {
	req, ok := jsonhttp.DecodeAndValidate[createConfigRequest](w, r)
	if !ok {
		return
	}

	if req.CodeSigningPolicies.UntrustedArtifactOnDeployment == "" {
		req.CodeSigningPolicies.UntrustedArtifactOnDeployment = "Warn"
	}

	arn, id := s.newARN()

	cfg := &CodeSigningConfig{
		AllowedPublishers:    req.AllowedPublishers,
		CodeSigningConfigArn: arn,
		CodeSigningConfigId:  id,
		CodeSigningPolicies:  req.CodeSigningPolicies,
		Description:          req.Description,
		LastModified:         time.Now().UTC().Format(time.RFC3339),
	}

	s.mu.Lock()
	s.configs[arn] = cfg
	s.mu.Unlock()

	jsonhttp.Write(w, http.StatusCreated, map[string]any{"CodeSigningConfig": cfg})
}
