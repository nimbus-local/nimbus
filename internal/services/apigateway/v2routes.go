package apigateway

import (
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

func (s *Service) createRoute(w http.ResponseWriter, r *http.Request, apiID string) {
	if _, ok := s.v2.getAPI(apiID); !ok {
		notFound(w, apiID, "Api")
		return
	}
	var req struct {
		RouteKey          string `json:"routeKey"`
		Target            string `json:"target"`
		AuthorizationType string `json:"authorizationType"`
	}
	if err := jsonDecode(r, &req); err != nil {
		apiError(w, http.StatusBadRequest, "BadRequestException", "invalid request body: "+err.Error())
		return
	}
	if req.RouteKey == "" {
		apiError(w, http.StatusBadRequest, "BadRequestException", "routeKey is required")
		return
	}
	route, ok := s.v2.createRoute(apiID, req.RouteKey, req.Target, req.AuthorizationType)
	if !ok {
		notFound(w, apiID, "Api")
		return
	}
	jsonhttp.Write(w, http.StatusCreated, route)
}

func (s *Service) getRoutes(w http.ResponseWriter, r *http.Request, apiID string) {
	routes, ok := s.v2.listRoutes(apiID)
	if !ok {
		notFound(w, apiID, "Api")
		return
	}
	jsonhttp.Write(w, http.StatusOK, map[string]any{"items": routes})
}

func (s *Service) getRoute(w http.ResponseWriter, r *http.Request, apiID, routeID string) {
	route, ok := s.v2.getRoute(apiID, routeID)
	if !ok {
		notFound(w, routeID, "Route")
		return
	}
	jsonhttp.Write(w, http.StatusOK, route)
}

func (s *Service) deleteRoute(w http.ResponseWriter, r *http.Request, apiID, routeID string) {
	if !s.v2.deleteRoute(apiID, routeID) {
		notFound(w, routeID, "Route")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) updateRoute(w http.ResponseWriter, r *http.Request, apiID, routeID string) {
	route, ok := s.v2.getRoute(apiID, routeID)
	if !ok {
		notFound(w, routeID, "Route")
		return
	}
	var req struct {
		RouteKey          string `json:"routeKey"`
		Target            string `json:"target"`
		AuthorizationType string `json:"authorizationType"`
	}
	if err := jsonDecode(r, &req); err == nil {
		s.v2.mu.Lock()
		if req.RouteKey != "" {
			route.RouteKey = req.RouteKey
		}
		if req.Target != "" {
			route.Target = req.Target
		}
		if req.AuthorizationType != "" {
			route.AuthorizationType = req.AuthorizationType
		}
		s.v2.mu.Unlock()
	}
	jsonhttp.Write(w, http.StatusOK, route)
}
