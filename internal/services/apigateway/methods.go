package apigateway

import (
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

func (s *Service) putMethod(w http.ResponseWriter, r *http.Request, apiID, resourceID, httpMethod string) {
	var req struct {
		AuthorizationType string          `json:"authorizationType"`
		RequestParameters map[string]bool `json:"requestParameters"`
	}
	if err := jsonDecode(r, &req); err != nil {
		apiError(w, http.StatusBadRequest, "BadRequestException", "invalid request body: "+err.Error())
		return
	}
	authType := req.AuthorizationType
	if authType == "" {
		authType = "NONE"
	}
	m := &Method{
		HttpMethod:        httpMethod,
		AuthorizationType: authType,
		RequestParameters: req.RequestParameters,
	}
	if !s.db.putMethod(apiID, resourceID, httpMethod, m) {
		notFound(w, resourceID, "Resource")
		return
	}
	jsonhttp.Write(w, http.StatusCreated, m)
}

func (s *Service) getMethod(w http.ResponseWriter, r *http.Request, apiID, resourceID, httpMethod string) {
	m, ok := s.db.getMethod(apiID, resourceID, httpMethod)
	if !ok {
		notFound(w, httpMethod, "Method")
		return
	}
	jsonhttp.Write(w, http.StatusOK, m)
}

func (s *Service) deleteMethod(w http.ResponseWriter, r *http.Request, apiID, resourceID, httpMethod string) {
	if !s.db.deleteMethod(apiID, resourceID, httpMethod) {
		notFound(w, httpMethod, "Method")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) putIntegration(w http.ResponseWriter, r *http.Request, apiID, resourceID, httpMethod string) {
	var req struct {
		Type                string            `json:"type"`
		HttpMethod          string            `json:"httpMethod"`
		Uri                 string            `json:"uri"`
		PassthroughBehavior string            `json:"passthroughBehavior"`
		RequestTemplates    map[string]string `json:"requestTemplates"`
		ContentHandling     string            `json:"contentHandling"`
		TimeoutInMillis     int               `json:"timeoutInMillis"`
	}
	if err := jsonDecode(r, &req); err != nil {
		apiError(w, http.StatusBadRequest, "BadRequestException", "invalid request body: "+err.Error())
		return
	}
	if req.Type == "" {
		apiError(w, http.StatusBadRequest, "BadRequestException", "type is required")
		return
	}
	timeout := req.TimeoutInMillis
	if timeout == 0 {
		timeout = 29000
	}
	integ := &Integration{
		Type:                req.Type,
		HttpMethod:          req.HttpMethod,
		Uri:                 req.Uri,
		PassthroughBehavior: req.PassthroughBehavior,
		RequestTemplates:    req.RequestTemplates,
		ContentHandling:     req.ContentHandling,
		TimeoutInMillis:     timeout,
	}
	if !s.db.putIntegration(apiID, resourceID, httpMethod, integ) {
		notFound(w, httpMethod, "Method")
		return
	}
	jsonhttp.Write(w, http.StatusCreated, integ)
}

func (s *Service) getIntegration(w http.ResponseWriter, r *http.Request, apiID, resourceID, httpMethod string) {
	integ, ok := s.db.getIntegration(apiID, resourceID, httpMethod)
	if !ok {
		notFound(w, httpMethod, "Integration")
		return
	}
	jsonhttp.Write(w, http.StatusOK, integ)
}

func (s *Service) deleteIntegration(w http.ResponseWriter, r *http.Request, apiID, resourceID, httpMethod string) {
	if !s.db.deleteIntegration(apiID, resourceID, httpMethod) {
		notFound(w, httpMethod, "Integration")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) putMethodResponse(w http.ResponseWriter, r *http.Request, apiID, resourceID, httpMethod, statusCode string) {
	var req struct {
		ResponseParameters map[string]bool   `json:"responseParameters"`
		ResponseModels     map[string]string `json:"responseModels"`
	}
	// Body is optional; ignore EOF.
	jsonDecode(r, &req) //nolint:errcheck
	mr := &MethodResponse{
		StatusCode:         statusCode,
		ResponseParameters: req.ResponseParameters,
		ResponseModels:     req.ResponseModels,
	}
	if !s.db.putMethodResponse(apiID, resourceID, httpMethod, statusCode, mr) {
		notFound(w, httpMethod, "Method")
		return
	}
	jsonhttp.Write(w, http.StatusCreated, mr)
}

func (s *Service) getMethodResponse(w http.ResponseWriter, r *http.Request, apiID, resourceID, httpMethod, statusCode string) {
	mr, ok := s.db.getMethodResponse(apiID, resourceID, httpMethod, statusCode)
	if !ok {
		notFound(w, statusCode, "MethodResponse")
		return
	}
	jsonhttp.Write(w, http.StatusOK, mr)
}

func (s *Service) deleteMethodResponse(w http.ResponseWriter, r *http.Request, apiID, resourceID, httpMethod, statusCode string) {
	if !s.db.deleteMethodResponse(apiID, resourceID, httpMethod, statusCode) {
		notFound(w, statusCode, "MethodResponse")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) putIntegrationResponse(w http.ResponseWriter, r *http.Request, apiID, resourceID, httpMethod, statusCode string) {
	var req struct {
		SelectionPattern   string            `json:"selectionPattern"`
		ResponseParameters map[string]string `json:"responseParameters"`
		ResponseTemplates  map[string]string `json:"responseTemplates"`
		ContentHandling    string            `json:"contentHandling"`
	}
	if err := jsonDecode(r, &req); err != nil {
		apiError(w, http.StatusBadRequest, "BadRequestException", "invalid request body: "+err.Error())
		return
	}
	ir := &IntegrationResponse{
		StatusCode:         statusCode,
		SelectionPattern:   req.SelectionPattern,
		ResponseParameters: req.ResponseParameters,
		ResponseTemplates:  req.ResponseTemplates,
		ContentHandling:    req.ContentHandling,
	}
	if !s.db.putIntegrationResponse(apiID, resourceID, httpMethod, statusCode, ir) {
		notFound(w, httpMethod, "Integration")
		return
	}
	jsonhttp.Write(w, http.StatusCreated, ir)
}

func (s *Service) getIntegrationResponse(w http.ResponseWriter, r *http.Request, apiID, resourceID, httpMethod, statusCode string) {
	ir, ok := s.db.getIntegrationResponse(apiID, resourceID, httpMethod, statusCode)
	if !ok {
		notFound(w, statusCode, "IntegrationResponse")
		return
	}
	jsonhttp.Write(w, http.StatusOK, ir)
}

func (s *Service) deleteIntegrationResponse(w http.ResponseWriter, r *http.Request, apiID, resourceID, httpMethod, statusCode string) {
	if !s.db.deleteIntegrationResponse(apiID, resourceID, httpMethod, statusCode) {
		notFound(w, statusCode, "IntegrationResponse")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
