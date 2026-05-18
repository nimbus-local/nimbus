package apigateway

import (
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

func (s *Service) createRestAPI(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := jsonDecode(r, &req); err != nil {
		apiError(w, http.StatusBadRequest, "BadRequestException", "invalid request body: "+err.Error())
		return
	}
	if req.Name == "" {
		apiError(w, http.StatusBadRequest, "BadRequestException", "name is required")
		return
	}
	api := s.db.createAPI(req.Name, req.Description)
	jsonhttp.Write(w, http.StatusCreated, api)
}

func (s *Service) getRestAPIs(w http.ResponseWriter, r *http.Request) {
	apis := s.db.listAPIs()
	jsonhttp.Write(w, http.StatusOK, map[string]any{"item": apis})
}

func (s *Service) getRestAPI(w http.ResponseWriter, r *http.Request, apiID string) {
	api, ok := s.db.getAPI(apiID)
	if !ok {
		notFound(w, apiID, "RestApi")
		return
	}
	jsonhttp.Write(w, http.StatusOK, api)
}

func (s *Service) deleteRestAPI(w http.ResponseWriter, r *http.Request, apiID string) {
	if !s.db.deleteAPI(apiID) {
		notFound(w, apiID, "RestApi")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Service) updateRestAPI(w http.ResponseWriter, r *http.Request, apiID string) {
	api, ok := s.db.getAPI(apiID)
	if !ok {
		notFound(w, apiID, "RestApi")
		return
	}
	// Minimal patch support: name and description via JSON merge patch.
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := jsonDecode(r, &req); err == nil {
		s.db.mu.Lock()
		if req.Name != "" {
			api.Name = req.Name
		}
		api.Description = req.Description
		s.db.mu.Unlock()
	}
	jsonhttp.Write(w, http.StatusOK, api)
}
