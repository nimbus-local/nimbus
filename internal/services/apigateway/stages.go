package apigateway

import (
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

func (s *Service) createDeployment(w http.ResponseWriter, r *http.Request, apiID string) {
	if _, ok := s.db.getAPI(apiID); !ok {
		notFound(w, apiID, "RestApi")
		return
	}
	var req struct {
		Description string `json:"description"`
		StageName   string `json:"stageName"`
	}
	if err := jsonDecode(r, &req); err != nil {
		apiError(w, http.StatusBadRequest, "BadRequestException", "invalid request body: "+err.Error())
		return
	}
	d, ok := s.db.createDeployment(apiID, req.Description, req.StageName)
	if !ok {
		notFound(w, apiID, "RestApi")
		return
	}
	jsonhttp.Write(w, http.StatusCreated, d)
}

func (s *Service) getDeployments(w http.ResponseWriter, r *http.Request, apiID string) {
	deployments, ok := s.db.listDeployments(apiID)
	if !ok {
		notFound(w, apiID, "RestApi")
		return
	}
	jsonhttp.Write(w, http.StatusOK, map[string]any{"item": deployments})
}

func (s *Service) getDeployment(w http.ResponseWriter, r *http.Request, apiID, deploymentID string) {
	d, ok := s.db.getDeployment(apiID, deploymentID)
	if !ok {
		notFound(w, deploymentID, "Deployment")
		return
	}
	jsonhttp.Write(w, http.StatusOK, d)
}

func (s *Service) deleteDeployment(w http.ResponseWriter, r *http.Request, apiID, deploymentID string) {
	if !s.db.deleteDeployment(apiID, deploymentID) {
		notFound(w, deploymentID, "Deployment")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Service) createStage(w http.ResponseWriter, r *http.Request, apiID string) {
	if _, ok := s.db.getAPI(apiID); !ok {
		notFound(w, apiID, "RestApi")
		return
	}
	var req struct {
		StageName    string            `json:"stageName"`
		DeploymentID string            `json:"deploymentId"`
		Description  string            `json:"description"`
		Variables    map[string]string `json:"variables"`
	}
	if err := jsonDecode(r, &req); err != nil {
		apiError(w, http.StatusBadRequest, "BadRequestException", "invalid request body: "+err.Error())
		return
	}
	if req.StageName == "" {
		apiError(w, http.StatusBadRequest, "BadRequestException", "stageName is required")
		return
	}
	stage, ok := s.db.createStage(apiID, req.StageName, req.DeploymentID, req.Description, req.Variables)
	if !ok {
		notFound(w, apiID, "RestApi")
		return
	}
	jsonhttp.Write(w, http.StatusCreated, stage)
}

func (s *Service) getStages(w http.ResponseWriter, r *http.Request, apiID string) {
	stages, ok := s.db.listStages(apiID)
	if !ok {
		notFound(w, apiID, "RestApi")
		return
	}
	jsonhttp.Write(w, http.StatusOK, map[string]any{"item": stages})
}

func (s *Service) getStage(w http.ResponseWriter, r *http.Request, apiID, stageName string) {
	stage, ok := s.db.getStage(apiID, stageName)
	if !ok {
		notFound(w, stageName, "Stage")
		return
	}
	jsonhttp.Write(w, http.StatusOK, stage)
}

func (s *Service) deleteStage(w http.ResponseWriter, r *http.Request, apiID, stageName string) {
	if !s.db.deleteStage(apiID, stageName) {
		notFound(w, stageName, "Stage")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Service) updateStage(w http.ResponseWriter, r *http.Request, apiID, stageName string) {
	stage, ok := s.db.getStage(apiID, stageName)
	if !ok {
		notFound(w, stageName, "Stage")
		return
	}
	var req struct {
		DeploymentID string            `json:"deploymentId"`
		Description  string            `json:"description"`
		Variables    map[string]string `json:"variables"`
	}
	if err := jsonDecode(r, &req); err == nil {
		s.db.mu.Lock()
		if req.DeploymentID != "" {
			stage.DeploymentID = req.DeploymentID
		}
		stage.Description = req.Description
		if req.Variables != nil {
			stage.Variables = req.Variables
		}
		s.db.mu.Unlock()
	}
	jsonhttp.Write(w, http.StatusOK, stage)
}
