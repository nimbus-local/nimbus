package code_signing

import (
	"fmt"
	"net/http"
	"time"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

type updateConfigRequest struct {
	AllowedPublishers   *AllowedPublishers   `json:"AllowedPublishers,omitempty"`
	CodeSigningPolicies *CodeSigningPolicies `json:"CodeSigningPolicies,omitempty"`
	Description         *string              `json:"Description,omitempty"`
}

// PUT /2015-03-31/code-signing-configs/{CodeSigningConfigArn}
func (s *Service) UpdateConfig(w http.ResponseWriter, r *http.Request, arn string) {
	req, ok := jsonhttp.DecodeAndValidate[updateConfigRequest](w, r)
	if !ok {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cfg, ok := s.configs[arn]
	if !ok {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("CodeSigningConfig not found: %s", arn))
		return
	}

	if req.AllowedPublishers != nil {
		cfg.AllowedPublishers = *req.AllowedPublishers
	}
	if req.CodeSigningPolicies != nil {
		cfg.CodeSigningPolicies = *req.CodeSigningPolicies
	}
	if req.Description != nil {
		cfg.Description = *req.Description
	}
	cfg.LastModified = time.Now().UTC().Format(time.RFC3339)

	jsonhttp.Write(w, http.StatusOK, map[string]any{"CodeSigningConfig": cfg})
}
