package permissions

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

type addPermissionRequest struct {
	Action              string `json:"Action"`
	Principal           string `json:"Principal"`
	StatementId         string `json:"StatementId"`
	EventSourceToken    string `json:"EventSourceToken,omitempty"`
	FunctionUrlAuthType string `json:"FunctionUrlAuthType,omitempty"`
	PrincipalOrgID      string `json:"PrincipalOrgID,omitempty"`
	RevisionId          string `json:"RevisionId,omitempty"`
	SourceAccount       string `json:"SourceAccount,omitempty"`
	SourceArn           string `json:"SourceArn,omitempty"`
	Qualifier           string `json:"-"` // populated from query param, not body
}

func (r *addPermissionRequest) Validate() error {
	if r.Action == "" {
		return errors.New("Action is required")
	}
	if r.Principal == "" {
		return errors.New("Principal is required")
	}
	if r.StatementId == "" {
		return errors.New("StatementId is required")
	}
	return nil
}

// POST /2015-03-31/functions/{FunctionName}/policy
func (s *Service) AddPermission(w http.ResponseWriter, r *http.Request, functionName string) {
	req, ok := jsonhttp.Decode[addPermissionRequest](w, r)
	if !ok {
		return
	}
	req.Qualifier = r.URL.Query().Get("Qualifier")

	if !s.checker.FunctionExists(functionName) {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", functionName))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.functionPolicies[functionName][req.StatementId]; exists {
		jsonhttp.Error(w, http.StatusConflict, "ResourceConflictException",
			fmt.Sprintf("The statement id (%s) provided already exists. Please provide a new statement id, or remove the existing statement.", req.StatementId))
		return
	}

	if req.RevisionId != "" && s.policyRevisions[functionName] != req.RevisionId {
		jsonhttp.Error(w, http.StatusPreconditionFailed, "PreconditionFailedException",
			"The RevisionId provided does not match the latest RevisionId for the Lambda function or alias.")
		return
	}

	stmt := &Statement{
		Sid:       req.StatementId,
		Effect:    "Allow",
		Principal: req.Principal,
		Action:    req.Action,
	}

	if s.functionPolicies[functionName] == nil {
		s.functionPolicies[functionName] = map[string]*Statement{}
	}
	s.functionPolicies[functionName][req.StatementId] = stmt
	revID := newRevisionID()
	s.policyRevisions[functionName] = revID

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
