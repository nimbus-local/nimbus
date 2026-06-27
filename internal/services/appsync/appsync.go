// Package appsync emulates the AWS AppSync GraphQL API management plane.
// All state is held in memory — no GraphQL queries are actually executed.
package appsync

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/nimbus-local/nimbus/internal/uid"
)

const accountID = "000000000000"

// Service implements the AppSync emulator.
type Service struct {
	mu        sync.RWMutex
	region    string
	apis      map[string]*graphqlAPI       // apiId -> api
	sources   map[string]*dataSource       // apiId+"/"+name -> dataSource
	resolvers map[string]*resolver         // apiId+"/"+typeName+"/"+fieldName -> resolver
	apiKeys   map[string]*apiKey           // apiId+"/"+keyId -> apiKey
	tags      map[string]map[string]string // resourceArn -> tags
}

type graphqlAPI struct {
	ID                 string            `json:"apiId"`
	Name               string            `json:"name"`
	AuthenticationType string            `json:"authenticationType"`
	Tags               map[string]string `json:"tags,omitempty"`
	ARN                string            `json:"arn"`
	schemaStatus       string
	schemaDefinition   string
}

type dataSource struct {
	APIId          string                  `json:"apiId"`
	Name           string                  `json:"name"`
	Type           string                  `json:"type"`
	Description    string                  `json:"description,omitempty"`
	ServiceRoleArn string                  `json:"serviceRoleArn,omitempty"`
	LambdaConfig   *lambdaDataSourceConfig `json:"lambdaConfig,omitempty"`
	DataSourceArn  string                  `json:"dataSourceArn"`
}

type lambdaDataSourceConfig struct {
	LambdaFunctionArn string `json:"lambdaFunctionArn"`
}

type resolver struct {
	APIId                   string `json:"apiId"`
	TypeName                string `json:"typeName"`
	FieldName               string `json:"fieldName"`
	DataSourceName          string `json:"dataSourceName,omitempty"`
	Kind                    string `json:"kind"`
	RequestMappingTemplate  string `json:"requestMappingTemplate,omitempty"`
	ResponseMappingTemplate string `json:"responseMappingTemplate,omitempty"`
	ResolverArn             string `json:"resolverArn"`
}

type apiKey struct {
	APIId       string `json:"apiId"`
	ID          string `json:"id"`
	Description string `json:"description,omitempty"`
	Expires     int64  `json:"expires"`
	Deletes     int64  `json:"deletes"`
}

// New creates a new AppSync service instance.
func New(region string) *Service {
	if region == "" {
		region = "us-east-1"
	}
	return &Service{
		region:    region,
		apis:      map[string]*graphqlAPI{},
		sources:   map[string]*dataSource{},
		resolvers: map[string]*resolver{},
		apiKeys:   map[string]*apiKey{},
		tags:      map[string]map[string]string{},
	}
}

func (s *Service) Name() string { return "appsync" }

// Reset clears all in-memory state.
func (s *Service) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.apis = map[string]*graphqlAPI{}
	s.sources = map[string]*dataSource{}
	s.resolvers = map[string]*resolver{}
	s.apiKeys = map[string]*apiKey{}
	s.tags = map[string]map[string]string{}
}

