package apigateway

import (
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

func (s *Service) createHTTPApi(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name         string `json:"name"`
		Description  string `json:"description"`
		ProtocolType string `json:"protocolType"`
	}
	if err := jsonDecode(r, &req); err != nil {
		apiError(w, http.StatusBadRequest, "BadRequestException", "invalid request body: "+err.Error())
		return
	}
	if req.Name == "" {
		apiError(w, http.StatusBadRequest, "BadRequestException", "name is required")
		return
	}
	api := s.v2.createAPI(req.Name, req.Description, 4566)
	jsonhttp.Write(w, http.StatusCreated, api)
}

func (s *Service) getHTTPApis(w http.ResponseWriter, r *http.Request) {
	apis := s.v2.listAPIs()
	jsonhttp.Write(w, http.StatusOK, map[string]any{"items": apis})
}

func (s *Service) getHTTPApi(w http.ResponseWriter, r *http.Request, apiID string) {
	api, ok := s.v2.getAPI(apiID)
	if !ok {
		notFound(w, apiID, "Api")
		return
	}
	jsonhttp.Write(w, http.StatusOK, api)
}

func (s *Service) deleteHTTPApi(w http.ResponseWriter, r *http.Request, apiID string) {
	if !s.v2.deleteAPI(apiID) {
		notFound(w, apiID, "Api")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) updateHTTPApi(w http.ResponseWriter, r *http.Request, apiID string) {
	api, ok := s.v2.getAPI(apiID)
	if !ok {
		notFound(w, apiID, "Api")
		return
	}
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := jsonDecode(r, &req); err == nil {
		s.v2.mu.Lock()
		if req.Name != "" {
			api.Name = req.Name
		}
		api.Description = req.Description
		s.v2.mu.Unlock()
	}
	jsonhttp.Write(w, http.StatusOK, api)
}
