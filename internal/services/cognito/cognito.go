package cognito

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nimbus-local/nimbus/internal/uid"
)

const accountID = "000000000000"

// Service implements the AWS Cognito User Pools emulator.
// All state is in-memory.
type Service struct {
	mu      sync.RWMutex
	pools   map[string]*userPool   // poolID -> pool
	clients map[string]*poolClient // clientID -> client
	region  string
}

type userPool struct {
	id               string
	arn              string
	name             string
	status           string
	creationDate     time.Time
	lastModifiedDate time.Time
	tags             map[string]string
	// schema / policies stored as raw JSON for pass-through
	schema                   json.RawMessage
	policies                 json.RawMessage
	autoVerifiedAttributes   []string
	usernameAttributes       []string
	mfaConfiguration         string
	emailVerificationMessage string
	emailVerificationSubject string
}

type poolClient struct {
	clientID     string
	clientName   string
	userPoolID   string
	creationDate time.Time
	// Explicit flows requested by caller — stored for DescribeUserPoolClient.
	explicitAuthFlows  []string
	callbackURLs       []string
	logoutURLs         []string
	allowedOAuthFlows  []string
	allowedOAuthScopes []string
	supportedIDPs      []string
	generateSecret     bool
	clientSecret       string
}

func New(region string) *Service {
	if region == "" {
		region = "us-east-1"
	}
	return &Service{
		region:  region,
		pools:   map[string]*userPool{},
		clients: map[string]*poolClient{},
	}
}

func (s *Service) Name() string { return "cognito" }

// Detect identifies Cognito User Pool requests by X-Amz-Target prefix.
func (s *Service) Detect(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), "AWSCognitoIdentityProviderService.")
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	target := r.Header.Get("X-Amz-Target")
	action := strings.TrimPrefix(target, "AWSCognitoIdentityProviderService.")

	switch action {
	// User pool
	case "CreateUserPool":
		s.createUserPool(w, r)
	case "DescribeUserPool":
		s.describeUserPool(w, r)
	case "UpdateUserPool":
		s.updateUserPool(w, r)
	case "DeleteUserPool":
		s.deleteUserPool(w, r)
	case "ListUserPools":
		s.listUserPools(w, r)

	// User pool client
	case "CreateUserPoolClient":
		s.createUserPoolClient(w, r)
	case "DescribeUserPoolClient":
		s.describeUserPoolClient(w, r)
	case "UpdateUserPoolClient":
		s.updateUserPoolClient(w, r)
	case "DeleteUserPoolClient":
		s.deleteUserPoolClient(w, r)
	case "ListUserPoolClients":
		s.listUserPoolClients(w, r)

	// Tags
	case "ListTagsForResource":
		s.listTagsForResource(w, r)
	case "TagResource":
		s.tagResource(w, r)
	case "UntagResource":
		s.untagResource(w, r)

	default:
		jsonError(w, http.StatusBadRequest, "NotImplementedException",
			fmt.Sprintf("Action not implemented: %s", action))
	}
}

// --- User pool ---

func (s *Service) createUserPool(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PoolName                 string            `json:"PoolName"`
		AutoVerifiedAttributes   []string          `json:"AutoVerifiedAttributes"`
		UsernameAttributes       []string          `json:"UsernameAttributes"`
		MfaConfiguration         string            `json:"MfaConfiguration"`
		Schema                   json.RawMessage   `json:"Schema"`
		Policies                 json.RawMessage   `json:"Policies"`
		UserPoolTags             map[string]string `json:"UserPoolTags"`
		EmailVerificationMessage string            `json:"EmailVerificationMessage"`
		EmailVerificationSubject string            `json:"EmailVerificationSubject"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "InvalidParameterException", "could not parse request")
		return
	}
	if req.PoolName == "" {
		jsonError(w, http.StatusBadRequest, "InvalidParameterException", "PoolName is required")
		return
	}

	id := "us-east-1_" + uid.New()[:8]
	now := time.Now().UTC()
	tags := req.UserPoolTags
	if tags == nil {
		tags = map[string]string{}
	}
	pool := &userPool{
		id:                       id,
		arn:                      s.poolARN(id),
		name:                     req.PoolName,
		status:                   "Active",
		creationDate:             now,
		lastModifiedDate:         now,
		tags:                     tags,
		schema:                   req.Schema,
		policies:                 req.Policies,
		autoVerifiedAttributes:   req.AutoVerifiedAttributes,
		usernameAttributes:       req.UsernameAttributes,
		mfaConfiguration:         req.MfaConfiguration,
		emailVerificationMessage: req.EmailVerificationMessage,
		emailVerificationSubject: req.EmailVerificationSubject,
	}
	if pool.mfaConfiguration == "" {
		pool.mfaConfiguration = "OFF"
	}

	s.mu.Lock()
	s.pools[id] = pool
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{
		"UserPool": s.poolDetail(pool),
	})
}

func (s *Service) describeUserPool(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserPoolID string `json:"UserPoolId"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.RLock()
	pool, ok := s.pools[req.UserPoolID]
	s.mu.RUnlock()
	if !ok {
		jsonError(w, http.StatusBadRequest, "ResourceNotFoundException",
			fmt.Sprintf("User pool %s does not exist.", req.UserPoolID))
		return
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{
		"UserPool": s.poolDetail(pool),
	})
}

