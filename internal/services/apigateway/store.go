package apigateway

import (
	"strings"
	"sync"
	"time"

	"github.com/nimbus-local/nimbus/internal/uid"
)

const defaultAccount = "000000000000"

// shortID returns a 10-char hex ID matching AWS API Gateway's format.
func shortID() string {
	return strings.ReplaceAll(uid.New(), "-", "")[:10]
}

type RestAPI struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	CreatedDate int64  `json:"createdDate"`
}

type Resource struct {
	ID              string             `json:"id"`
	ParentID        string             `json:"parentId,omitempty"`
	PathPart        string             `json:"pathPart,omitempty"`
	Path            string             `json:"path"`
	ResourceMethods map[string]*Method `json:"resourceMethods,omitempty"`
}

type Method struct {
	HttpMethod        string                     `json:"httpMethod"`
	AuthorizationType string                     `json:"authorizationType"`
	RequestParameters map[string]bool            `json:"requestParameters,omitempty"`
	MethodIntegration *Integration               `json:"methodIntegration,omitempty"`
	MethodResponses   map[string]*MethodResponse `json:"methodResponses,omitempty"`
}

type Integration struct {
	Type                 string                          `json:"type"`
	HttpMethod           string                          `json:"httpMethod,omitempty"`
	Uri                  string                          `json:"uri,omitempty"`
	PassthroughBehavior  string                          `json:"passthroughBehavior,omitempty"`
	RequestTemplates     map[string]string               `json:"requestTemplates,omitempty"`
	ContentHandling      string                          `json:"contentHandling,omitempty"`
	TimeoutInMillis      int                             `json:"timeoutInMillis,omitempty"`
	IntegrationResponses map[string]*IntegrationResponse `json:"integrationResponses,omitempty"`
}

type MethodResponse struct {
	StatusCode         string            `json:"statusCode"`
	ResponseParameters map[string]bool   `json:"responseParameters,omitempty"`
	ResponseModels     map[string]string `json:"responseModels,omitempty"`
}

type IntegrationResponse struct {
	StatusCode         string            `json:"statusCode"`
	SelectionPattern   string            `json:"selectionPattern,omitempty"`
	ResponseParameters map[string]string `json:"responseParameters,omitempty"`
	ResponseTemplates  map[string]string `json:"responseTemplates,omitempty"`
	ContentHandling    string            `json:"contentHandling,omitempty"`
}

type Deployment struct {
	ID          string `json:"id"`
	Description string `json:"description,omitempty"`
	CreatedDate int64  `json:"createdDate"`
}

type Stage struct {
	StageName       string            `json:"stageName"`
	DeploymentID    string            `json:"deploymentId"`
	Description     string            `json:"description,omitempty"`
	CreatedDate     int64             `json:"createdDate"`
	LastUpdatedDate int64             `json:"lastUpdatedDate"`
	Variables       map[string]string `json:"variables,omitempty"`
}

type apiRecord struct {
	api         *RestAPI
	resources   map[string]*Resource
	deployments map[string]*Deployment
	stages      map[string]*Stage
}

type store struct {
	mu   sync.RWMutex
	apis map[string]*apiRecord
}

func newStore() *store {
	return &store{apis: make(map[string]*apiRecord)}
}

// Reset clears all REST API (v1) state.
func (s *store) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.apis = map[string]*apiRecord{}
}

func (s *store) createAPI(name, description string) *RestAPI {
	s.mu.Lock()
	defer s.mu.Unlock()
	api := &RestAPI{
		ID:          shortID(),
		Name:        name,
		Description: description,
		CreatedDate: time.Now().Unix(),
	}
	root := &Resource{ID: shortID(), Path: "/"}
	s.apis[api.ID] = &apiRecord{
		api:         api,
		resources:   map[string]*Resource{root.ID: root},
		deployments: map[string]*Deployment{},
		stages:      map[string]*Stage{},
	}
	return api
}

func (s *store) getAPI(id string) (*RestAPI, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.apis[id]
	if !ok {
		return nil, false
	}
	return rec.api, true
}

func (s *store) listAPIs() []*RestAPI {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*RestAPI, 0, len(s.apis))
	for _, rec := range s.apis {
		out = append(out, rec.api)
	}
	return out
}

func (s *store) deleteAPI(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.apis[id]
	delete(s.apis, id)
	return ok
}

func (s *store) createResource(apiID, parentID, pathPart string) (*Resource, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return nil, false
	}
	parent, ok := rec.resources[parentID]
	if !ok {
		return nil, false
	}
	path := parent.Path
	if path == "/" {
		path = "/" + pathPart
	} else {
		path = path + "/" + pathPart
	}
	r := &Resource{
		ID:       shortID(),
		ParentID: parentID,
		PathPart: pathPart,
		Path:     path,
	}
	rec.resources[r.ID] = r
	return r, true
}

func (s *store) getResource(apiID, resourceID string) (*Resource, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return nil, false
	}
	r, ok := rec.resources[resourceID]
	return r, ok
}