// Detect claims requests whose path starts with /v1/apis or /v1/tags (AppSync REST paths).
func (s *Service) Detect(r *http.Request) bool {
	p := r.URL.Path
	return p == "/v1/apis" || strings.HasPrefix(p, "/v1/apis/") ||
		strings.HasPrefix(p, "/v1/tags/")
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path

	// Tags: POST/GET /v1/tags/{resourceArn}
	if strings.HasPrefix(p, "/v1/tags/") {
		rawARN := strings.TrimPrefix(p, "/v1/tags/")
		resourceARN, _ := url.PathUnescape(rawARN)
		switch r.Method {
		case http.MethodGet:
			s.listTagsForResource(w, resourceARN)
		case http.MethodPost:
			s.tagResource(w, r, resourceARN)
		case http.MethodDelete:
			s.untagResource(w, r, resourceARN)
		default:
			jsonError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		}
		return
	}

	// /v1/apis
	rest := strings.TrimPrefix(p, "/v1/apis")
	if rest == "" || rest == "/" {
		switch r.Method {
		case http.MethodPost:
			s.createGraphqlAPI(w, r)
		case http.MethodGet:
			s.listGraphqlAPIs(w)
		default:
			jsonError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		}
		return
	}

	// /v1/apis/{apiId}/...
	rest = strings.TrimPrefix(rest, "/")
	apiID, tail, _ := strings.Cut(rest, "/")
	if apiID == "" {
		jsonError(w, http.StatusBadRequest, "BadRequestException", "missing apiId")
		return
	}

	if tail == "" {
		// /v1/apis/{apiId}
		switch r.Method {
		case http.MethodGet:
			s.getGraphqlAPI(w, apiID)
		case http.MethodPut:
			s.updateGraphqlAPI(w, r, apiID)
		case http.MethodDelete:
			s.deleteGraphqlAPI(w, apiID)
		default:
			jsonError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		}
		return
	}

	switch {
	case tail == "schemacreation":
		switch r.Method {
		case http.MethodPost:
			s.startSchemaCreation(w, r, apiID)
		case http.MethodGet:
			s.getSchemaCreationStatus(w, apiID)
		default:
			jsonError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		}

	case tail == "datasources":
		switch r.Method {
		case http.MethodPost:
			s.createDataSource(w, r, apiID)
		case http.MethodGet:
			s.listDataSources(w, apiID)
		default:
			jsonError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		}

	case strings.HasPrefix(tail, "datasources/"):
		dsName, _ := url.PathUnescape(strings.TrimPrefix(tail, "datasources/"))
		switch r.Method {
		case http.MethodGet:
			s.getDataSource(w, apiID, dsName)
		case http.MethodPut:
			s.updateDataSource(w, r, apiID, dsName)
		case http.MethodDelete:
			s.deleteDataSource(w, apiID, dsName)
		default:
			jsonError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		}

	case strings.EqualFold(tail, "apikeys"):
		switch r.Method {
		case http.MethodPost:
			s.createApiKey(w, r, apiID)
		case http.MethodGet:
			s.listApiKeys(w, apiID)
		default:
			jsonError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		}

	case strings.HasPrefix(strings.ToLower(tail), "apikeys/"):
		keyID := tail[len("apikeys/"):]
		switch r.Method {
		case http.MethodPut:
			s.updateApiKey(w, r, apiID, keyID)
		case http.MethodDelete:
			s.deleteApiKey(w, apiID, keyID)
		default:
			jsonError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		}

	case strings.HasPrefix(tail, "types/"):
		s.routeResolver(w, r, apiID, tail)

	default:
		jsonError(w, http.StatusBadRequest, "BadRequestException", fmt.Sprintf("unknown path: /v1/apis/%s/%s", apiID, tail))
	}
}

// routeResolver handles /v1/apis/{apiId}/types/{typeName}/resolvers[/{fieldName}]
func (s *Service) routeResolver(w http.ResponseWriter, r *http.Request, apiID, tail string) {
	// tail = "types/{typeName}/resolvers[/{fieldName}]"
	tail = strings.TrimPrefix(tail, "types/")
	typeName, rest, _ := strings.Cut(tail, "/")
	if typeName == "" {
		jsonError(w, http.StatusBadRequest, "BadRequestException", "missing typeName")
		return
	}
	typeName, _ = url.PathUnescape(typeName)

	if rest == "resolvers" || rest == "resolvers/" {
		switch r.Method {
		case http.MethodPost:
			s.createResolver(w, r, apiID, typeName)
		case http.MethodGet:
			s.listResolvers(w, apiID, typeName)
		default:
			jsonError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		}
		return
	}

	if strings.HasPrefix(rest, "resolvers/") {
		fieldName, _ := url.PathUnescape(strings.TrimPrefix(rest, "resolvers/"))
		switch r.Method {
		case http.MethodGet:
			s.getResolver(w, apiID, typeName, fieldName)
		case http.MethodPut:
			s.updateResolver(w, r, apiID, typeName, fieldName)
		case http.MethodDelete:
			s.deleteResolver(w, apiID, typeName, fieldName)
		default:
			jsonError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		}
		return
	}

	jsonError(w, http.StatusBadRequest, "BadRequestException", "invalid resolver path")
}

