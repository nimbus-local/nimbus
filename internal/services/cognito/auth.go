package cognito

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nimbus-local/nimbus/internal/uid"
)

type user struct {
	sub        string
	username   string
	password   string // plaintext — local dev only
	confirmed  bool
	enabled    bool
	attributes map[string]string // attribute name -> value
	userPoolID string
	createdAt  time.Time
}

type tokenRecord struct {
	jti        string
	username   string
	userPoolID string
	issuedAt   time.Time
	revoked    bool
	isRefresh  bool
}

// GET /{poolId}/.well-known/jwks.json
func (s *Service) jwksHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(jwksResponse(&s.rsaKey.PublicKey))
}

func (s *Service) adminCreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserPoolID        string `json:"UserPoolId"`
		Username          string `json:"Username"`
		TemporaryPassword string `json:"TemporaryPassword"`
		UserAttributes    []struct {
			Name  string `json:"Name"`
			Value string `json:"Value"`
		} `json:"UserAttributes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "InvalidParameterException", "could not parse request")
		return
	}

	s.mu.Lock()
	_, poolExists := s.pools[req.UserPoolID]
	if !poolExists {
		s.mu.Unlock()
		jsonError(w, http.StatusBadRequest, "ResourceNotFoundException",
			fmt.Sprintf("User pool %s does not exist.", req.UserPoolID))
		return
	}
	if s.users[req.UserPoolID] == nil {
		s.users[req.UserPoolID] = map[string]*user{}
	}
	if _, exists := s.users[req.UserPoolID][req.Username]; exists {
		s.mu.Unlock()
		jsonError(w, http.StatusBadRequest, "UsernameExistsException", "User account already exists.")
		return
	}

	attrs := map[string]string{}
	for _, a := range req.UserAttributes {
		attrs[a.Name] = a.Value
	}
	// Treat email-format username as email attribute when not explicitly set.
	if _, ok := attrs["email"]; !ok && strings.Contains(req.Username, "@") {
		attrs["email"] = req.Username
	}

	u := &user{
		sub:        uid.New(),
		username:   req.Username,
		password:   req.TemporaryPassword,
		confirmed:  true, // auto-confirm in local mode
		enabled:    true,
		attributes: attrs,
		userPoolID: req.UserPoolID,
		createdAt:  time.Now().UTC(),
	}
	s.users[req.UserPoolID][req.Username] = u
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{
		"User": s.userDetail(u),
	})
}

func (s *Service) adminSetUserPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserPoolID string `json:"UserPoolId"`
		Username   string `json:"Username"`
		Password   string `json:"Password"`
		Permanent  bool   `json:"Permanent"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.Lock()
	poolUsers, ok := s.users[req.UserPoolID]
	if !ok {
		s.mu.Unlock()
		jsonError(w, http.StatusBadRequest, "UserNotFoundException", "User does not exist.")
		return
	}
	u, ok := poolUsers[req.Username]
	if !ok {
		s.mu.Unlock()
		jsonError(w, http.StatusBadRequest, "UserNotFoundException", "User does not exist.")
		return
	}
	u.password = req.Password
	if req.Permanent {
		u.confirmed = true
	}
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) initiateAuth(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AuthFlow       string            `json:"AuthFlow"`
		AuthParameters map[string]string `json:"AuthParameters"`
		ClientID       string            `json:"ClientId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "InvalidParameterException", "could not parse request")
		return
	}

	switch req.AuthFlow {
	case "USER_PASSWORD_AUTH":
		s.mu.RLock()
		c, ok := s.clients[req.ClientID]
		s.mu.RUnlock()
		if !ok {
			jsonError(w, http.StatusBadRequest, "ResourceNotFoundException", "Client does not exist.")
			return
		}
		s.issueTokens(w, c.userPoolID, req.ClientID, req.AuthParameters)
	default:
		jsonError(w, http.StatusBadRequest, "InvalidParameterException",
			fmt.Sprintf("Unsupported AuthFlow: %s", req.AuthFlow))
	}
}

func (s *Service) adminInitiateAuth(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AuthFlow       string            `json:"AuthFlow"`
		AuthParameters map[string]string `json:"AuthParameters"`
		UserPoolID     string            `json:"UserPoolId"`
		ClientID       string            `json:"ClientId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "InvalidParameterException", "could not parse request")
		return
	}

	switch req.AuthFlow {
	case "USER_PASSWORD_AUTH", "ADMIN_USER_PASSWORD_AUTH", "ADMIN_NO_SRP_AUTH":
		s.issueTokens(w, req.UserPoolID, req.ClientID, req.AuthParameters)
	default:
		jsonError(w, http.StatusBadRequest, "InvalidParameterException",
			fmt.Sprintf("Unsupported AuthFlow: %s", req.AuthFlow))
	}
}

