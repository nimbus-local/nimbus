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

// --- Unknown action ---

func TestUnknownAction(t *testing.T) {
	s := newSvc()
	w := cognitoReq(t, s, "GetUserAttributeVerificationCode", map[string]interface{}{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}