// --- GraphQL API operations ---

func (s *Service) createGraphqlAPI(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name               string            `json:"name"`
		AuthenticationType string            `json:"authenticationType"`
		Tags               map[string]string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		jsonError(w, http.StatusBadRequest, "BadRequestException", "name is required")
		return
	}
	if req.AuthenticationType == "" {
		req.AuthenticationType = "API_KEY"
	}

	// Real AppSync API IDs are dash-free; the TF provider splits composite IDs
	// on the first '-', so UUIDs would cause the datasource/resolver reads to
	// use a truncated apiId and fail with "not found".
	apiID := strings.ReplaceAll(uid.New(), "-", "")
	arn := fmt.Sprintf("arn:aws:appsync:%s:%s:apis/%s", s.region, accountID, apiID)

	api := &graphqlAPI{
		ID:                 apiID,
		Name:               req.Name,
		AuthenticationType: req.AuthenticationType,
		Tags:               req.Tags,
		ARN:                arn,
		schemaStatus:       "ACTIVE",
	}

	s.mu.Lock()
	s.apis[apiID] = api
	if req.Tags != nil {
		s.tags[arn] = req.Tags
	}
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{"graphqlApi": s.apiResponse(api)})
}

func (s *Service) getGraphqlAPI(w http.ResponseWriter, apiID string) {
	s.mu.RLock()
	api := s.apis[apiID]
	s.mu.RUnlock()
	if api == nil {
		jsonError(w, http.StatusNotFound, "NotFoundException", fmt.Sprintf("GraphQL API %s not found", apiID))
		return
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{"graphqlApi": s.apiResponse(api)})
}

func (s *Service) updateGraphqlAPI(w http.ResponseWriter, r *http.Request, apiID string) {
	s.mu.Lock()
	api := s.apis[apiID]
	s.mu.Unlock()
	if api == nil {
		jsonError(w, http.StatusNotFound, "NotFoundException", fmt.Sprintf("GraphQL API %s not found", apiID))
		return
	}

	var req struct {
		Name               string `json:"name"`
		AuthenticationType string `json:"authenticationType"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.Lock()
	if req.Name != "" {
		api.Name = req.Name
	}
	if req.AuthenticationType != "" {
		api.AuthenticationType = req.AuthenticationType
	}
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{"graphqlApi": s.apiResponse(api)})
}

func (s *Service) deleteGraphqlAPI(w http.ResponseWriter, apiID string) {
	s.mu.Lock()
	api := s.apis[apiID]
	if api == nil {
		s.mu.Unlock()
		jsonError(w, http.StatusNotFound, "NotFoundException", fmt.Sprintf("GraphQL API %s not found", apiID))
		return
	}
	delete(s.apis, apiID)
	// cascade-delete associated resources
	prefix := apiID + "/"
	for k := range s.sources {
		if strings.HasPrefix(k, prefix) {
			delete(s.sources, k)
		}
	}
	for k := range s.resolvers {
		if strings.HasPrefix(k, prefix) {
			delete(s.resolvers, k)
		}
	}
	for k := range s.apiKeys {
		if strings.HasPrefix(k, prefix) {
			delete(s.apiKeys, k)
		}
	}
	delete(s.tags, api.ARN)
	s.mu.Unlock()

	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) listGraphqlAPIs(w http.ResponseWriter) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := make([]interface{}, 0, len(s.apis))
	for _, api := range s.apis {
		list = append(list, s.apiResponse(api))
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{"graphqlApis": list})
}

func (s *Service) apiResponse(api *graphqlAPI) map[string]interface{} {
	return map[string]interface{}{
		"apiId":              api.ID,
		"name":               api.Name,
		"authenticationType": api.AuthenticationType,
		"arn":                api.ARN,
		"tags":               api.Tags,
		"uris": map[string]string{
			"GRAPHQL": fmt.Sprintf("http://%s.appsync-api.%s.nimbus.local/graphql", api.ID, s.region),
		},
	}
}

// --- Schema operations ---

func (s *Service) startSchemaCreation(w http.ResponseWriter, r *http.Request, apiID string) {
	s.mu.Lock()
	api := s.apis[apiID]
	if api == nil {
		s.mu.Unlock()
		jsonError(w, http.StatusNotFound, "NotFoundException", fmt.Sprintf("GraphQL API %s not found", apiID))
		return
	}
	var req struct {
		Definition string `json:"definition"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	api.schemaDefinition = req.Definition
	api.schemaStatus = "SUCCESS"
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]string{"status": "SUCCESS"})
}

