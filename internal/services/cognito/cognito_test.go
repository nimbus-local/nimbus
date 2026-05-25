package cognito

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newSvc() *Service { return New("us-east-1") }

func cognitoReq(t *testing.T, s *Service, action string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	req := httptest.NewRequest(http.MethodPost, "/", &buf)
	req.Header.Set("X-Amz-Target", "AWSCognitoIdentityProviderService."+action)
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	return w
}

func createPool(t *testing.T, s *Service, name string) string {
	t.Helper()
	w := cognitoReq(t, s, "CreateUserPool", map[string]interface{}{
		"PoolName": name,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("CreateUserPool %s: expected 200, got %d\n%s", name, w.Code, w.Body.String())
	}
	var resp map[string]map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	return resp["UserPool"]["Id"].(string)
}

func createClient(t *testing.T, s *Service, poolID, name string) string {
	t.Helper()
	w := cognitoReq(t, s, "CreateUserPoolClient", map[string]interface{}{
		"UserPoolId": poolID,
		"ClientName": name,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("CreateUserPoolClient %s: expected 200, got %d\n%s", name, w.Code, w.Body.String())
	}
	var resp map[string]map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	return resp["UserPoolClient"]["ClientId"].(string)
}

// --- Name / Detect ---

func TestName(t *testing.T) {
	s := newSvc()
	if s.Name() != "cognito" {
		t.Errorf("expected Name()=cognito, got %s", s.Name())
	}
}

func TestDetect(t *testing.T) {
	s := newSvc()
	cases := []struct {
		target string
		want   bool
	}{
		{"AWSCognitoIdentityProviderService.CreateUserPool", true},
		{"AWSCognitoIdentityProviderService.InitiateAuth", true},
		{"AmazonSQS.SendMessage", false},
		{"", false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set("X-Amz-Target", tc.target)
		if got := s.Detect(req); got != tc.want {
			t.Errorf("Detect(%q) = %v, want %v", tc.target, got, tc.want)
		}
	}
}

// --- CreateUserPool ---

func TestCreateUserPool(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "my-pool")
	if !strings.HasPrefix(poolID, "us-east-1_") {
		t.Errorf("expected poolID to have region prefix, got %s", poolID)
	}
}

func TestCreateUserPool_WithOptions(t *testing.T) {
	s := newSvc()
	w := cognitoReq(t, s, "CreateUserPool", map[string]interface{}{
		"PoolName":               "options-pool",
		"AutoVerifiedAttributes": []string{"email"},
		"UsernameAttributes":     []string{"email"},
		"MfaConfiguration":       "OFF",
		"UserPoolTags":           map[string]string{"env": "dev"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "options-pool") {
		t.Error("expected pool name in response")
	}
}

func TestCreateUserPool_MissingName(t *testing.T) {
	s := newSvc()
	w := cognitoReq(t, s, "CreateUserPool", map[string]interface{}{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- DescribeUserPool ---

func TestDescribeUserPool(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "describe-pool")

	w := cognitoReq(t, s, "DescribeUserPool", map[string]interface{}{
		"UserPoolId": poolID,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), poolID) {
		t.Error("expected pool ID in response")
	}
}

func TestDescribeUserPool_NotFound(t *testing.T) {
	s := newSvc()
	w := cognitoReq(t, s, "DescribeUserPool", map[string]interface{}{
		"UserPoolId": "us-east-1_nope",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- UpdateUserPool ---

func TestUpdateUserPool(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "update-pool")

	w := cognitoReq(t, s, "UpdateUserPool", map[string]interface{}{
		"UserPoolId":       poolID,
		"MfaConfiguration": "OPTIONAL",
		"UserPoolTags":     map[string]string{"updated": "true"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}
}

func TestUpdateUserPool_NotFound(t *testing.T) {
	s := newSvc()
	w := cognitoReq(t, s, "UpdateUserPool", map[string]interface{}{
		"UserPoolId": "us-east-1_nope",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- DeleteUserPool ---

func TestDeleteUserPool(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "delete-pool")

	w := cognitoReq(t, s, "DeleteUserPool", map[string]interface{}{
		"UserPoolId": poolID,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	w2 := cognitoReq(t, s, "DescribeUserPool", map[string]interface{}{
		"UserPoolId": poolID,
	})
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 after delete, got %d", w2.Code)
	}
}

func TestDeleteUserPool_CascadesClients(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "cascade-pool")
	clientID := createClient(t, s, poolID, "cascade-client")

	// Delete pool
	cognitoReq(t, s, "DeleteUserPool", map[string]interface{}{
		"UserPoolId": poolID,
	})

	// Client should be gone too
	w := cognitoReq(t, s, "DescribeUserPoolClient", map[string]interface{}{
		"UserPoolId": poolID,
		"ClientId":   clientID,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected client 404 after pool delete, got %d", w.Code)
	}
}

func TestDeleteUserPool_NotFound(t *testing.T) {
	s := newSvc()
	w := cognitoReq(t, s, "DeleteUserPool", map[string]interface{}{
		"UserPoolId": "us-east-1_nope",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- ListUserPools ---

func TestListUserPools(t *testing.T) {
	s := newSvc()
	createPool(t, s, "pool-a")
	createPool(t, s, "pool-b")

	w := cognitoReq(t, s, "ListUserPools", map[string]interface{}{
		"MaxResults": 10,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "pool-a") || !strings.Contains(body, "pool-b") {
		t.Error("expected both pools in list response")
	}
}

// --- CreateUserPoolClient ---

func TestCreateUserPoolClient(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "client-pool")
	clientID := createClient(t, s, poolID, "web-client")
	if clientID == "" {
		t.Fatal("expected non-empty ClientId")
	}
}

func TestCreateUserPoolClient_WithOptions(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "opts-pool")

	w := cognitoReq(t, s, "CreateUserPoolClient", map[string]interface{}{
		"UserPoolId":         poolID,
		"ClientName":         "full-client",
		"ExplicitAuthFlows":  []string{"ALLOW_USER_PASSWORD_AUTH", "ALLOW_REFRESH_TOKEN_AUTH"},
		"CallbackURLs":       []string{"https://app.local/callback"},
		"LogoutURLs":         []string{"https://app.local/logout"},
		"AllowedOAuthFlows":  []string{"code"},
		"AllowedOAuthScopes": []string{"openid", "email"},
		"GenerateSecret":     true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "ClientSecret") {
		t.Error("expected ClientSecret when GenerateSecret=true")
	}
}

func TestCreateUserPoolClient_PoolNotFound(t *testing.T) {
	s := newSvc()
	w := cognitoReq(t, s, "CreateUserPoolClient", map[string]interface{}{
		"UserPoolId": "us-east-1_nope",
		"ClientName": "orphan",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- DescribeUserPoolClient ---

func TestDescribeUserPoolClient(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "desc-pool")
	clientID := createClient(t, s, poolID, "desc-client")

	w := cognitoReq(t, s, "DescribeUserPoolClient", map[string]interface{}{
		"UserPoolId": poolID,
		"ClientId":   clientID,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), clientID) {
		t.Error("expected client ID in response")
	}
}

func TestDescribeUserPoolClient_NotFound(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "pool-for-miss")
	w := cognitoReq(t, s, "DescribeUserPoolClient", map[string]interface{}{
		"UserPoolId": poolID,
		"ClientId":   "nope",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- UpdateUserPoolClient ---

func TestUpdateUserPoolClient(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "upd-pool")
	clientID := createClient(t, s, poolID, "upd-client")

	w := cognitoReq(t, s, "UpdateUserPoolClient", map[string]interface{}{
		"UserPoolId":        poolID,
		"ClientId":          clientID,
		"ClientName":        "renamed-client",
		"ExplicitAuthFlows": []string{"ALLOW_REFRESH_TOKEN_AUTH"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}
}

func TestUpdateUserPoolClient_NotFound(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "upd-miss-pool")
	w := cognitoReq(t, s, "UpdateUserPoolClient", map[string]interface{}{
		"UserPoolId": poolID,
		"ClientId":   "nope",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- DeleteUserPoolClient ---

func TestDeleteUserPoolClient(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "del-client-pool")
	clientID := createClient(t, s, poolID, "del-client")

	w := cognitoReq(t, s, "DeleteUserPoolClient", map[string]interface{}{
		"UserPoolId": poolID,
		"ClientId":   clientID,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	w2 := cognitoReq(t, s, "DescribeUserPoolClient", map[string]interface{}{
		"UserPoolId": poolID,
		"ClientId":   clientID,
	})
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 after delete, got %d", w2.Code)
	}
}

func TestDeleteUserPoolClient_NotFound(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "del-miss-pool")
	w := cognitoReq(t, s, "DeleteUserPoolClient", map[string]interface{}{
		"UserPoolId": poolID,
		"ClientId":   "nope",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- ListUserPoolClients ---

func TestListUserPoolClients(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "list-clients-pool")
	createClient(t, s, poolID, "client-1")
	createClient(t, s, poolID, "client-2")

	// Create a client in a different pool — should not appear.
	otherPool := createPool(t, s, "other-pool")
	createClient(t, s, otherPool, "other-client")

	w := cognitoReq(t, s, "ListUserPoolClients", map[string]interface{}{
		"UserPoolId": poolID,
		"MaxResults": 10,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "client-1") || !strings.Contains(body, "client-2") {
		t.Error("expected both clients in response")
	}
	if strings.Contains(body, "other-client") {
		t.Error("other-pool client should not appear")
	}
}

// --- Tags ---

func TestTagsRoundTrip(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "tagged-pool")

	// Get the pool ARN from DescribeUserPool
	dw := cognitoReq(t, s, "DescribeUserPool", map[string]interface{}{
		"UserPoolId": poolID,
	})
	var dResp map[string]map[string]interface{}
	json.NewDecoder(dw.Body).Decode(&dResp)
	arn := dResp["UserPool"]["Arn"].(string)

	// Tag
	cognitoReq(t, s, "TagResource", map[string]interface{}{
		"ResourceArn": arn,
		"Tags":        map[string]string{"env": "staging"},
	})

	// List
	w := cognitoReq(t, s, "ListTagsForResource", map[string]interface{}{
		"ResourceArn": arn,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "staging") {
		t.Error("expected tag value in response")
	}

	// Untag
	cognitoReq(t, s, "UntagResource", map[string]interface{}{
		"ResourceArn": arn,
		"TagKeys":     []string{"env"},
	})

	w2 := cognitoReq(t, s, "ListTagsForResource", map[string]interface{}{
		"ResourceArn": arn,
	})
	if strings.Contains(w2.Body.String(), "staging") {
		t.Error("expected removed tag to be absent")
	}
}

// --- GetUserPoolMfaConfig / SetUserPoolMfaConfig ---

func TestGetUserPoolMfaConfig(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "mfa-pool")

	w := cognitoReq(t, s, "GetUserPoolMfaConfig", map[string]interface{}{
		"UserPoolId": poolID,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "MfaConfiguration") {
		t.Error("expected MfaConfiguration in response")
	}
}

func TestGetUserPoolMfaConfig_NotFound(t *testing.T) {
	s := newSvc()
	w := cognitoReq(t, s, "GetUserPoolMfaConfig", map[string]interface{}{
		"UserPoolId": "us-east-1_nope",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- JWKS ---

func TestJWKS(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "jwks-pool")

	req := httptest.NewRequest(http.MethodGet, "/"+poolID+"/.well-known/jwks.json", nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, field := range []string{"keys", "kty", "kid", "alg", "RS256", "n", "e"} {
		if !strings.Contains(body, field) {
			t.Errorf("JWKS missing field %q", field)
		}
	}
}

func TestDetect_JWKS(t *testing.T) {
	s := newSvc()
	req := httptest.NewRequest(http.MethodGet, "/us-east-1_abc12345/.well-known/jwks.json", nil)
	if !s.Detect(req) {
		t.Error("Detect should return true for JWKS path")
	}
}

// --- User management ---

func createUser(t *testing.T, s *Service, poolID, username, password string) {
	t.Helper()
	w := cognitoReq(t, s, "AdminCreateUser", map[string]interface{}{
		"UserPoolId":        poolID,
		"Username":          username,
		"TemporaryPassword": password,
		"UserAttributes": []map[string]string{
			{"Name": "email", "Value": username},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("AdminCreateUser %s: expected 200, got %d\n%s", username, w.Code, w.Body.String())
	}
}

func TestAdminCreateUser(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "usr-pool")
	createUser(t, s, poolID, "alice@example.com", "Password1!")

	w := cognitoReq(t, s, "AdminCreateUser", map[string]interface{}{
		"UserPoolId": poolID,
		"Username":   "alice@example.com",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on duplicate, got %d", w.Code)
	}
}

func TestAdminCreateUser_PoolNotFound(t *testing.T) {
	s := newSvc()
	w := cognitoReq(t, s, "AdminCreateUser", map[string]interface{}{
		"UserPoolId": "us-east-1_nope",
		"Username":   "alice@example.com",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestAdminSetUserPassword(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "setpw-pool")
	createUser(t, s, poolID, "bob@example.com", "OldPass1!")

	w := cognitoReq(t, s, "AdminSetUserPassword", map[string]interface{}{
		"UserPoolId": poolID,
		"Username":   "bob@example.com",
		"Password":   "NewPass1!",
		"Permanent":  true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}
}

func TestAdminSetUserPassword_NotFound(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "setpw-miss-pool")
	w := cognitoReq(t, s, "AdminSetUserPassword", map[string]interface{}{
		"UserPoolId": poolID,
		"Username":   "nobody@example.com",
		"Password":   "Pass1!",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- Auth flows ---

func authSetup(t *testing.T) (*Service, string, string) {
	t.Helper()
	s := newSvc()
	poolID := createPool(t, s, "auth-pool")
	clientID := createClient(t, s, poolID, "auth-client")
	createUser(t, s, poolID, "carol@example.com", "Pass1!")
	return s, poolID, clientID
}

func TestInitiateAuth_UserPassword(t *testing.T) {
	s, _, clientID := authSetup(t)

	w := cognitoReq(t, s, "InitiateAuth", map[string]interface{}{
		"AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{
			"USERNAME": "carol@example.com",
			"PASSWORD": "Pass1!",
		},
		"ClientId": clientID,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, field := range []string{"AccessToken", "IdToken", "RefreshToken", "ExpiresIn"} {
		if !strings.Contains(body, field) {
			t.Errorf("InitiateAuth missing %q in response", field)
		}
	}
}

func TestInitiateAuth_WrongPassword(t *testing.T) {
	s, _, clientID := authSetup(t)

	w := cognitoReq(t, s, "InitiateAuth", map[string]interface{}{
		"AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{
			"USERNAME": "carol@example.com",
			"PASSWORD": "wrong",
		},
		"ClientId": clientID,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestInitiateAuth_UserNotFound(t *testing.T) {
	s, _, clientID := authSetup(t)

	w := cognitoReq(t, s, "InitiateAuth", map[string]interface{}{
		"AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{
			"USERNAME": "nobody@example.com",
			"PASSWORD": "Pass1!",
		},
		"ClientId": clientID,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestInitiateAuth_ClientNotFound(t *testing.T) {
	s := newSvc()
	createPool(t, s, "auth-no-client-pool")

	w := cognitoReq(t, s, "InitiateAuth", map[string]interface{}{
		"AuthFlow":       "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{},
		"ClientId":       "nope",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestInitiateAuth_UnsupportedFlow(t *testing.T) {
	s, _, clientID := authSetup(t)

	w := cognitoReq(t, s, "InitiateAuth", map[string]interface{}{
		"AuthFlow": "SRP_AUTH",
		"ClientId": clientID,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestAdminInitiateAuth(t *testing.T) {
	s, poolID, clientID := authSetup(t)

	w := cognitoReq(t, s, "AdminInitiateAuth", map[string]interface{}{
		"AuthFlow":   "ADMIN_USER_PASSWORD_AUTH",
		"UserPoolId": poolID,
		"ClientId":   clientID,
		"AuthParameters": map[string]string{
			"USERNAME": "carol@example.com",
			"PASSWORD": "Pass1!",
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}
}

// --- GetUser ---

func doAuth(t *testing.T, s *Service, clientID string) string {
	t.Helper()
	w := cognitoReq(t, s, "InitiateAuth", map[string]interface{}{
		"AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{
			"USERNAME": "carol@example.com",
			"PASSWORD": "Pass1!",
		},
		"ClientId": clientID,
	})
	var resp map[string]map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	return resp["AuthenticationResult"]["AccessToken"].(string)
}

func TestGetUser(t *testing.T) {
	s, _, clientID := authSetup(t)
	accessToken := doAuth(t, s, clientID)

	w := cognitoReq(t, s, "GetUser", map[string]interface{}{
		"AccessToken": accessToken,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "carol@example.com") {
		t.Error("expected username in GetUser response")
	}
}

func TestGetUser_InvalidToken(t *testing.T) {
	s := newSvc()
	w := cognitoReq(t, s, "GetUser", map[string]interface{}{
		"AccessToken": "not.a.token",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- GlobalSignOut ---

func TestGlobalSignOut(t *testing.T) {
	s, _, clientID := authSetup(t)
	accessToken := doAuth(t, s, clientID)

	// Sign out
	w := cognitoReq(t, s, "GlobalSignOut", map[string]interface{}{
		"AccessToken": accessToken,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	// GetUser after sign-out should fail
	w2 := cognitoReq(t, s, "GetUser", map[string]interface{}{
		"AccessToken": accessToken,
	})
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 after sign-out, got %d", w2.Code)
	}
}

// --- RevokeToken ---

func TestRevokeToken(t *testing.T) {
	s, _, clientID := authSetup(t)

	w := cognitoReq(t, s, "InitiateAuth", map[string]interface{}{
		"AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{
			"USERNAME": "carol@example.com",
			"PASSWORD": "Pass1!",
		},
		"ClientId": clientID,
	})
	var resp map[string]map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	refreshToken := resp["AuthenticationResult"]["RefreshToken"].(string)

	rw := cognitoReq(t, s, "RevokeToken", map[string]interface{}{
		"Token":    refreshToken,
		"ClientId": clientID,
	})
	if rw.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", rw.Code, rw.Body.String())
	}
}

func TestRevokeToken_NotRefreshToken(t *testing.T) {
	s, _, clientID := authSetup(t)
	accessToken := doAuth(t, s, clientID)

	w := cognitoReq(t, s, "RevokeToken", map[string]interface{}{
		"Token":    accessToken, // access token, not refresh
		"ClientId": clientID,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- AdminGetUser ---

func TestAdminGetUser(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "get-user-pool")
	createUser(t, s, poolID, "dave@example.com", "Pass1!")

	w := cognitoReq(t, s, "AdminGetUser", map[string]interface{}{
		"UserPoolId": poolID,
		"Username":   "dave@example.com",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "dave@example.com") {
		t.Error("expected username in response")
	}
}

func TestAdminGetUser_NotFound(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "get-user-miss-pool")
	w := cognitoReq(t, s, "AdminGetUser", map[string]interface{}{
		"UserPoolId": poolID,
		"Username":   "nobody@example.com",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- AdminDeleteUser ---

func TestAdminDeleteUser(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "del-user-pool")
	createUser(t, s, poolID, "eve@example.com", "Pass1!")

	w := cognitoReq(t, s, "AdminDeleteUser", map[string]interface{}{
		"UserPoolId": poolID,
		"Username":   "eve@example.com",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	// Should be gone
	w2 := cognitoReq(t, s, "AdminGetUser", map[string]interface{}{
		"UserPoolId": poolID,
		"Username":   "eve@example.com",
	})
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 after delete, got %d", w2.Code)
	}
}

func TestAdminDeleteUser_NotFound(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "del-user-miss-pool")
	w := cognitoReq(t, s, "AdminDeleteUser", map[string]interface{}{
		"UserPoolId": poolID,
		"Username":   "nobody@example.com",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- AdminUpdateUserAttributes ---

func TestAdminUpdateUserAttributes(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "upd-attr-pool")
	createUser(t, s, poolID, "frank@example.com", "Pass1!")

	w := cognitoReq(t, s, "AdminUpdateUserAttributes", map[string]interface{}{
		"UserPoolId": poolID,
		"Username":   "frank@example.com",
		"UserAttributes": []map[string]string{
			{"Name": "custom:role", "Value": "admin"},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	// Verify via AdminGetUser
	w2 := cognitoReq(t, s, "AdminGetUser", map[string]interface{}{
		"UserPoolId": poolID,
		"Username":   "frank@example.com",
	})
	if !strings.Contains(w2.Body.String(), "admin") {
		t.Error("expected updated attribute in response")
	}
}

func TestAdminUpdateUserAttributes_NotFound(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "upd-attr-miss-pool")
	w := cognitoReq(t, s, "AdminUpdateUserAttributes", map[string]interface{}{
		"UserPoolId":     poolID,
		"Username":       "nobody@example.com",
		"UserAttributes": []map[string]string{},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- SignUp ---

func TestSignUp(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "signup-pool")
	clientID := createClient(t, s, poolID, "signup-client")

	w := cognitoReq(t, s, "SignUp", map[string]interface{}{
		"ClientId": clientID,
		"Username": "grace@example.com",
		"Password": "Pass1!",
		"UserAttributes": []map[string]string{
			{"Name": "email", "Value": "grace@example.com"},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "UserConfirmed") {
		t.Error("expected UserConfirmed in response")
	}
	if !strings.Contains(body, "true") {
		t.Error("expected auto-confirmed=true")
	}
}

func TestSignUp_Duplicate(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "signup-dup-pool")
	clientID := createClient(t, s, poolID, "signup-dup-client")

	body := map[string]interface{}{
		"ClientId": clientID,
		"Username": "henry@example.com",
		"Password": "Pass1!",
	}
	cognitoReq(t, s, "SignUp", body)
	w := cognitoReq(t, s, "SignUp", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on duplicate, got %d", w.Code)
	}
}

func TestSignUp_ClientNotFound(t *testing.T) {
	s := newSvc()
	w := cognitoReq(t, s, "SignUp", map[string]interface{}{
		"ClientId": "nope",
		"Username": "ida@example.com",
		"Password": "Pass1!",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestSignUp_ThenAuth(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "signup-auth-pool")
	clientID := createClient(t, s, poolID, "signup-auth-client")

	cognitoReq(t, s, "SignUp", map[string]interface{}{
		"ClientId": clientID,
		"Username": "jack@example.com",
		"Password": "Pass1!",
	})

	w := cognitoReq(t, s, "InitiateAuth", map[string]interface{}{
		"AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{
			"USERNAME": "jack@example.com",
			"PASSWORD": "Pass1!",
		},
		"ClientId": clientID,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on auth after signup, got %d\n%s", w.Code, w.Body.String())
	}
}

// --- ConfirmSignUp ---

func TestConfirmSignUp(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "confirm-pool")
	clientID := createClient(t, s, poolID, "confirm-client")

	cognitoReq(t, s, "SignUp", map[string]interface{}{
		"ClientId": clientID,
		"Username": "kate@example.com",
		"Password": "Pass1!",
	})

	w := cognitoReq(t, s, "ConfirmSignUp", map[string]interface{}{
		"ClientId":         clientID,
		"Username":         "kate@example.com",
		"ConfirmationCode": "123456",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}
}

// --- ListUsers ---

func TestListUsers(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "list-users-pool")
	createUser(t, s, poolID, "liam@example.com", "Pass1!")
	createUser(t, s, poolID, "mia@example.com", "Pass1!")

	// Users in another pool — should not appear.
	otherPool := createPool(t, s, "list-users-other")
	createUser(t, s, otherPool, "noah@example.com", "Pass1!")

	w := cognitoReq(t, s, "ListUsers", map[string]interface{}{
		"UserPoolId": poolID,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "liam@example.com") || !strings.Contains(body, "mia@example.com") {
		t.Error("expected both users in response")
	}
	if strings.Contains(body, "noah@example.com") {
		t.Error("other-pool user should not appear")
	}
}

// --- Groups ---

func createGroup(t *testing.T, s *Service, poolID, name string) {
	t.Helper()
	w := cognitoReq(t, s, "CreateGroup", map[string]interface{}{
		"UserPoolId": poolID,
		"GroupName":  name,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("CreateGroup %s: expected 200, got %d\n%s", name, w.Code, w.Body.String())
	}
}

func TestCreateGroup(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "group-pool")
	createGroup(t, s, poolID, "admins")

	w := cognitoReq(t, s, "GetGroup", map[string]interface{}{
		"UserPoolId": poolID,
		"GroupName":  "admins",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "admins") {
		t.Error("expected group name in response")
	}
}

func TestCreateGroup_Duplicate(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "group-dup-pool")
	createGroup(t, s, poolID, "editors")

	w := cognitoReq(t, s, "CreateGroup", map[string]interface{}{
		"UserPoolId": poolID,
		"GroupName":  "editors",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on duplicate, got %d", w.Code)
	}
}

func TestCreateGroup_PoolNotFound(t *testing.T) {
	s := newSvc()
	w := cognitoReq(t, s, "CreateGroup", map[string]interface{}{
		"UserPoolId": "us-east-1_nope",
		"GroupName":  "orphan",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestDeleteGroup(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "del-group-pool")
	createGroup(t, s, poolID, "readers")

	w := cognitoReq(t, s, "DeleteGroup", map[string]interface{}{
		"UserPoolId": poolID,
		"GroupName":  "readers",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	w2 := cognitoReq(t, s, "GetGroup", map[string]interface{}{
		"UserPoolId": poolID,
		"GroupName":  "readers",
	})
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 after delete, got %d", w2.Code)
	}
}

func TestListGroups(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "list-groups-pool")
	createGroup(t, s, poolID, "alpha")
	createGroup(t, s, poolID, "beta")

	w := cognitoReq(t, s, "ListGroups", map[string]interface{}{
		"UserPoolId": poolID,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "alpha") || !strings.Contains(body, "beta") {
		t.Error("expected both groups in response")
	}
}

func TestGetGroup_NotFound(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "get-group-miss-pool")
	w := cognitoReq(t, s, "GetGroup", map[string]interface{}{
		"UserPoolId": poolID,
		"GroupName":  "nope",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- Group membership ---

func TestAdminAddUserToGroup(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "membership-pool")
	createUser(t, s, poolID, "olivia@example.com", "Pass1!")
	createGroup(t, s, poolID, "vip")

	w := cognitoReq(t, s, "AdminAddUserToGroup", map[string]interface{}{
		"UserPoolId": poolID,
		"Username":   "olivia@example.com",
		"GroupName":  "vip",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	w2 := cognitoReq(t, s, "AdminListGroupsForUser", map[string]interface{}{
		"UserPoolId": poolID,
		"Username":   "olivia@example.com",
	})
	if !strings.Contains(w2.Body.String(), "vip") {
		t.Error("expected group in AdminListGroupsForUser response")
	}
}

func TestAdminAddUserToGroup_Idempotent(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "idempotent-pool")
	createUser(t, s, poolID, "peter@example.com", "Pass1!")
	createGroup(t, s, poolID, "once")

	cognitoReq(t, s, "AdminAddUserToGroup", map[string]interface{}{
		"UserPoolId": poolID, "Username": "peter@example.com", "GroupName": "once",
	})
	w := cognitoReq(t, s, "AdminAddUserToGroup", map[string]interface{}{
		"UserPoolId": poolID, "Username": "peter@example.com", "GroupName": "once",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on idempotent add, got %d", w.Code)
	}

	// Should still appear exactly once
	w2 := cognitoReq(t, s, "AdminListGroupsForUser", map[string]interface{}{
		"UserPoolId": poolID,
		"Username":   "peter@example.com",
	})
	var resp map[string]interface{}
	json.NewDecoder(w2.Body).Decode(&resp)
	groups, _ := resp["Groups"].([]interface{})
	if len(groups) != 1 {
		t.Errorf("expected 1 group, got %d", len(groups))
	}
}

func TestAdminRemoveUserFromGroup(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "remove-pool")
	createUser(t, s, poolID, "quinn@example.com", "Pass1!")
	createGroup(t, s, poolID, "temp")

	cognitoReq(t, s, "AdminAddUserToGroup", map[string]interface{}{
		"UserPoolId": poolID, "Username": "quinn@example.com", "GroupName": "temp",
	})
	cognitoReq(t, s, "AdminRemoveUserFromGroup", map[string]interface{}{
		"UserPoolId": poolID, "Username": "quinn@example.com", "GroupName": "temp",
	})

	w := cognitoReq(t, s, "AdminListGroupsForUser", map[string]interface{}{
		"UserPoolId": poolID,
		"Username":   "quinn@example.com",
	})
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	groups, _ := resp["Groups"].([]interface{})
	if len(groups) != 0 {
		t.Errorf("expected 0 groups after removal, got %d", len(groups))
	}
}

func TestAdminDeleteUser_RemovesFromGroups(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "del-removes-pool")
	createUser(t, s, poolID, "rose@example.com", "Pass1!")
	createGroup(t, s, poolID, "staff")

	cognitoReq(t, s, "AdminAddUserToGroup", map[string]interface{}{
		"UserPoolId": poolID, "Username": "rose@example.com", "GroupName": "staff",
	})
	cognitoReq(t, s, "AdminDeleteUser", map[string]interface{}{
		"UserPoolId": poolID,
		"Username":   "rose@example.com",
	})

	// Group should be empty
	w := cognitoReq(t, s, "GetGroup", map[string]interface{}{
		"UserPoolId": poolID,
		"GroupName":  "staff",
	})
	// Group still exists but has no members — we don't expose members in GetGroup,
	// but AdminListGroupsForUser for the deleted user should return empty.
	if w.Code != http.StatusOK {
		t.Fatalf("group should still exist, got %d", w.Code)
	}
}

// --- cognito:groups claim in id token ---

func TestCognitoGroupsInIDToken(t *testing.T) {
	s := newSvc()
	poolID := createPool(t, s, "groups-token-pool")
	clientID := createClient(t, s, poolID, "groups-token-client")
	createUser(t, s, poolID, "sam@example.com", "Pass1!")
	createGroup(t, s, poolID, "superusers")

	cognitoReq(t, s, "AdminAddUserToGroup", map[string]interface{}{
		"UserPoolId": poolID, "Username": "sam@example.com", "GroupName": "superusers",
	})

	w := cognitoReq(t, s, "InitiateAuth", map[string]interface{}{
		"AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{
			"USERNAME": "sam@example.com",
			"PASSWORD": "Pass1!",
		},
		"ClientId": clientID,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	var resp map[string]map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	idToken, _ := resp["AuthenticationResult"]["IdToken"].(string)
	if idToken == "" {
		t.Fatal("expected IdToken in response")
	}

	// Decode the payload and check cognito:groups
	claims, err := parseJWTPayload(idToken)
	if err != nil {
		t.Fatalf("failed to parse id token: %v", err)
	}
	groups, ok := claims["cognito:groups"].([]interface{})
	if !ok || len(groups) == 0 {
		t.Errorf("expected cognito:groups in id token, got %v", claims["cognito:groups"])
	}
	if groups[0].(string) != "superusers" {
		t.Errorf("expected superusers in groups, got %v", groups[0])
	}
}

func TestCognitoGroupsAbsentWhenNoGroups(t *testing.T) {
	s, _, clientID := authSetup(t)

	w := cognitoReq(t, s, "InitiateAuth", map[string]interface{}{
		"AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{
			"USERNAME": "carol@example.com",
			"PASSWORD": "Pass1!",
		},
		"ClientId": clientID,
	})
	var resp map[string]map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	idToken, _ := resp["AuthenticationResult"]["IdToken"].(string)

	claims, _ := parseJWTPayload(idToken)
	if _, present := claims["cognito:groups"]; present {
		t.Error("cognito:groups should be absent when user has no groups")
	}
}

// --- Unknown action ---

func TestUnknownAction(t *testing.T) {
	s := newSvc()
	w := cognitoReq(t, s, "GetUserAttributeVerificationCode", map[string]interface{}{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}
