package aliases

import (
	"fmt"
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

type createAliasRequest struct {
	Description     string                     `json:"Description,omitempty"`
	FunctionVersion string                     `json:"FunctionVersion" validate:"required"`
	Name            string                     `json:"Name"            validate:"required"`
	RoutingConfig   *AliasRoutingConfiguration `json:"RoutingConfig,omitempty"`
}

// POST /2015-03-31/functions/{FunctionName}/aliases
func (s *Service) Create(w http.ResponseWriter, r *http.Request, functionName string) {
	req, ok := jsonhttp.DecodeAndValidate[createAliasRequest](w, r)
	if !ok {
		return
	}

	if !s.checker.FunctionExists(functionName) {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", functionName))
		return
	}

	key := functionName + ":" + req.Name

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.aliases[key]; exists {
		jsonhttp.Error(w, http.StatusConflict, "ResourceConflictException",
			fmt.Sprintf("Alias already exists: %s", req.Name))
		return
	}

	alias := &AliasConfig{
		AliasArn:        s.arn(functionName, req.Name),
		Description:     req.Description,
		FunctionVersion: req.FunctionVersion,
		Name:            req.Name,
		RevisionId:      newRevisionID(),
		RoutingConfig:   req.RoutingConfig,
	}
	s.aliases[key] = alias

	jsonhttp.Write(w, http.StatusCreated, alias)
}