func (s *Service) getSchemaCreationStatus(w http.ResponseWriter, apiID string) {
	s.mu.RLock()
	api := s.apis[apiID]
	s.mu.RUnlock()
	if api == nil {
		jsonError(w, http.StatusNotFound, "NotFoundException", fmt.Sprintf("GraphQL API %s not found", apiID))
		return
	}
	status := api.schemaStatus
	if status == "" {
		status = "SUCCESS"
	}
	jsonWrite(w, http.StatusOK, map[string]string{"status": status})
}

// --- Data source operations ---

func (s *Service) createDataSource(w http.ResponseWriter, r *http.Request, apiID string) {
	s.mu.RLock()
	api := s.apis[apiID]
	s.mu.RUnlock()
	if api == nil {
		jsonError(w, http.StatusNotFound, "NotFoundException", fmt.Sprintf("GraphQL API %s not found", apiID))
		return
	}

	var req struct {
		Name           string                  `json:"name"`
		Type           string                  `json:"type"`
		Description    string                  `json:"description"`
		ServiceRoleArn string                  `json:"serviceRoleArn"`
		LambdaConfig   *lambdaDataSourceConfig `json:"lambdaConfig"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		jsonError(w, http.StatusBadRequest, "BadRequestException", "name is required")
		return
	}

	arn := fmt.Sprintf("arn:aws:appsync:%s:%s:apis/%s/datasources/%s", s.region, accountID, apiID, req.Name)
	ds := &dataSource{
		APIId:          apiID,
		Name:           req.Name,
		Type:           req.Type,
		Description:    req.Description,
		ServiceRoleArn: req.ServiceRoleArn,
		LambdaConfig:   req.LambdaConfig,
		DataSourceArn:  arn,
	}

	s.mu.Lock()
	s.sources[apiID+"/"+req.Name] = ds
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{"dataSource": ds})
}

func (s *Service) getDataSource(w http.ResponseWriter, apiID, name string) {
	s.mu.RLock()
	ds := s.sources[apiID+"/"+name]
	s.mu.RUnlock()
	if ds == nil {
		jsonError(w, http.StatusNotFound, "NotFoundException", fmt.Sprintf("data source %s not found", name))
		return
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{"dataSource": ds})
}

func (s *Service) updateDataSource(w http.ResponseWriter, r *http.Request, apiID, name string) {
	s.mu.Lock()
	ds := s.sources[apiID+"/"+name]
	if ds == nil {
		s.mu.Unlock()
		jsonError(w, http.StatusNotFound, "NotFoundException", fmt.Sprintf("data source %s not found", name))
		return
	}
	var req struct {
		Type           string                  `json:"type"`
		Description    string                  `json:"description"`
		ServiceRoleArn string                  `json:"serviceRoleArn"`
		LambdaConfig   *lambdaDataSourceConfig `json:"lambdaConfig"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.Type != "" {
		ds.Type = req.Type
	}
	if req.Description != "" {
		ds.Description = req.Description
	}
	if req.ServiceRoleArn != "" {
		ds.ServiceRoleArn = req.ServiceRoleArn
	}
	if req.LambdaConfig != nil {
		ds.LambdaConfig = req.LambdaConfig
	}
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{"dataSource": ds})
}

