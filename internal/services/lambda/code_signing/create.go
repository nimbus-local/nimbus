package code_signing

import (
	"errors"
	"net/http"
	"time"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

type createConfigRequest struct {
	AllowedPublishers   AllowedPublishers   `json:"AllowedPublishers"`
	CodeSigningPolicies CodeSigningPolicies `json:"CodeSigningPolicies,omitempty"`
	Description         string              `json:"Description,omitempty"`
}

func (r *createConfigRequest) Validate() error {
	if len(r.AllowedPublishers.SigningProfileVersionArns) == 0 {
		return errors.New("AllowedPublishers.SigningProfileVersionArns must have at least one entry")
	}
	if p := r.CodeSigningPolicies.UntrustedArtifactOnDeployment; p != "" && p != "Warn" && p != "Enforce" {
		return errors.New("UntrustedArtifactOnDeployment must be Warn or Enforce")
	}
	return nil
}

// POST /2015-03-31/code-signing-configs
func (s *Service) Create(w http.ResponseWriter, r *http.Request) {
	req, ok := jsonhttp.Decode[createConfigRequest](w, r)
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