func (s *Service) updateUserPool(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserPoolID               string            `json:"UserPoolId"`
		UserPoolTags             map[string]string `json:"UserPoolTags"`
		MfaConfiguration         string            `json:"MfaConfiguration"`
		AutoVerifiedAttributes   []string          `json:"AutoVerifiedAttributes"`
		EmailVerificationMessage string            `json:"EmailVerificationMessage"`
		EmailVerificationSubject string            `json:"EmailVerificationSubject"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.Lock()
	pool, ok := s.pools[req.UserPoolID]
	if !ok {
		s.mu.Unlock()
		jsonError(w, http.StatusBadRequest, "ResourceNotFoundException",
			fmt.Sprintf("User pool %s does not exist.", req.UserPoolID))
		return
	}
	if req.UserPoolTags != nil {
		pool.tags = req.UserPoolTags
	}
	if req.MfaConfiguration != "" {
		pool.mfaConfiguration = req.MfaConfiguration
	}
	if req.AutoVerifiedAttributes != nil {
		pool.autoVerifiedAttributes = req.AutoVerifiedAttributes
	}
	if req.EmailVerificationMessage != "" {
		pool.emailVerificationMessage = req.EmailVerificationMessage
	}
	if req.EmailVerificationSubject != "" {
		pool.emailVerificationSubject = req.EmailVerificationSubject
	}
	pool.lastModifiedDate = time.Now().UTC()
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) deleteUserPool(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserPoolID string `json:"UserPoolId"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.Lock()
	_, ok := s.pools[req.UserPoolID]
	if !ok {
		s.mu.Unlock()
		jsonError(w, http.StatusBadRequest, "ResourceNotFoundException",
			fmt.Sprintf("User pool %s does not exist.", req.UserPoolID))
		return
	}
	delete(s.pools, req.UserPoolID)
	// Cascade-delete clients belonging to this pool.
	for id, c := range s.clients {
		if c.userPoolID == req.UserPoolID {
			delete(s.clients, id)
		}
	}
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) listUserPools(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MaxResults int    `json:"MaxResults"`
		NextToken  string `json:"NextToken"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.RLock()
	defer s.mu.RUnlock()

	type summary struct {
		ID               string  `json:"Id"`
		Name             string  `json:"Name"`
		Status           string  `json:"Status"`
		CreationDate     float64 `json:"CreationDate"`
		LastModifiedDate float64 `json:"LastModifiedDate"`
	}
	pools := make([]summary, 0, len(s.pools))
	for _, p := range s.pools {
		pools = append(pools, summary{
			ID:               p.id,
			Name:             p.name,
			Status:           p.status,
			CreationDate:     float64(p.creationDate.Unix()),
			LastModifiedDate: float64(p.lastModifiedDate.Unix()),
		})
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{
		"UserPools": pools,
		"NextToken": "",
	})
}

// --- User pool client ---

func (s *Service) createUserPoolClient(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserPoolID         string   `json:"UserPoolId"`
		ClientName         string   `json:"ClientName"`
		ExplicitAuthFlows  []string `json:"ExplicitAuthFlows"`
		CallbackURLs       []string `json:"CallbackURLs"`
		LogoutURLs         []string `json:"LogoutURLs"`
		AllowedOAuthFlows  []string `json:"AllowedOAuthFlows"`
		AllowedOAuthScopes []string `json:"AllowedOAuthScopes"`
		SupportedIDPs      []string `json:"SupportedIdentityProviders"`
		GenerateSecret     bool     `json:"GenerateSecret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "InvalidParameterException", "could not parse request")
		return
	}

	s.mu.RLock()
	_, poolExists := s.pools[req.UserPoolID]
	s.mu.RUnlock()
	if !poolExists {
		jsonError(w, http.StatusBadRequest, "ResourceNotFoundException",
			fmt.Sprintf("User pool %s does not exist.", req.UserPoolID))
		return
	}

	clientID := uid.New()
	now := time.Now().UTC()
	c := &poolClient{
		clientID:           clientID,
		clientName:         req.ClientName,
		userPoolID:         req.UserPoolID,
		creationDate:       now,
		explicitAuthFlows:  req.ExplicitAuthFlows,
		callbackURLs:       req.CallbackURLs,
		logoutURLs:         req.LogoutURLs,
		allowedOAuthFlows:  req.AllowedOAuthFlows,
		allowedOAuthScopes: req.AllowedOAuthScopes,
		supportedIDPs:      req.SupportedIDPs,
		generateSecret:     req.GenerateSecret,
	}
	if req.GenerateSecret {
		c.clientSecret = uid.New()
	}

	s.mu.Lock()
	s.clients[clientID] = c
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{
		"UserPoolClient": s.clientDetail(c),
	})
}