func (s *Service) deleteDataSource(w http.ResponseWriter, apiID, name string) {
	s.mu.Lock()
	key := apiID + "/" + name
	if s.sources[key] == nil {
		s.mu.Unlock()
		jsonError(w, http.StatusNotFound, "NotFoundException", fmt.Sprintf("data source %s not found", name))
		return
	}
	delete(s.sources, key)
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) listDataSources(w http.ResponseWriter, apiID string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	prefix := apiID + "/"
	var list []interface{}
	for k, ds := range s.sources {
		if strings.HasPrefix(k, prefix) {
			list = append(list, ds)
		}
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{"dataSources": list})
}

// --- Resolver operations ---

func resolverKey(apiID, typeName, fieldName string) string {
	return apiID + "/" + typeName + "/" + fieldName
}

func (s *Service) createResolver(w http.ResponseWriter, r *http.Request, apiID, typeName string) {
	var req struct {
		FieldName               string `json:"fieldName"`
		DataSourceName          string `json:"dataSourceName"`
		Kind                    string `json:"kind"`
		RequestMappingTemplate  string `json:"requestMappingTemplate"`
		ResponseMappingTemplate string `json:"responseMappingTemplate"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.FieldName == "" {
		jsonError(w, http.StatusBadRequest, "BadRequestException", "fieldName is required")
		return
	}
	if req.Kind == "" {
		req.Kind = "UNIT"
	}

	arn := fmt.Sprintf("arn:aws:appsync:%s:%s:apis/%s/types/%s/resolvers/%s",
		s.region, accountID, apiID, typeName, req.FieldName)
	res := &resolver{
		APIId:                   apiID,
		TypeName:                typeName,
		FieldName:               req.FieldName,
		DataSourceName:          req.DataSourceName,
		Kind:                    req.Kind,
		RequestMappingTemplate:  req.RequestMappingTemplate,
		ResponseMappingTemplate: req.ResponseMappingTemplate,
		ResolverArn:             arn,
	}

	s.mu.Lock()
	s.resolvers[resolverKey(apiID, typeName, req.FieldName)] = res
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{"resolver": res})
}

func (s *Service) getResolver(w http.ResponseWriter, apiID, typeName, fieldName string) {
	s.mu.RLock()
	res := s.resolvers[resolverKey(apiID, typeName, fieldName)]
	s.mu.RUnlock()
	if res == nil {
		jsonError(w, http.StatusNotFound, "NotFoundException",
			fmt.Sprintf("resolver %s/%s not found", typeName, fieldName))
		return
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{"resolver": res})
}

func (s *Service) updateResolver(w http.ResponseWriter, r *http.Request, apiID, typeName, fieldName string) {
	s.mu.Lock()
	res := s.resolvers[resolverKey(apiID, typeName, fieldName)]
	if res == nil {
		s.mu.Unlock()
		jsonError(w, http.StatusNotFound, "NotFoundException",
			fmt.Sprintf("resolver %s/%s not found", typeName, fieldName))
		return
	}
	var req struct {
		DataSourceName          string `json:"dataSourceName"`
		Kind                    string `json:"kind"`
		RequestMappingTemplate  string `json:"requestMappingTemplate"`
		ResponseMappingTemplate string `json:"responseMappingTemplate"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.DataSourceName != "" {
		res.DataSourceName = req.DataSourceName
	}
	if req.Kind != "" {
		res.Kind = req.Kind
	}
	if req.RequestMappingTemplate != "" {
		res.RequestMappingTemplate = req.RequestMappingTemplate
	}
	if req.ResponseMappingTemplate != "" {
		res.ResponseMappingTemplate = req.ResponseMappingTemplate
	}
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{"resolver": res})
}

func (s *Service) deleteResolver(w http.ResponseWriter, apiID, typeName, fieldName string) {
	s.mu.Lock()
	key := resolverKey(apiID, typeName, fieldName)
	if s.resolvers[key] == nil {
		s.mu.Unlock()
		jsonError(w, http.StatusNotFound, "NotFoundException",
			fmt.Sprintf("resolver %s/%s not found", typeName, fieldName))
		return
	}
	delete(s.resolvers, key)
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) listResolvers(w http.ResponseWriter, apiID, typeName string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	prefix := apiID + "/" + typeName + "/"
	var list []interface{}
	for k, res := range s.resolvers {
		if strings.HasPrefix(k, prefix) {
			list = append(list, res)
		}
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{"resolvers": list})
}

