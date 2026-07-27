package apigateway

import (
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

func (s *Service) createIntegration(w http.ResponseWriter, r *http.Request, apiID string) {
	if _, ok := s.v2.getAPI(apiID); !ok {
		notFound(w, apiID, "Api")
		return
	}
	var req V2Integration
	if err := jsonDecode(r, &req); err != nil {
		apiError(w, http.StatusBadRequest, "BadRequestException", "invalid request body: "+err.Error())
		return
	}
	if req.IntegrationType == "" {
		apiError(w, http.StatusBadRequest, "BadRequestException", "integrationType is required")
		return
	}
	if req.ConnectionType == "" {
		req.ConnectionType = "INTERNET" // the AWS default
	}
	integ, ok := s.v2.createIntegration(apiID, &req)
	if !ok {
		notFound(w, apiID, "Api")
		return
	}
	jsonhttp.Write(w, http.StatusCreated, integ)
}

func (s *Service) getIntegrations(w http.ResponseWriter, r *http.Request, apiID string) {
	integrations, ok := s.v2.listIntegrations(apiID)
	if !ok {
		notFound(w, apiID, "Api")
		return
	}
	jsonhttp.Write(w, http.StatusOK, map[string]any{"items": integrations})
}

func (s *Service) getIntegrationV2(w http.ResponseWriter, r *http.Request, apiID, integID string) {
	integ, ok := s.v2.getIntegration(apiID, integID)
	if !ok {
		notFound(w, integID, "Integration")
		return
	}
	jsonhttp.Write(w, http.StatusOK, integ)
}

func (s *Service) deleteIntegrationV2(w http.ResponseWriter, r *http.Request, apiID, integID string) {
	if !s.v2.deleteIntegration(apiID, integID) {
		notFound(w, integID, "Integration")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) updateIntegration(w http.ResponseWriter, r *http.Request, apiID, integID string) {
	integ, ok := s.v2.getIntegration(apiID, integID)
	if !ok {
		notFound(w, integID, "Integration")
		return
	}
	var req V2Integration
	if err := jsonDecode(r, &req); err == nil {
		s.v2.mu.Lock()
		if req.ConnectionType != "" {
			integ.ConnectionType = req.ConnectionType
		}
		if req.IntegrationType != "" {
			integ.IntegrationType = req.IntegrationType
		}
		if req.IntegrationUri != "" {
			integ.IntegrationUri = req.IntegrationUri
		}
		if req.IntegrationMethod != "" {
			integ.IntegrationMethod = req.IntegrationMethod
		}
		if req.PayloadFormatVersion != "" {
			integ.PayloadFormatVersion = req.PayloadFormatVersion
		}
		if req.TimeoutInMillis != 0 {
			integ.TimeoutInMillis = req.TimeoutInMillis
		}
		s.v2.mu.Unlock()
	}
	jsonhttp.Write(w, http.StatusOK, integ)
}
