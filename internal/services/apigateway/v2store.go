package apigateway

import (
	"strings"
	"sync"
	"time"

	"github.com/nimbus-local/nimbus/internal/uid"
)

// HTTP API (v2) data models

type HTTPApi struct {
	ApiId        string `json:"apiId"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	ProtocolType string `json:"protocolType"` // always "HTTP"
	ApiEndpoint  string `json:"apiEndpoint"`
	CreatedDate  string `json:"createdDate"` // RFC3339
}

type V2Route struct {
	RouteId           string `json:"routeId"`
	RouteKey          string `json:"routeKey"`         // "GET /items/{id}" or "$default"
	Target            string `json:"target,omitempty"` // "integrations/{id}"
	AuthorizationType string `json:"authorizationType"`
}

type V2Integration struct {
	IntegrationId        string `json:"integrationId"`
	IntegrationType      string `json:"integrationType"` // AWS_PROXY, HTTP_PROXY
	IntegrationUri       string `json:"integrationUri,omitempty"`
	IntegrationMethod    string `json:"integrationMethod,omitempty"`
	PayloadFormatVersion string `json:"payloadFormatVersion,omitempty"` // "1.0" or "2.0"
	TimeoutInMillis      int    `json:"timeoutInMillis,omitempty"`
	Description          string `json:"description,omitempty"`
}

type V2Stage struct {
	StageName       string            `json:"stageName"`
	DeploymentId    string            `json:"deploymentId,omitempty"`
	AutoDeploy      bool              `json:"autoDeploy,omitempty"`
	Description     string            `json:"description,omitempty"`
	CreatedDate     string            `json:"createdDate"`
	LastUpdatedDate string            `json:"lastUpdatedDate"`
	StageVariables  map[string]string `json:"stageVariables,omitempty"`
}

type V2Deployment struct {
	DeploymentId     string `json:"deploymentId"`
	DeploymentStatus string `json:"deploymentStatus"` // DEPLOYED
	Description      string `json:"description,omitempty"`
	CreatedDate      string `json:"createdDate"`
}

type v2apiRecord struct {
	api          *HTTPApi
	routes       map[string]*V2Route
	integrations map[string]*V2Integration
	stages       map[string]*V2Stage
	deployments  map[string]*V2Deployment
}

type v2store struct {
	mu   sync.RWMutex
	apis map[string]*v2apiRecord
}

func newV2Store() *v2store {
	return &v2store{apis: make(map[string]*v2apiRecord)}
}

// Reset clears all HTTP API (v2) state.
func (s *v2store) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.apis = map[string]*v2apiRecord{}
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// API operations

func (s *v2store) createAPI(name, description string, port int) *HTTPApi {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := shortID()
	api := &HTTPApi{
		ApiId:        id,
		Name:         name,
		Description:  description,
		ProtocolType: "HTTP",
		ApiEndpoint:  apiEndpoint(id, port),
		CreatedDate:  nowRFC3339(),
	}
	s.apis[id] = &v2apiRecord{
		api:          api,
		routes:       make(map[string]*V2Route),
		integrations: make(map[string]*V2Integration),
		stages:       make(map[string]*V2Stage),
		deployments:  make(map[string]*V2Deployment),
	}
	return api
}

func apiEndpoint(apiID string, port int) string {
	if port == 0 {
		port = 4566
	}
	return "http://localhost:" + itoa(port) + "/apis/" + apiID
}

func itoa(n int) string {
	if n == 4566 {
		return "4566"
	}
	// enough for our purposes
	buf := make([]byte, 0, 5)
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	return string(buf)
}

func (s *v2store) getAPI(id string) (*HTTPApi, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.apis[id]
	if !ok {
		return nil, false
	}
	return rec.api, true
}

func (s *v2store) listAPIs() []*HTTPApi {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*HTTPApi, 0, len(s.apis))
	for _, rec := range s.apis {
		out = append(out, rec.api)
	}
	return out
}

func (s *v2store) deleteAPI(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.apis[id]
	delete(s.apis, id)
	return ok
}

// Route operations

func (s *v2store) createRoute(apiID, routeKey, target, authType string) (*V2Route, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return nil, false
	}
	if authType == "" {
		authType = "NONE"
	}
	r := &V2Route{
		RouteId:           shortID(),
		RouteKey:          routeKey,
		Target:            target,
		AuthorizationType: authType,
	}
	rec.routes[r.RouteId] = r
	return r, true
}

func (s *v2store) getRoute(apiID, routeID string) (*V2Route, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return nil, false
	}
	r, ok := rec.routes[routeID]
	return r, ok
}

func (s *v2store) listRoutes(apiID string) ([]*V2Route, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return nil, false
	}
	out := make([]*V2Route, 0, len(rec.routes))
	for _, r := range rec.routes {
		out = append(out, r)
	}
	return out, true
}

func (s *v2store) deleteRoute(apiID, routeID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return false
	}
	_, ok = rec.routes[routeID]
	delete(rec.routes, routeID)
	return ok
}

// Integration operations

func (s *v2store) createIntegration(apiID string, integ *V2Integration) (*V2Integration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return nil, false
	}
	if integ.PayloadFormatVersion == "" {
		integ.PayloadFormatVersion = "2.0"
	}
	if integ.TimeoutInMillis == 0 {
		integ.TimeoutInMillis = 30000
	}
	integ.IntegrationId = shortID()
	rec.integrations[integ.IntegrationId] = integ
	return integ, true
}

func (s *v2store) getIntegration(apiID, integID string) (*V2Integration, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return nil, false
	}
	i, ok := rec.integrations[integID]
	return i, ok
}

func (s *v2store) listIntegrations(apiID string) ([]*V2Integration, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return nil, false
	}
	out := make([]*V2Integration, 0, len(rec.integrations))
	for _, i := range rec.integrations {
		out = append(out, i)
	}
	return out, true
}

func (s *v2store) deleteIntegration(apiID, integID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return false
	}
	_, ok = rec.integrations[integID]
	delete(rec.integrations, integID)
	return ok
}

// Stage operations

func (s *v2store) createStage(apiID, stageName, deploymentID, description string, autoDeploy bool, variables map[string]string) (*V2Stage, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return nil, false
	}
	now := nowRFC3339()
	stage := &V2Stage{
		StageName:       stageName,
		DeploymentId:    deploymentID,
		AutoDeploy:      autoDeploy,
		Description:     description,
		CreatedDate:     now,
		LastUpdatedDate: now,
		StageVariables:  variables,
	}
	rec.stages[stageName] = stage
	return stage, true
}

func (s *v2store) getStage(apiID, stageName string) (*V2Stage, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return nil, false
	}
	st, ok := rec.stages[stageName]
	return st, ok
}

func (s *v2store) listStages(apiID string) ([]*V2Stage, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return nil, false
	}
	out := make([]*V2Stage, 0, len(rec.stages))
	for _, st := range rec.stages {
		out = append(out, st)
	}
	return out, true
}

func (s *v2store) deleteStage(apiID, stageName string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return false
	}
	_, ok = rec.stages[stageName]
	delete(rec.stages, stageName)
	return ok
}

// Deployment operations

func (s *v2store) createDeployment(apiID, description string) (*V2Deployment, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return nil, false
	}
	d := &V2Deployment{
		DeploymentId:     uid.New(),
		DeploymentStatus: "DEPLOYED",
		Description:      description,
		CreatedDate:      nowRFC3339(),
	}
	rec.deployments[d.DeploymentId] = d
	return d, true
}

func (s *v2store) getDeployment(apiID, deploymentID string) (*V2Deployment, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return nil, false
	}
	d, ok := rec.deployments[deploymentID]
	return d, ok
}

func (s *v2store) listDeployments(apiID string) ([]*V2Deployment, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return nil, false
	}
	out := make([]*V2Deployment, 0, len(rec.deployments))
	for _, d := range rec.deployments {
		out = append(out, d)
	}
	return out, true
}

func (s *v2store) deleteDeployment(apiID, deploymentID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return false
	}
	_, ok = rec.deployments[deploymentID]
	delete(rec.deployments, deploymentID)
	return ok
}

// findRouteForRequest matches a request (method + path) against the API's routes.
// Returns the matched route, path parameters, and the route's path pattern.
func (s *v2store) findRouteForRequest(apiID, method, path string) (*V2Route, map[string]string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return nil, nil, false
	}

	var defaultRoute *V2Route

	for _, route := range rec.routes {
		if route.RouteKey == "$default" {
			defaultRoute = route
			continue
		}
		routeMethod, routePath, ok := parseRouteKey(route.RouteKey)
		if !ok {
			continue
		}
		if routeMethod != "ANY" && !strings.EqualFold(routeMethod, method) {
			continue
		}
		if params, matched := matchPath(routePath, path); matched {
			return route, params, true
		}
	}

	if defaultRoute != nil {
		return defaultRoute, map[string]string{}, true
	}
	return nil, nil, false
}

// integrationForRoute resolves the integration referenced by a route's Target field.
func (s *v2store) integrationForRoute(apiID string, route *V2Route) (*V2Integration, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return nil, false
	}
	integID := strings.TrimPrefix(route.Target, "integrations/")
	integ, ok := rec.integrations[integID]
	return integ, ok
}

// parseRouteKey splits "GET /items/{id}" into ("GET", "/items/{id}", true).
func parseRouteKey(routeKey string) (method, path string, ok bool) {
	parts := strings.SplitN(routeKey, " ", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}