// --- API key operations ---

func (s *Service) createApiKey(w http.ResponseWriter, r *http.Request, apiID string) {
	s.mu.RLock()
	api := s.apis[apiID]
	s.mu.RUnlock()
	if api == nil {
		jsonError(w, http.StatusNotFound, "NotFoundException", fmt.Sprintf("GraphQL API %s not found", apiID))
		return
	}

	var req struct {
		Description string `json:"description"`
		Expires     int64  `json:"expires"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	keyID := "da2-" + uid.New()[:8]
	expires := req.Expires
	if expires == 0 {
		expires = time.Now().Add(365 * 24 * time.Hour).Unix()
	}
	k := &apiKey{
		APIId:       apiID,
		ID:          keyID,
		Description: req.Description,
		Expires:     expires,
		Deletes:     expires + 7*24*int64(time.Hour/time.Second),
	}

	s.mu.Lock()
	s.apiKeys[apiID+"/"+keyID] = k
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{"apiKey": k})
}

func (s *Service) listApiKeys(w http.ResponseWriter, apiID string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	prefix := apiID + "/"
	var list []interface{}
	for k, key := range s.apiKeys {
		if strings.HasPrefix(k, prefix) {
			list = append(list, key)
		}
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{"apiKeys": list})
}

func (s *Service) updateApiKey(w http.ResponseWriter, r *http.Request, apiID, keyID string) {
	s.mu.Lock()
	k := s.apiKeys[apiID+"/"+keyID]
	if k == nil {
		s.mu.Unlock()
		jsonError(w, http.StatusNotFound, "NotFoundException", fmt.Sprintf("API key %s not found", keyID))
		return
	}
	var req struct {
		Description string `json:"description"`
		Expires     int64  `json:"expires"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.Description != "" {
		k.Description = req.Description
	}
	if req.Expires != 0 {
		k.Expires = req.Expires
	}
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{"apiKey": k})
}

func (s *Service) deleteApiKey(w http.ResponseWriter, apiID, keyID string) {
	s.mu.Lock()
	key := apiID + "/" + keyID
	if s.apiKeys[key] == nil {
		s.mu.Unlock()
		jsonError(w, http.StatusNotFound, "NotFoundException", fmt.Sprintf("API key %s not found", keyID))
		return
	}
	delete(s.apiKeys, key)
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// --- Tag operations ---

func (s *Service) listTagsForResource(w http.ResponseWriter, resourceARN string) {
	s.mu.RLock()
	tags := s.tags[resourceARN]
	s.mu.RUnlock()
	if tags == nil {
		tags = map[string]string{}
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{"tags": tags})
}

func (s *Service) tagResource(w http.ResponseWriter, r *http.Request, resourceARN string) {
	var req struct {
		Tags map[string]string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "BadRequestException", "invalid request body")
		return
	}
	s.mu.Lock()
	if s.tags[resourceARN] == nil {
		s.tags[resourceARN] = map[string]string{}
	}
	for k, v := range req.Tags {
		s.tags[resourceARN][k] = v
	}
	s.mu.Unlock()
	jsonWrite(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) untagResource(w http.ResponseWriter, r *http.Request, resourceARN string) {
	var tagKeys []string
	if q := r.URL.Query().Get("tagKeys"); q != "" {
		tagKeys = strings.Split(q, ",")
	}
	s.mu.Lock()
	if s.tags[resourceARN] != nil {
		for _, k := range tagKeys {
			delete(s.tags[resourceARN], k)
		}
	}
	s.mu.Unlock()
	jsonWrite(w, http.StatusOK, map[string]interface{}{})
}

// --- Nimbus inspection ---

// APIsHandler serves GET /_nimbus/appsync/apis
func (s *Service) APIsHandler(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := make([]interface{}, 0, len(s.apis))
	for _, api := range s.apis {
		list = append(list, s.apiResponse(api))
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

// APICount returns the number of GraphQL APIs.
func (s *Service) APICount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.apis)
}

// --- Helpers ---

func jsonWrite(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{
		"message": message,
		"code":    code,
	})
}
