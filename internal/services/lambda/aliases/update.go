package aliases

import (
	"fmt"
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

type updateAliasRequest struct {
	Description     string                     `json:"Description,omitempty"`
	FunctionVersion string                     `json:"FunctionVersion,omitempty"`
	RevisionId      string                     `json:"RevisionId,omitempty"`
	RoutingConfig   *AliasRoutingConfiguration `json:"RoutingConfig,omitempty"`
}

// PUT /2015-03-31/functions/{FunctionName}/aliases/{Name}
func (s *Service) Update(w http.ResponseWriter, r *http.Request, functionName, aliasName string) {
	req, ok := jsonhttp.DecodeAndValidate[updateAliasRequest](w, r)
	if !ok {
		return
	}

	key := functionName + ":" + aliasName

	s.mu.Lock()
	defer s.mu.Unlock()

	alias, ok := s.aliases[key]
	if !ok {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", aliasName))
		return
	}

	if req.RevisionId != "" && req.RevisionId != alias.RevisionId {
		jsonhttp.Error(w, http.StatusPreconditionFailed, "PreconditionFailedException",
			"The RevisionId provided does not match the latest RevisionId for the Lambda function.")
		return
	}

	if req.Description != "" {
		alias.Description = req.Description
	}
	if req.FunctionVersion != "" {
		alias.FunctionVersion = req.FunctionVersion
	}
	if req.RoutingConfig != nil {
		alias.RoutingConfig = req.RoutingConfig
	}

	alias.RevisionId = newRevisionID()

	jsonhttp.Write(w, http.StatusOK, alias)
}