func (s *store) listResources(apiID string) ([]*Resource, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return nil, false
	}
	out := make([]*Resource, 0, len(rec.resources))
	for _, r := range rec.resources {
		out = append(out, r)
	}
	return out, true
}

func (s *store) deleteResource(apiID, resourceID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return false
	}
	_, ok = rec.resources[resourceID]
	delete(rec.resources, resourceID)
	return ok
}

func (s *store) putMethod(apiID, resourceID, httpMethod string, m *Method) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return false
	}
	res, ok := rec.resources[resourceID]
	if !ok {
		return false
	}
	if res.ResourceMethods == nil {
		res.ResourceMethods = map[string]*Method{}
	}
	res.ResourceMethods[httpMethod] = m
	return true
}

func (s *store) getMethod(apiID, resourceID, httpMethod string) (*Method, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return nil, false
	}
	res, ok := rec.resources[resourceID]
	if !ok {
		return nil, false
	}
	if res.ResourceMethods == nil {
		return nil, false
	}
	m, ok := res.ResourceMethods[httpMethod]
	return m, ok
}

func (s *store) deleteMethod(apiID, resourceID, httpMethod string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return false
	}
	res, ok := rec.resources[resourceID]
	if !ok {
		return false
	}
	if res.ResourceMethods == nil {
		return false
	}
	_, ok = res.ResourceMethods[httpMethod]
	delete(res.ResourceMethods, httpMethod)
	return ok
}

func (s *store) putIntegration(apiID, resourceID, httpMethod string, integ *Integration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return false
	}
	res, ok := rec.resources[resourceID]
	if !ok {
		return false
	}
	if res.ResourceMethods == nil {
		return false
	}
	m, ok := res.ResourceMethods[httpMethod]
	if !ok {
		return false
	}
	m.MethodIntegration = integ
	return true
}

func (s *store) getIntegration(apiID, resourceID, httpMethod string) (*Integration, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return nil, false
	}
	res, ok := rec.resources[resourceID]
	if !ok {
		return nil, false
	}
	if res.ResourceMethods == nil {
		return nil, false
	}
	m, ok := res.ResourceMethods[httpMethod]
	if !ok || m.MethodIntegration == nil {
		return nil, false
	}
	return m.MethodIntegration, true
}

func (s *store) deleteIntegration(apiID, resourceID, httpMethod string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return false
	}
	res, ok := rec.resources[resourceID]
	if !ok {
		return false
	}
	if res.ResourceMethods == nil {
		return false
	}
	m, ok := res.ResourceMethods[httpMethod]
	if !ok || m.MethodIntegration == nil {
		return false
	}
	m.MethodIntegration = nil
	return true
}

func (s *store) putMethodResponse(apiID, resourceID, httpMethod, statusCode string, mr *MethodResponse) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return false
	}
	res, ok := rec.resources[resourceID]
	if !ok {
		return false
	}
	if res.ResourceMethods == nil {
		return false
	}
	m, ok := res.ResourceMethods[httpMethod]
	if !ok {
		return false
	}
	if m.MethodResponses == nil {
		m.MethodResponses = map[string]*MethodResponse{}
	}
	m.MethodResponses[statusCode] = mr
	return true
}

func (s *store) getMethodResponse(apiID, resourceID, httpMethod, statusCode string) (*MethodResponse, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return nil, false
	}
	res, ok := rec.resources[resourceID]
	if !ok {
		return nil, false
	}
	if res.ResourceMethods == nil {
		return nil, false
	}
	m, ok := res.ResourceMethods[httpMethod]
	if !ok || m.MethodResponses == nil {
		return nil, false
	}
	mr, ok := m.MethodResponses[statusCode]
	return mr, ok
}

func (s *store) deleteMethodResponse(apiID, resourceID, httpMethod, statusCode string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return false
	}
	res, ok := rec.resources[resourceID]
	if !ok {
		return false
	}
	if res.ResourceMethods == nil {
		return false
	}
	m, ok := res.ResourceMethods[httpMethod]
	if !ok || m.MethodResponses == nil {
		return false
	}
	_, ok = m.MethodResponses[statusCode]
	delete(m.MethodResponses, statusCode)
	return ok
}

func (s *store) putIntegrationResponse(apiID, resourceID, httpMethod, statusCode string, ir *IntegrationResponse) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return false
	}
	res, ok := rec.resources[resourceID]
	if !ok {
		return false
	}
	if res.ResourceMethods == nil {
		return false
	}
	m, ok := res.ResourceMethods[httpMethod]
	if !ok || m.MethodIntegration == nil {
		return false
	}
	if m.MethodIntegration.IntegrationResponses == nil {
		m.MethodIntegration.IntegrationResponses = map[string]*IntegrationResponse{}
	}
	m.MethodIntegration.IntegrationResponses[statusCode] = ir
	return true
}