func (s *Service) describeUserPoolClient(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserPoolID string `json:"UserPoolId"`
		ClientID   string `json:"ClientId"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.RLock()
	c, ok := s.clients[req.ClientID]
	s.mu.RUnlock()
	if !ok || c.userPoolID != req.UserPoolID {
		jsonError(w, http.StatusBadRequest, "ResourceNotFoundException",
			fmt.Sprintf("User pool client %s does not exist.", req.ClientID))
		return
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{
		"UserPoolClient": s.clientDetail(c),
	})
}

func (s *Service) updateUserPoolClient(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserPoolID         string   `json:"UserPoolId"`
		ClientID           string   `json:"ClientId"`
		ClientName         string   `json:"ClientName"`
		ExplicitAuthFlows  []string `json:"ExplicitAuthFlows"`
		CallbackURLs       []string `json:"CallbackURLs"`
		LogoutURLs         []string `json:"LogoutURLs"`
		AllowedOAuthFlows  []string `json:"AllowedOAuthFlows"`
		AllowedOAuthScopes []string `json:"AllowedOAuthScopes"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.Lock()
	c, ok := s.clients[req.ClientID]
	if !ok || c.userPoolID != req.UserPoolID {
		s.mu.Unlock()
		jsonError(w, http.StatusBadRequest, "ResourceNotFoundException",
			fmt.Sprintf("User pool client %s does not exist.", req.ClientID))
		return
	}
	if req.ClientName != "" {
		c.clientName = req.ClientName
	}
	if req.ExplicitAuthFlows != nil {
		c.explicitAuthFlows = req.ExplicitAuthFlows
	}
	if req.CallbackURLs != nil {
		c.callbackURLs = req.CallbackURLs
	}
	if req.LogoutURLs != nil {
		c.logoutURLs = req.LogoutURLs
	}
	if req.AllowedOAuthFlows != nil {
		c.allowedOAuthFlows = req.AllowedOAuthFlows
	}
	if req.AllowedOAuthScopes != nil {
		c.allowedOAuthScopes = req.AllowedOAuthScopes
	}
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{
		"UserPoolClient": s.clientDetail(c),
	})
}

