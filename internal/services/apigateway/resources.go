package apigateway

import (
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

func (s *Service) getResources(w http.ResponseWriter, r *http.Request, apiID string) {
	resources, ok := s.db.listResources(apiID)
	if !ok {
		notFound(w, apiID, "RestApi")
		return
	}
	jsonhttp.Write(w, http.StatusOK, map[string]any{"item": resources})
}

func (s *Service) getResource(w http.ResponseWriter, r *http.Request, apiID, resourceID string) {
	res, ok := s.db.getResource(apiID, resourceID)
	if !ok {
		notFound(w, resourceID, "Resource")
		return
	}
	jsonhttp.Write(w, http.StatusOK, res)
}

func (s *Service) createResource(w http.ResponseWriter, r *http.Request, apiID, parentID string) {
	var req struct {
		PathPart string `json:"pathPart"`
	}
	if err := jsonDecode(r, &req); err != nil {
		apiError(w, http.StatusBadRequest, "BadRequestException", "invalid request body: "+err.Error())
		return
	}
	if req.PathPart == "" {
		apiError(w, http.StatusBadRequest, "BadRequestException", "pathPart is required")
		return
	}
	res, ok := s.db.createResource(apiID, parentID, req.PathPart)
	if !ok {
		notFound(w, parentID, "Resource (parent)")
		return
	}
	jsonhttp.Write(w, http.StatusCreated, res)
}

func (s *Service) deleteResource(w http.ResponseWriter, r *http.Request, apiID, resourceID string) {
	if !s.db.deleteResource(apiID, resourceID) {
		notFound(w, resourceID, "Resource")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
