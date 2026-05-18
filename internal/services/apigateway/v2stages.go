package apigateway

import (
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

func (s *Service) createV2Stage(w http.ResponseWriter, r *http.Request, apiID string) {
	if _, ok := s.v2.getAPI(apiID); !ok {
		notFound(w, apiID, "Api")
		return
	}
	var req struct {
		StageName      string            `json:"stageName"`
		DeploymentId   string            `json:"deploymentId"`
		AutoDeploy     bool              `json:"autoDeploy"`
		Description    string            `json:"description"`
		StageVariables map[string]string `json:"stageVariables"`
	}
	if err := jsonDecode(r, &req); err != nil {
		apiError(w, http.StatusBadRequest, "BadRequestException", "invalid request body: "+err.Error())
		return
	}
	if req.StageName == "" {
		apiError(w, http.StatusBadRequest, "BadRequestException", "stageName is required")
		return
	}
	stage, ok := s.v2.createStage(apiID, req.StageName, req.DeploymentId, req.Description, req.AutoDeploy, req.StageVariables)
	if !ok {
		notFound(w, apiID, "Api")
		return
	}
	jsonhttp.Write(w, http.StatusCreated, stage)
}

func (s *Service) getV2Stages(w http.ResponseWriter, r *http.Request, apiID string) {
	stages, ok := s.v2.listStages(apiID)
	if !ok {
		notFound(w, apiID, "Api")
		return
	}
	jsonhttp.Write(w, http.StatusOK, map[string]any{"items": stages})
}

func (s *Service) getV2Stage(w http.ResponseWriter, r *http.Request, apiID, stageName string) {
	stage, ok := s.v2.getStage(apiID, stageName)
	if !ok {
		notFound(w, stageName, "Stage")
		return
	}
	jsonhttp.Write(w, http.StatusOK, stage)
}

func (s *Service) deleteV2Stage(w http.ResponseWriter, r *http.Request, apiID, stageName string) {
	if !s.v2.deleteStage(apiID, stageName) {
		notFound(w, stageName, "Stage")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) updateV2Stage(w http.ResponseWriter, r *http.Request, apiID, stageName string) {
	stage, ok := s.v2.getStage(apiID, stageName)
	if !ok {
		notFound(w, stageName, "Stage")
		return
	}
	var req struct {
		DeploymentId   string            `json:"deploymentId"`
		AutoDeploy     bool              `json:"autoDeploy"`
		Description    string            `json:"description"`
		StageVariables map[string]string `json:"stageVariables"`
	}
	if err := jsonDecode(r, &req); err == nil {
		s.v2.mu.Lock()
		if req.DeploymentId != "" {
			stage.DeploymentId = req.DeploymentId
		}
		stage.AutoDeploy = req.AutoDeploy
		stage.Description = req.Description
		if req.StageVariables != nil {
			stage.StageVariables = req.StageVariables
		}
		stage.LastUpdatedDate = nowRFC3339()
		s.v2.mu.Unlock()
	}
	jsonhttp.Write(w, http.StatusOK, stage)
}

func (s *Service) createV2Deployment(w http.ResponseWriter, r *http.Request, apiID string) {
	if _, ok := s.v2.getAPI(apiID); !ok {
		notFound(w, apiID, "Api")
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
	d, ok := s.v2.createDeployment(apiID, req.Description)
	if !ok {
		notFound(w, apiID, "Api")
		return
	}
	// Wire the deployment to the stage if named.
	if req.StageName != "" {
		if stage, ok := s.v2.getStage(apiID, req.StageName); ok {
			s.v2.mu.Lock()
			stage.DeploymentId = d.DeploymentId
			stage.LastUpdatedDate = nowRFC3339()
			s.v2.mu.Unlock()
		}
	}
	jsonhttp.Write(w, http.StatusCreated, d)
}

func (s *Service) getV2Deployments(w http.ResponseWriter, r *http.Request, apiID string) {
	deployments, ok := s.v2.listDeployments(apiID)
	if !ok {
		notFound(w, apiID, "Api")
		return
	}
	jsonhttp.Write(w, http.StatusOK, map[string]any{"items": deployments})
}

func (s *Service) getV2Deployment(w http.ResponseWriter, r *http.Request, apiID, deploymentID string) {
	d, ok := s.v2.getDeployment(apiID, deploymentID)
	if !ok {
		notFound(w, deploymentID, "Deployment")
		return
	}
	jsonhttp.Write(w, http.StatusOK, d)
}

func (s *Service) deleteV2Deployment(w http.ResponseWriter, r *http.Request, apiID, deploymentID string) {
	if !s.v2.deleteDeployment(apiID, deploymentID) {
		notFound(w, deploymentID, "Deployment")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