func (s *Service) issueTokens(w http.ResponseWriter, poolID, clientID string, params map[string]string) {
	username := params["USERNAME"]
	password := params["PASSWORD"]

	s.mu.RLock()
	var u *user
	if poolUsers := s.users[poolID]; poolUsers != nil {
		u = poolUsers[username]
	}
	s.mu.RUnlock()

	if u == nil || !u.enabled {
		jsonError(w, http.StatusBadRequest, "UserNotFoundException", "User does not exist.")
		return
	}
	if u.password != password {
		jsonError(w, http.StatusBadRequest, "NotAuthorizedException", "Incorrect username or password.")
		return
	}
	if !u.confirmed {
		jsonError(w, http.StatusBadRequest, "UserNotConfirmedException", "User is not confirmed.")
		return
	}

	now := time.Now()
	exp := now.Add(time.Hour)
	iss := fmt.Sprintf("https://cognito-idp.%s.amazonaws.com/%s", s.region, poolID)
	jti := uid.New()
	originJTI := uid.New()

	accessClaims := map[string]interface{}{
		"sub":        u.sub,
		"iss":        iss,
		"client_id":  clientID,
		"origin_jti": originJTI,
		"event_id":   uid.New(),
		"token_use":  "access",
		"scope":      "aws.cognito.signin.user.admin",
		"auth_time":  now.Unix(),
		"exp":        exp.Unix(),
		"iat":        now.Unix(),
		"jti":        jti,
		"username":   u.username,
	}
	idClaims := map[string]interface{}{
		"sub":              u.sub,
		"iss":              iss,
		"aud":              clientID,
		"origin_jti":       originJTI,
		"auth_time":        now.Unix(),
		"exp":              exp.Unix(),
		"iat":              now.Unix(),
		"jti":              uid.New(),
		"cognito:username": u.username,
		"token_use":        "id",
		"email_verified":   true,
	}
	if email, ok := u.attributes["email"]; ok {
		idClaims["email"] = email
	}

	accessToken, err := signJWT(s.rsaKey, accessClaims)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "InternalErrorException", "failed to sign token")
		return
	}
	idToken, err := signJWT(s.rsaKey, idClaims)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "InternalErrorException", "failed to sign token")
		return
	}

	refreshJTI := uid.New()
	refreshToken := "refresh-" + refreshJTI

	s.mu.Lock()
	s.tokens[jti] = &tokenRecord{
		jti:        jti,
		username:   u.username,
		userPoolID: poolID,
		issuedAt:   now,
	}
	s.tokens[refreshJTI] = &tokenRecord{
		jti:        refreshJTI,
		username:   u.username,
		userPoolID: poolID,
		issuedAt:   now,
		isRefresh:  true,
	}
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{
		"AuthenticationResult": map[string]interface{}{
			"AccessToken":  accessToken,
			"IdToken":      idToken,
			"RefreshToken": refreshToken,
			"ExpiresIn":    3600,
			"TokenType":    "Bearer",
		},
	})
}

func (s *Service) getUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AccessToken string `json:"AccessToken"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	claims, err := parseJWTPayload(req.AccessToken)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "NotAuthorizedException", "Invalid access token.")
		return
	}

	jti, _ := claims["jti"].(string)
	username, _ := claims["username"].(string)
	iss, _ := claims["iss"].(string)
	// iss = "https://cognito-idp.{region}.amazonaws.com/{poolId}"
	poolID := iss[strings.LastIndex(iss, "/")+1:]

	s.mu.RLock()
	tok := s.tokens[jti]
	var u *user
	if poolUsers := s.users[poolID]; poolUsers != nil {
		u = poolUsers[username]
	}
	s.mu.RUnlock()

	if tok == nil || tok.revoked || u == nil {
		jsonError(w, http.StatusBadRequest, "NotAuthorizedException", "Invalid access token.")
		return
	}

	attrs := make([]map[string]string, 0, len(u.attributes)+1)
	attrs = append(attrs, map[string]string{"Name": "sub", "Value": u.sub})
	for k, v := range u.attributes {
		attrs = append(attrs, map[string]string{"Name": k, "Value": v})
	}

	jsonWrite(w, http.StatusOK, map[string]interface{}{
		"Username":       u.username,
		"UserAttributes": attrs,
	})
}

func (s *Service) globalSignOut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AccessToken string `json:"AccessToken"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	claims, err := parseJWTPayload(req.AccessToken)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "NotAuthorizedException", "Invalid access token.")
		return
	}

	username, _ := claims["username"].(string)
	iss, _ := claims["iss"].(string)
	poolID := iss[strings.LastIndex(iss, "/")+1:]

	s.mu.Lock()
	for _, tok := range s.tokens {
		if tok.userPoolID == poolID && tok.username == username {
			tok.revoked = true
		}
	}
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) revokeToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token    string `json:"Token"`
		ClientID string `json:"ClientId"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	// Our refresh tokens are "refresh-{jti}"
	if !strings.HasPrefix(req.Token, "refresh-") {
		jsonError(w, http.StatusBadRequest, "UnsupportedTokenTypeException", "Token must be a refresh token.")
		return
	}
	jti := strings.TrimPrefix(req.Token, "refresh-")

	s.mu.Lock()
	if tok, ok := s.tokens[jti]; ok {
		tok.revoked = true
	}
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) userDetail(u *user) map[string]interface{} {
	attrs := make([]map[string]string, 0, len(u.attributes)+1)
	attrs = append(attrs, map[string]string{"Name": "sub", "Value": u.sub})
	for k, v := range u.attributes {
		attrs = append(attrs, map[string]string{"Name": k, "Value": v})
	}
	status := "CONFIRMED"
	if !u.confirmed {
		status = "UNCONFIRMED"
	}
	return map[string]interface{}{
		"Username":             u.username,
		"UserAttributes":       attrs,
		"UserStatus":           status,
		"Enabled":              u.enabled,
		"UserCreateDate":       float64(u.createdAt.Unix()),
		"UserLastModifiedDate": float64(u.createdAt.Unix()),
	}
}