func (s *store) getIntegrationResponse(apiID, resourceID, httpMethod, statusCode string) (*IntegrationResponse, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return nil, false
	}
	res, ok := rec.resources[resourceID]
	if !ok {
		return nil, false
	}
	if res.ResourceMethods == nil {
		return nil, false
	}
	m, ok := res.ResourceMethods[httpMethod]
	if !ok || m.MethodIntegration == nil || m.MethodIntegration.IntegrationResponses == nil {
		return nil, false
	}
	ir, ok := m.MethodIntegration.IntegrationResponses[statusCode]
	return ir, ok
}

func (s *store) deleteIntegrationResponse(apiID, resourceID, httpMethod, statusCode string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return false
	}
	res, ok := rec.resources[resourceID]
	if !ok {
		return false
	}
	if res.ResourceMethods == nil {
		return false
	}
	m, ok := res.ResourceMethods[httpMethod]
	if !ok || m.MethodIntegration == nil || m.MethodIntegration.IntegrationResponses == nil {
		return false
	}
	_, ok = m.MethodIntegration.IntegrationResponses[statusCode]
	delete(m.MethodIntegration.IntegrationResponses, statusCode)
	return ok
}

func (s *store) createDeployment(apiID, description, stageName string) (*Deployment, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return nil, false
	}
	d := &Deployment{
		ID:          uid.New(),
		Description: description,
		CreatedDate: time.Now().Unix(),
	}
	rec.deployments[d.ID] = d
	if stageName != "" {
		now := time.Now().Unix()
		if existing, ok := rec.stages[stageName]; ok {
			existing.DeploymentID = d.ID
			existing.LastUpdatedDate = now
		} else {
			rec.stages[stageName] = &Stage{
				StageName:       stageName,
				DeploymentID:    d.ID,
				Description:     description,
				CreatedDate:     now,
				LastUpdatedDate: now,
			}
		}
	}
	return d, true
}

func (s *store) getDeployment(apiID, deploymentID string) (*Deployment, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return nil, false
	}
	d, ok := rec.deployments[deploymentID]
	return d, ok
}

func (s *store) listDeployments(apiID string) ([]*Deployment, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return nil, false
	}
	out := make([]*Deployment, 0, len(rec.deployments))
	for _, d := range rec.deployments {
		out = append(out, d)
	}
	return out, true
}

func (s *store) deleteDeployment(apiID, deploymentID string) bool {
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

func (s *store) createStage(apiID, stageName, deploymentID, description string, variables map[string]string) (*Stage, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return nil, false
	}
	now := time.Now().Unix()
	stage := &Stage{
		StageName:       stageName,
		DeploymentID:    deploymentID,
		Description:     description,
		CreatedDate:     now,
		LastUpdatedDate: now,
		Variables:       variables,
	}
	rec.stages[stageName] = stage
	return stage, true
}

func (s *store) getStage(apiID, stageName string) (*Stage, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return nil, false
	}
	st, ok := rec.stages[stageName]
	return st, ok
}

func (s *store) listStages(apiID string) ([]*Stage, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return nil, false
	}
	out := make([]*Stage, 0, len(rec.stages))
	for _, st := range rec.stages {
		out = append(out, st)
	}
	return out, true
}

func (s *store) deleteStage(apiID, stageName string) bool {
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

// findResourceForPath finds the resource whose Path pattern matches path.
// Returns the matched resource and any captured path parameters.
func (s *store) findResourceForPath(apiID, path string) (*Resource, map[string]string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.apis[apiID]
	if !ok {
		return nil, nil, false
	}
	// Prefer exact matches, then pattern matches.
	for _, res := range rec.resources {
		if res.Path == path {
			return res, map[string]string{}, true
		}
	}
	for _, res := range rec.resources {
		if params, matched := matchPath(res.Path, path); matched {
			return res, params, true
		}
	}
	return nil, nil, false
}

// matchPath matches a resource path pattern against a concrete path.
// Supports {param} and {param+} (greedy) segments.
func matchPath(pattern, path string) (map[string]string, bool) {
	patParts := splitPath(pattern)
	pathParts := splitPath(path)

	if len(patParts) == 0 && len(pathParts) == 0 {
		return map[string]string{}, true
	}

	params := map[string]string{}

	// Greedy last segment: {proxy+}
	if len(patParts) > 0 && strings.HasSuffix(patParts[len(patParts)-1], "+}") {
		if len(pathParts) < len(patParts) {
			return nil, false
		}
		for i, p := range patParts[:len(patParts)-1] {
			if strings.HasPrefix(p, "{") {
				params[strings.Trim(p, "{}")] = pathParts[i]
			} else if p != pathParts[i] {
				return nil, false
			}
		}
		name := strings.TrimSuffix(strings.TrimPrefix(patParts[len(patParts)-1], "{"), "+}")
		params[name] = strings.Join(pathParts[len(patParts)-1:], "/")
		return params, true
	}

	if len(patParts) != len(pathParts) {
		return nil, false
	}
	for i, p := range patParts {
		if strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}") {
			params[strings.Trim(p, "{}")] = pathParts[i]
		} else if p != pathParts[i] {
			return nil, false
		}
	}
	return params, true
}

func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}
