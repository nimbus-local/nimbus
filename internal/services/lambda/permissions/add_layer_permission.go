package permissions

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

type addLayerVersionPermissionRequest struct {
	StatementId    string `json:"StatementId"`
	Action         string `json:"Action"`
	Principal      string `json:"Principal"`
	OrganizationId string `json:"OrganizationId,omitempty"`
}

func (r *addLayerVersionPermissionRequest) Validate() error {
	if r.StatementId == "" {
		return errors.New("StatementId is required")
	}
	if r.Action == "" {
		return errors.New("Action is required")
	}
	if r.Principal == "" {
		return errors.New("Principal is required")
	}
	return nil
}

// POST /2018-10-31/layers/{LayerName}/versions/{VersionNumber}/policy
func (s *Service) AddLayerVersionPermission(w http.ResponseWriter, r *http.Request, layerName string, versionNumber int) {
	req, ok := jsonhttp.Decode[addLayerVersionPermissionRequest](w, r)
	if !ok {
		return
	}

	key := fmt.Sprintf("%s:%d", layerName, versionNumber)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.layerPolicies[key][req.StatementId]; exists {
		jsonhttp.Error(w, http.StatusConflict, "ResourceConflictException",
			fmt.Sprintf("The statement id (%s) provided already exists. Please provide a new statement id, or remove the existing statement.", req.StatementId))
		return
	}

	stmt := &Statement{
		Sid:       req.StatementId,
		Effect:    "Allow",
		Principal: req.Principal,
		Action:    req.Action,
	}

	if s.layerPolicies[key] == nil {
		s.layerPolicies[key] = map[string]*Statement{}
	}
	s.layerPolicies[key][req.StatementId] = stmt
	revID := newRevisionID()
	s.layerRevisions[key] = revID

	stmtBytes, err := json.Marshal(stmt)
	if err != nil {
		jsonhttp.Error(w, http.StatusInternalServerError, "ServiceException", "failed to marshal statement")
		return
	}

	jsonhttp.Write(w, http.StatusCreated, map[string]string{
		"RevisionId": revID,
		"Statement":  string(stmtBytes),
	})
}