func (s *Service) deleteUserPoolClient(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserPoolID string `json:"UserPoolId"`
		ClientID   string `json:"ClientId"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.Lock()
	c, ok := s.clients[req.ClientID]
	if !ok || c.userPoolID != req.UserPoolID {
		s.mu.Unlock()
		jsonError(w, http.StatusBadRequest, "ResourceNotFoundException",
			fmt.Sprintf("User pool client %s does not exist.", req.ClientID))
		return
	}
	delete(s.clients, req.ClientID)
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) listUserPoolClients(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserPoolID string `json:"UserPoolId"`
		MaxResults int    `json:"MaxResults"`
		NextToken  string `json:"NextToken"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.RLock()
	defer s.mu.RUnlock()

	type summary struct {
		ClientID   string `json:"ClientId"`
		ClientName string `json:"ClientName"`
		UserPoolID string `json:"UserPoolId"`
	}
	clients := make([]summary, 0)
	for _, c := range s.clients {
		if c.userPoolID == req.UserPoolID {
			clients = append(clients, summary{
				ClientID:   c.clientID,
				ClientName: c.clientName,
				UserPoolID: c.userPoolID,
			})
		}
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{
		"UserPoolClients": clients,
		"NextToken":       "",
	})
}

// --- Tags ---

func (s *Service) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceARN string `json:"ResourceArn"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, p := range s.pools {
		if p.arn == req.ResourceARN {
			jsonWrite(w, http.StatusOK, map[string]interface{}{"Tags": p.tags})
			return
		}
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{"Tags": map[string]string{}})
}

func (s *Service) tagResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceARN string            `json:"ResourceArn"`
		Tags        map[string]string `json:"Tags"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.Lock()
	for _, p := range s.pools {
		if p.arn == req.ResourceARN {
			for k, v := range req.Tags {
				p.tags[k] = v
			}
			break
		}
	}
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) untagResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceARN string   `json:"ResourceArn"`
		TagKeys     []string `json:"TagKeys"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.Lock()
	for _, p := range s.pools {
		if p.arn == req.ResourceARN {
			for _, k := range req.TagKeys {
				delete(p.tags, k)
			}
			break
		}
	}
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{})
}

// --- Helpers ---

func (s *Service) poolARN(id string) string {
	return fmt.Sprintf("arn:aws:cognito-idp:%s:%s:userpool/%s", s.region, accountID, id)
}

func (s *Service) poolDetail(p *userPool) map[string]interface{} {
	out := map[string]interface{}{
		"Id":               p.id,
		"Arn":              p.arn,
		"Name":             p.name,
		"Status":           p.status,
		"CreationDate":     float64(p.creationDate.Unix()),
		"LastModifiedDate": float64(p.lastModifiedDate.Unix()),
		"MfaConfiguration": p.mfaConfiguration,
		"UserPoolTags":     p.tags,
	}
	if len(p.autoVerifiedAttributes) > 0 {
		out["AutoVerifiedAttributes"] = p.autoVerifiedAttributes
	}
	if len(p.usernameAttributes) > 0 {
		out["UsernameAttributes"] = p.usernameAttributes
	}
	if p.schema != nil {
		out["SchemaAttributes"] = p.schema
	}
	if p.policies != nil {
		out["Policies"] = p.policies
	}
	return out
}

func (s *Service) clientDetail(c *poolClient) map[string]interface{} {
	out := map[string]interface{}{
		"ClientId":     c.clientID,
		"ClientName":   c.clientName,
		"UserPoolId":   c.userPoolID,
		"CreationDate": float64(c.creationDate.Unix()),
	}
	if len(c.explicitAuthFlows) > 0 {
		out["ExplicitAuthFlows"] = c.explicitAuthFlows
	}
	if len(c.callbackURLs) > 0 {
		out["CallbackURLs"] = c.callbackURLs
	}
	if len(c.logoutURLs) > 0 {
		out["LogoutURLs"] = c.logoutURLs
	}
	if len(c.allowedOAuthFlows) > 0 {
		out["AllowedOAuthFlows"] = c.allowedOAuthFlows
	}
	if len(c.allowedOAuthScopes) > 0 {
		out["AllowedOAuthScopes"] = c.allowedOAuthScopes
	}
	if len(c.supportedIDPs) > 0 {
		out["SupportedIdentityProviders"] = c.supportedIDPs
	}
	if c.generateSecret && c.clientSecret != "" {
		out["ClientSecret"] = c.clientSecret
	}
	return out
}

func jsonWrite(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.Header().Set("x-amzn-ErrorType", code)
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{
		"__type":  code,
		"message": message,
	})
}
