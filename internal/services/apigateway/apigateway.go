// Package apigateway emulates the AWS API Gateway REST API management plane
// and the execute-api runtime. The management API is served under /restapis/;
// invocations arrive as /restapis/{apiId}/{stage}/_user_request_/{proxy+}.
package apigateway

import (
	"net/http"
	"strings"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

// LambdaInvoker is implemented by the Lambda invocation service so API Gateway
// can forward AWS_PROXY requests without an HTTP round-trip.
type LambdaInvoker interface {
	DirectInvoke(functionName string, payload []byte) ([]byte, error)
}

// Service is the top-level API Gateway emulator.
// It handles both REST API (v1, /restapis/) and HTTP API (v2, /apis/).
type Service struct {
	region  string
	account string
	db      *store   // REST API (v1) state
	v2      *v2store // HTTP API (v2) state
	lambda  LambdaInvoker
}

func New(region string, lambda LambdaInvoker) *Service {
	if region == "" {
		region = "us-east-1"
	}
	return &Service{
		region:  region,
		account: defaultAccount,
		db:      newStore(),
		v2:      newV2Store(),
		lambda:  lambda,
	}
}

func (s *Service) Name() string { return "apigateway" }

// Detect claims /restapis/* (REST API v1) and /apis/* (HTTP API v2).
// AWS SDK Go v2 (used by Pulumi) prefixes HTTP API paths with /v2/.
func (s *Service) Detect(r *http.Request) bool {
	p := r.URL.Path
	return p == "/restapis" || strings.HasPrefix(p, "/restapis/") ||
		p == "/apis" || strings.HasPrefix(p, "/apis/") ||
		p == "/v2/apis" || strings.HasPrefix(p, "/v2/apis/")
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Strip /v2 prefix — AWS SDK Go v2 sends /v2/apis/... instead of /apis/...
	if strings.HasPrefix(r.URL.Path, "/v2/") {
		r2 := *r
		u2 := *r.URL
		u2.Path = r.URL.Path[3:]
		r2.URL = &u2
		r = &r2
	}

	if strings.HasPrefix(r.URL.Path, "/apis") {
		s.serveV2(w, r)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/restapis")

	if path == "" || path == "/" {
		switch r.Method {
		case http.MethodPost:
			s.createRestAPI(w, r)
		case http.MethodGet:
			s.getRestAPIs(w, r)
		default:
			apiError(w, http.StatusMethodNotAllowed, "MethodNotAllowedException", "Method not allowed")
		}
		return
	}

	path = strings.TrimPrefix(path, "/")
	apiID, rest, _ := strings.Cut(path, "/")

	if rest == "" {
		switch r.Method {
		case http.MethodGet:
			s.getRestAPI(w, r, apiID)
		case http.MethodDelete:
			s.deleteRestAPI(w, r, apiID)
		case http.MethodPatch:
			s.updateRestAPI(w, r, apiID)
		default:
			apiError(w, http.StatusMethodNotAllowed, "MethodNotAllowedException", "Method not allowed")
		}
		return
	}

	segment, rest2, _ := strings.Cut(rest, "/")

	switch segment {
	case "resources":
		s.routeResources(w, r, apiID, rest2)
	case "deployments":
		s.routeDeployments(w, r, apiID, rest2)
	case "stages":
		s.routeStages(w, r, apiID, rest2)
	default:
		// /{stage}/_user_request_/{proxy+}
		stage := segment
		if strings.HasPrefix(rest2, "_user_request_") || rest2 == "_user_request_" {
			proxyPath := strings.TrimPrefix(rest2, "_user_request_")
			if proxyPath == "" {
				proxyPath = "/"
			} else {
				proxyPath = strings.TrimPrefix(proxyPath, "/")
				proxyPath = "/" + proxyPath
			}
			s.execute(w, r, apiID, stage, proxyPath)
		} else {
			apiError(w, http.StatusNotFound, "NotFoundException", "Invalid resource path: "+r.URL.Path)
		}
	}
}

func (s *Service) routeResources(w http.ResponseWriter, r *http.Request, apiID, rest string) {
	if rest == "" {
		if r.Method == http.MethodGet {
			s.getResources(w, r, apiID)
		} else {
			apiError(w, http.StatusMethodNotAllowed, "MethodNotAllowedException", "Method not allowed")
		}
		return
	}

	resourceID, suffix, _ := strings.Cut(rest, "/")

	if suffix == "" {
		switch r.Method {
		case http.MethodGet:
			s.getResource(w, r, apiID, resourceID)
		case http.MethodPost:
			s.createResource(w, r, apiID, resourceID) // resourceID is parentId
		case http.MethodDelete:
			s.deleteResource(w, r, apiID, resourceID)
		default:
			apiError(w, http.StatusMethodNotAllowed, "MethodNotAllowedException", "Method not allowed")
		}
		return
	}

	if !strings.HasPrefix(suffix, "methods/") {
		apiError(w, http.StatusNotFound, "NotFoundException", "Invalid resource path: "+r.URL.Path)
		return
	}

	methodRest := strings.TrimPrefix(suffix, "methods/")
	httpMethod, methodSuffix, _ := strings.Cut(methodRest, "/")
	httpMethod = strings.ToUpper(httpMethod)

	if methodSuffix == "" {
		switch r.Method {
		case http.MethodPut:
			s.putMethod(w, r, apiID, resourceID, httpMethod)
		case http.MethodGet:
			s.getMethod(w, r, apiID, resourceID, httpMethod)
		case http.MethodDelete:
			s.deleteMethod(w, r, apiID, resourceID, httpMethod)
		default:
			apiError(w, http.StatusMethodNotAllowed, "MethodNotAllowedException", "Method not allowed")
		}
		return
	}

	switch {
	case methodSuffix == "integration":
		switch r.Method {
		case http.MethodPut:
			s.putIntegration(w, r, apiID, resourceID, httpMethod)
		case http.MethodGet:
			s.getIntegration(w, r, apiID, resourceID, httpMethod)
		case http.MethodDelete:
			s.deleteIntegration(w, r, apiID, resourceID, httpMethod)
		default:
			apiError(w, http.StatusMethodNotAllowed, "MethodNotAllowedException", "Method not allowed")
		}
	case strings.HasPrefix(methodSuffix, "responses/"):
		statusCode := strings.TrimPrefix(methodSuffix, "responses/")
		switch r.Method {
		case http.MethodPut:
			s.putMethodResponse(w, r, apiID, resourceID, httpMethod, statusCode)
		case http.MethodGet:
			s.getMethodResponse(w, r, apiID, resourceID, httpMethod, statusCode)
		case http.MethodDelete:
			s.deleteMethodResponse(w, r, apiID, resourceID, httpMethod, statusCode)
		default:
			apiError(w, http.StatusMethodNotAllowed, "MethodNotAllowedException", "Method not allowed")
		}
	case strings.HasPrefix(methodSuffix, "integration/responses/"):
		statusCode := strings.TrimPrefix(methodSuffix, "integration/responses/")
		switch r.Method {
		case http.MethodPut:
			s.putIntegrationResponse(w, r, apiID, resourceID, httpMethod, statusCode)
		case http.MethodGet:
			s.getIntegrationResponse(w, r, apiID, resourceID, httpMethod, statusCode)
		case http.MethodDelete:
			s.deleteIntegrationResponse(w, r, apiID, resourceID, httpMethod, statusCode)
		default:
			apiError(w, http.StatusMethodNotAllowed, "MethodNotAllowedException", "Method not allowed")
		}
	default:
		apiError(w, http.StatusNotFound, "NotFoundException", "Invalid resource path: "+r.URL.Path)
	}
}

func (s *Service) routeDeployments(w http.ResponseWriter, r *http.Request, apiID, rest string) {
	if rest == "" {
		switch r.Method {
		case http.MethodPost:
			s.createDeployment(w, r, apiID)
		case http.MethodGet:
			s.getDeployments(w, r, apiID)
		default:
			apiError(w, http.StatusMethodNotAllowed, "MethodNotAllowedException", "Method not allowed")
		}
		return
	}
	deploymentID := strings.TrimSuffix(rest, "/")
	switch r.Method {
	case http.MethodGet:
		s.getDeployment(w, r, apiID, deploymentID)
	case http.MethodDelete:
		s.deleteDeployment(w, r, apiID, deploymentID)
	default:
		apiError(w, http.StatusMethodNotAllowed, "MethodNotAllowedException", "Method not allowed")
	}
}

func (s *Service) routeStages(w http.ResponseWriter, r *http.Request, apiID, rest string) {
	if rest == "" {
		switch r.Method {
		case http.MethodPost:
			s.createStage(w, r, apiID)
		case http.MethodGet:
			s.getStages(w, r, apiID)
		default:
			apiError(w, http.StatusMethodNotAllowed, "MethodNotAllowedException", "Method not allowed")
		}
		return
	}
	stageName := strings.TrimSuffix(rest, "/")
	switch r.Method {
	case http.MethodGet:
		s.getStage(w, r, apiID, stageName)
	case http.MethodDelete:
		s.deleteStage(w, r, apiID, stageName)
	case http.MethodPatch:
		s.updateStage(w, r, apiID, stageName)
	default:
		apiError(w, http.StatusMethodNotAllowed, "MethodNotAllowedException", "Method not allowed")
	}
}

// serveV2 routes HTTP API (v2) requests under /apis/
func (s *Service) serveV2(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/apis")

	if path == "" || path == "/" {
		switch r.Method {
		case http.MethodPost:
			s.createHTTPApi(w, r)
		case http.MethodGet:
			s.getHTTPApis(w, r)
		default:
			apiError(w, http.StatusMethodNotAllowed, "MethodNotAllowedException", "Method not allowed")
		}
		return
	}

	path = strings.TrimPrefix(path, "/")
	apiID, rest, _ := strings.Cut(path, "/")

	if rest == "" {
		switch r.Method {
		case http.MethodGet:
			s.getHTTPApi(w, r, apiID)
		case http.MethodDelete:
			s.deleteHTTPApi(w, r, apiID)
		case http.MethodPatch:
			s.updateHTTPApi(w, r, apiID)
		default:
			apiError(w, http.StatusMethodNotAllowed, "MethodNotAllowedException", "Method not allowed")
		}
		return
	}

	segment, rest2, _ := strings.Cut(rest, "/")

	switch segment {
	case "routes":
		s.routeV2Routes(w, r, apiID, rest2)
	case "integrations":
		s.routeV2Integrations(w, r, apiID, rest2)
	case "stages":
		s.routeV2Stages(w, r, apiID, rest2)
	case "deployments":
		s.routeV2Deployments(w, r, apiID, rest2)
	default:
		// /{stage}/_user_request_/{proxy+}
		stage := segment
		if strings.HasPrefix(rest2, "_user_request_") || rest2 == "_user_request_" {
			proxyPath := strings.TrimPrefix(rest2, "_user_request_")
			if proxyPath == "" {
				proxyPath = "/"
			} else {
				proxyPath = strings.TrimPrefix(proxyPath, "/")
				proxyPath = "/" + proxyPath
			}
			s.executeV2(w, r, apiID, stage, proxyPath)
		} else {
			apiError(w, http.StatusNotFound, "NotFoundException", "Invalid path: "+r.URL.Path)
		}
	}
}

func (s *Service) routeV2Routes(w http.ResponseWriter, r *http.Request, apiID, rest string) {
	if rest == "" {
		switch r.Method {
		case http.MethodPost:
			s.createRoute(w, r, apiID)
		case http.MethodGet:
			s.getRoutes(w, r, apiID)
		default:
			apiError(w, http.StatusMethodNotAllowed, "MethodNotAllowedException", "Method not allowed")
		}
		return
	}
	routeID := strings.TrimSuffix(rest, "/")
	switch r.Method {
	case http.MethodGet:
		s.getRoute(w, r, apiID, routeID)
	case http.MethodDelete:
		s.deleteRoute(w, r, apiID, routeID)
	case http.MethodPatch:
		s.updateRoute(w, r, apiID, routeID)
	default:
		apiError(w, http.StatusMethodNotAllowed, "MethodNotAllowedException", "Method not allowed")
	}
}

func (s *Service) routeV2Integrations(w http.ResponseWriter, r *http.Request, apiID, rest string) {
	if rest == "" {
		switch r.Method {
		case http.MethodPost:
			s.createIntegration(w, r, apiID)
		case http.MethodGet:
			s.getIntegrations(w, r, apiID)
		default:
			apiError(w, http.StatusMethodNotAllowed, "MethodNotAllowedException", "Method not allowed")
		}
		return
	}
	integID := strings.TrimSuffix(rest, "/")
	switch r.Method {
	case http.MethodGet:
		s.getIntegrationV2(w, r, apiID, integID)
	case http.MethodDelete:
		s.deleteIntegrationV2(w, r, apiID, integID)
	case http.MethodPatch:
		s.updateIntegration(w, r, apiID, integID)
	default:
		apiError(w, http.StatusMethodNotAllowed, "MethodNotAllowedException", "Method not allowed")
	}
}

func (s *Service) routeV2Stages(w http.ResponseWriter, r *http.Request, apiID, rest string) {
	if rest == "" {
		switch r.Method {
		case http.MethodPost:
			s.createV2Stage(w, r, apiID)
		case http.MethodGet:
			s.getV2Stages(w, r, apiID)
		default:
			apiError(w, http.StatusMethodNotAllowed, "MethodNotAllowedException", "Method not allowed")
		}
		return
	}
	stageName := strings.TrimSuffix(rest, "/")
	switch r.Method {
	case http.MethodGet:
		s.getV2Stage(w, r, apiID, stageName)
	case http.MethodDelete:
		s.deleteV2Stage(w, r, apiID, stageName)
	case http.MethodPatch:
		s.updateV2Stage(w, r, apiID, stageName)
	default:
		apiError(w, http.StatusMethodNotAllowed, "MethodNotAllowedException", "Method not allowed")
	}
}

func (s *Service) routeV2Deployments(w http.ResponseWriter, r *http.Request, apiID, rest string) {
	if rest == "" {
		switch r.Method {
		case http.MethodPost:
			s.createV2Deployment(w, r, apiID)
		case http.MethodGet:
			s.getV2Deployments(w, r, apiID)
		default:
			apiError(w, http.StatusMethodNotAllowed, "MethodNotAllowedException", "Method not allowed")
		}
		return
	}
	deploymentID := strings.TrimSuffix(rest, "/")
	switch r.Method {
	case http.MethodGet:
		s.getV2Deployment(w, r, apiID, deploymentID)
	case http.MethodDelete:
		s.deleteV2Deployment(w, r, apiID, deploymentID)
	default:
		apiError(w, http.StatusMethodNotAllowed, "MethodNotAllowedException", "Method not allowed")
	}
}

func apiError(w http.ResponseWriter, status int, code, message string) {
	jsonhttp.Error(w, status, code, message)
}

func notFound(w http.ResponseWriter, id, kind string) {
	jsonhttp.Error(w, http.StatusNotFound, "NotFoundException",
		"Invalid "+kind+": "+id)
}
