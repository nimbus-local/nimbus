package apigateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestV2DetectAndRoute(t *testing.T) {
	svc, _ := newTestService()

	cases := []struct {
		path string
		want bool
	}{
		{"/apis", true},
		{"/apis/abc123", true},
		{"/restapis", true},
		{"/restapis/abc123", true},
		{"/v2/apis", true},
		{"/v2/apis/abc123", true},
		{"/v2/apis/abc123/routes", true},
		{"/other", false},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(http.MethodGet, tc.path, nil)
		if got := svc.Detect(r); got != tc.want {
			t.Errorf("Detect(%q): want %v, got %v", tc.path, tc.want, got)
		}
	}
}

// TestV2PrefixCreateAndGet exercises the /v2/apis prefix used by AWS SDK Go v2 (Pulumi).
func TestV2PrefixCreateAndGet(t *testing.T) {
	svc, _ := newTestService()

	// CreateApi via /v2/apis
	w := do(svc, http.MethodPost, "/v2/apis", map[string]string{
		"name":         "pulumi-api",
		"protocolType": "HTTP",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateApi via /v2/apis: want 201, got %d — %s", w.Code, w.Body)
	}
	var api HTTPApi
	json.NewDecoder(w.Body).Decode(&api)
	if api.ApiId == "" {
		t.Fatal("expected non-empty apiId")
	}

	// GetApi via /v2/apis/{id}
	w2 := do(svc, http.MethodGet, "/v2/apis/"+api.ApiId, nil)
	if w2.Code != http.StatusOK {
		t.Fatalf("GetApi via /v2/apis/{id}: want 200, got %d", w2.Code)
	}

	// GetApis via /v2/apis — should list the API created above
	w3 := do(svc, http.MethodGet, "/v2/apis", nil)
	if w3.Code != http.StatusOK {
		t.Fatalf("GetApis via /v2/apis: want 200, got %d", w3.Code)
	}
	var list map[string]any
	json.NewDecoder(w3.Body).Decode(&list)
	if len(list["items"].([]any)) != 1 {
		t.Fatalf("want 1 API, got %d", len(list["items"].([]any)))
	}

	// CreateRoute via /v2/apis/{id}/routes
	w4 := do(svc, http.MethodPost, "/v2/apis/"+api.ApiId+"/routes",
		map[string]string{"routeKey": "GET /hello"})
	if w4.Code != http.StatusCreated {
		t.Fatalf("CreateRoute via /v2/: want 201, got %d — %s", w4.Code, w4.Body)
	}

	// DeleteApi via /v2/apis/{id}
	w5 := do(svc, http.MethodDelete, "/v2/apis/"+api.ApiId, nil)
	if w5.Code != http.StatusNoContent {
		t.Fatalf("DeleteApi via /v2/apis/{id}: want 204, got %d", w5.Code)
	}
	w6 := do(svc, http.MethodGet, "/v2/apis/"+api.ApiId, nil)
	if w6.Code != http.StatusNotFound {
		t.Fatalf("GetApi after delete via /v2/: want 404, got %d", w6.Code)
	}
}

func TestCreateAndGetHTTPApi(t *testing.T) {
	svc, _ := newTestService()

	w := do(svc, http.MethodPost, "/apis", map[string]string{
		"name":         "my-http-api",
		"protocolType": "HTTP",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateApi: want 201, got %d — %s", w.Code, w.Body)
	}

	var api HTTPApi
	json.NewDecoder(w.Body).Decode(&api)
	if api.ApiId == "" {
		t.Fatal("expected non-empty apiId")
	}
	if api.ProtocolType != "HTTP" {
		t.Fatalf("want protocolType HTTP, got %q", api.ProtocolType)
	}

	// GetApi
	w2 := do(svc, http.MethodGet, "/apis/"+api.ApiId, nil)
	if w2.Code != http.StatusOK {
		t.Fatalf("GetApi: want 200, got %d", w2.Code)
	}

	// GetApis
	w3 := do(svc, http.MethodGet, "/apis", nil)
	if w3.Code != http.StatusOK {
		t.Fatalf("GetApis: want 200, got %d", w3.Code)
	}
	var list map[string]any
	json.NewDecoder(w3.Body).Decode(&list)
	if len(list["items"].([]any)) != 1 {
		t.Fatalf("want 1 API, got %d", len(list["items"].([]any)))
	}
}

func TestDeleteHTTPApi(t *testing.T) {
	svc, _ := newTestService()
	w := do(svc, http.MethodPost, "/apis", map[string]string{"name": "x"})
	var api HTTPApi
	json.NewDecoder(w.Body).Decode(&api)

	w2 := do(svc, http.MethodDelete, "/apis/"+api.ApiId, nil)
	if w2.Code != http.StatusNoContent {
		t.Fatalf("DeleteApi: want 204, got %d", w2.Code)
	}

	w3 := do(svc, http.MethodGet, "/apis/"+api.ApiId, nil)
	if w3.Code != http.StatusNotFound {
		t.Fatalf("GetApi after delete: want 404, got %d", w3.Code)
	}
}

func TestRouteLifecycle(t *testing.T) {
	svc, _ := newTestService()

	w := do(svc, http.MethodPost, "/apis", map[string]string{"name": "x"})
	var api HTTPApi
	json.NewDecoder(w.Body).Decode(&api)

	// CreateRoute
	w2 := do(svc, http.MethodPost, fmt.Sprintf("/apis/%s/routes", api.ApiId),
		map[string]string{"routeKey": "GET /hello"})
	if w2.Code != http.StatusCreated {
		t.Fatalf("CreateRoute: want 201, got %d — %s", w2.Code, w2.Body)
	}
	var route V2Route
	json.NewDecoder(w2.Body).Decode(&route)
	if route.RouteKey != "GET /hello" {
		t.Fatalf("want routeKey 'GET /hello', got %q", route.RouteKey)
	}

	// GetRoutes
	w3 := do(svc, http.MethodGet, fmt.Sprintf("/apis/%s/routes", api.ApiId), nil)
	if w3.Code != http.StatusOK {
		t.Fatalf("GetRoutes: want 200, got %d", w3.Code)
	}

	// GetRoute
	w4 := do(svc, http.MethodGet, fmt.Sprintf("/apis/%s/routes/%s", api.ApiId, route.RouteId), nil)
	if w4.Code != http.StatusOK {
		t.Fatalf("GetRoute: want 200, got %d", w4.Code)
	}

	// DeleteRoute
	w5 := do(svc, http.MethodDelete, fmt.Sprintf("/apis/%s/routes/%s", api.ApiId, route.RouteId), nil)
	if w5.Code != http.StatusNoContent {
		t.Fatalf("DeleteRoute: want 204, got %d", w5.Code)
	}
}

func TestIntegrationLifecycle(t *testing.T) {
	svc, _ := newTestService()

	w := do(svc, http.MethodPost, "/apis", map[string]string{"name": "x"})
	var api HTTPApi
	json.NewDecoder(w.Body).Decode(&api)

	// CreateIntegration
	w2 := do(svc, http.MethodPost, fmt.Sprintf("/apis/%s/integrations", api.ApiId),
		map[string]string{
			"integrationType":      "AWS_PROXY",
			"integrationUri":       "arn:aws:apigateway:us-east-1:lambda:path/2015-03-31/functions/arn:aws:lambda:us-east-1:000000000000:function:my-func/invocations",
			"payloadFormatVersion": "2.0",
		})
	if w2.Code != http.StatusCreated {
		t.Fatalf("CreateIntegration: want 201, got %d — %s", w2.Code, w2.Body)
	}
	var integ V2Integration
	json.NewDecoder(w2.Body).Decode(&integ)
	if integ.IntegrationId == "" {
		t.Fatal("expected integrationId")
	}

	// GetIntegration
	w3 := do(svc, http.MethodGet, fmt.Sprintf("/apis/%s/integrations/%s", api.ApiId, integ.IntegrationId), nil)
	if w3.Code != http.StatusOK {
		t.Fatalf("GetIntegration: want 200, got %d", w3.Code)
	}

	// DeleteIntegration
	w4 := do(svc, http.MethodDelete, fmt.Sprintf("/apis/%s/integrations/%s", api.ApiId, integ.IntegrationId), nil)
	if w4.Code != http.StatusNoContent {
		t.Fatalf("DeleteIntegration: want 204, got %d", w4.Code)
	}
}

func TestV2StageAndDeployment(t *testing.T) {
	svc, _ := newTestService()

	w := do(svc, http.MethodPost, "/apis", map[string]string{"name": "x"})
	var api HTTPApi
	json.NewDecoder(w.Body).Decode(&api)

	// CreateStage
	w2 := do(svc, http.MethodPost, fmt.Sprintf("/apis/%s/stages", api.ApiId),
		map[string]any{"stageName": "prod", "autoDeploy": true})
	if w2.Code != http.StatusCreated {
		t.Fatalf("CreateStage: want 201, got %d — %s", w2.Code, w2.Body)
	}
	var stage V2Stage
	json.NewDecoder(w2.Body).Decode(&stage)
	if stage.StageName != "prod" {
		t.Fatalf("want stageName 'prod', got %q", stage.StageName)
	}

	// GetStage
	w3 := do(svc, http.MethodGet, fmt.Sprintf("/apis/%s/stages/prod", api.ApiId), nil)
	if w3.Code != http.StatusOK {
		t.Fatalf("GetStage: want 200, got %d", w3.Code)
	}

	// CreateDeployment
	w4 := do(svc, http.MethodPost, fmt.Sprintf("/apis/%s/deployments", api.ApiId),
		map[string]string{"description": "first deploy", "stageName": "prod"})
	if w4.Code != http.StatusCreated {
		t.Fatalf("CreateDeployment: want 201, got %d — %s", w4.Code, w4.Body)
	}
	var dep V2Deployment
	json.NewDecoder(w4.Body).Decode(&dep)
	if dep.DeploymentStatus != "DEPLOYED" {
		t.Fatalf("want status DEPLOYED, got %q", dep.DeploymentStatus)
	}
}

func TestV2LambdaProxyExecution(t *testing.T) {
	svc, ml := newTestService()
	ml.responses["handler"] = []byte(`{"statusCode":200,"headers":{"Content-Type":"application/json"},"body":"{\"ok\":true}"}`)

	// Create API
	w := do(svc, http.MethodPost, "/apis", map[string]string{"name": "x"})
	var api HTTPApi
	json.NewDecoder(w.Body).Decode(&api)

	// Create integration
	w2 := do(svc, http.MethodPost, fmt.Sprintf("/apis/%s/integrations", api.ApiId),
		map[string]string{
			"integrationType":      "AWS_PROXY",
			"integrationUri":       "arn:aws:apigateway:us-east-1:lambda:path/2015-03-31/functions/arn:aws:lambda:us-east-1:000000000000:function:handler/invocations",
			"payloadFormatVersion": "2.0",
		})
	var integ V2Integration
	json.NewDecoder(w2.Body).Decode(&integ)

	// Create route pointing to integration
	do(svc, http.MethodPost, fmt.Sprintf("/apis/%s/routes", api.ApiId),
		map[string]string{
			"routeKey": "GET /ping",
			"target":   "integrations/" + integ.IntegrationId,
		})

	// Create stage
	do(svc, http.MethodPost, fmt.Sprintf("/apis/%s/stages", api.ApiId),
		map[string]any{"stageName": "dev"})

	// Execute
	execPath := fmt.Sprintf("/apis/%s/dev/_user_request_/ping", api.ApiId)
	req := httptest.NewRequest(http.MethodGet, execPath, nil)
	w3 := httptest.NewRecorder()
	svc.ServeHTTP(w3, req)

	if w3.Code != http.StatusOK {
		t.Fatalf("v2 execution: want 200, got %d — %s", w3.Code, w3.Body)
	}

	// Verify it got a v2 event
	if len(ml.calls) != 1 {
		t.Fatalf("want 1 Lambda call, got %d", len(ml.calls))
	}
	var event v2ProxyEvent
	json.Unmarshal(ml.calls[0].Payload, &event)
	if event.Version != "2.0" {
		t.Fatalf("want event version 2.0, got %q", event.Version)
	}
	if event.RouteKey != "GET /ping" {
		t.Fatalf("want routeKey 'GET /ping', got %q", event.RouteKey)
	}
	if event.RequestContext.Http.Method != "GET" {
		t.Fatalf("want http.method GET, got %q", event.RequestContext.Http.Method)
	}
}

func TestV2PathParameterExecution(t *testing.T) {
	svc, ml := newTestService()
	ml.responses["handler"] = []byte(`{"statusCode":200,"body":"ok"}`)

	w := do(svc, http.MethodPost, "/apis", map[string]string{"name": "x"})
	var api HTTPApi
	json.NewDecoder(w.Body).Decode(&api)

	w2 := do(svc, http.MethodPost, fmt.Sprintf("/apis/%s/integrations", api.ApiId),
		map[string]string{
			"integrationType": "AWS_PROXY",
			"integrationUri":  "arn:aws:apigateway:us-east-1:lambda:path/2015-03-31/functions/arn:aws:lambda:us-east-1:000000000000:function:handler/invocations",
		})
	var integ V2Integration
	json.NewDecoder(w2.Body).Decode(&integ)

	do(svc, http.MethodPost, fmt.Sprintf("/apis/%s/routes", api.ApiId),
		map[string]string{
			"routeKey": "DELETE /users/{userId}",
			"target":   "integrations/" + integ.IntegrationId,
		})
	do(svc, http.MethodPost, fmt.Sprintf("/apis/%s/stages", api.ApiId),
		map[string]any{"stageName": "v1"})

	execPath := fmt.Sprintf("/apis/%s/v1/_user_request_/users/99", api.ApiId)
	req := httptest.NewRequest(http.MethodDelete, execPath, nil)
	w3 := httptest.NewRecorder()
	svc.ServeHTTP(w3, req)

	if w3.Code != http.StatusOK {
		t.Fatalf("v2 path param execution: want 200, got %d — %s", w3.Code, w3.Body)
	}

	var event v2ProxyEvent
	json.Unmarshal(ml.calls[0].Payload, &event)
	if event.PathParameters["userId"] != "99" {
		t.Fatalf("want userId=99, got %v", event.PathParameters)
	}
}

func TestV2DefaultRoute(t *testing.T) {
	svc, ml := newTestService()
	ml.responses["catch-all"] = []byte(`{"statusCode":200,"body":"caught"}`)

	w := do(svc, http.MethodPost, "/apis", map[string]string{"name": "x"})
	var api HTTPApi
	json.NewDecoder(w.Body).Decode(&api)

	w2 := do(svc, http.MethodPost, fmt.Sprintf("/apis/%s/integrations", api.ApiId),
		map[string]string{
			"integrationType": "AWS_PROXY",
			"integrationUri":  "arn:aws:apigateway:us-east-1:lambda:path/2015-03-31/functions/arn:aws:lambda:us-east-1:000000000000:function:catch-all/invocations",
		})
	var integ V2Integration
	json.NewDecoder(w2.Body).Decode(&integ)

	do(svc, http.MethodPost, fmt.Sprintf("/apis/%s/routes", api.ApiId),
		map[string]string{
			"routeKey": "$default",
			"target":   "integrations/" + integ.IntegrationId,
		})
	do(svc, http.MethodPost, fmt.Sprintf("/apis/%s/stages", api.ApiId),
		map[string]any{"stageName": "live"})

	// Any path should match $default
	execPath := fmt.Sprintf("/apis/%s/live/_user_request_/anything/nested", api.ApiId)
	req := httptest.NewRequest(http.MethodPost, execPath, strings.NewReader(`{}`))
	w3 := httptest.NewRecorder()
	svc.ServeHTTP(w3, req)

	if w3.Code != http.StatusOK {
		t.Fatalf("$default route: want 200, got %d — %s", w3.Code, w3.Body)
	}
	if ml.calls[0].FunctionName != "catch-all" {
		t.Fatalf("want function 'catch-all', got %q", ml.calls[0].FunctionName)
	}
}

func TestV2PayloadFormatV1(t *testing.T) {
	svc, ml := newTestService()
	ml.responses["fn"] = []byte(`{"statusCode":200,"body":"v1-event"}`)

	w := do(svc, http.MethodPost, "/apis", map[string]string{"name": "x"})
	var api HTTPApi
	json.NewDecoder(w.Body).Decode(&api)

	// Integration with payloadFormatVersion 1.0
	w2 := do(svc, http.MethodPost, fmt.Sprintf("/apis/%s/integrations", api.ApiId),
		map[string]string{
			"integrationType":      "AWS_PROXY",
			"integrationUri":       "arn:aws:apigateway:us-east-1:lambda:path/2015-03-31/functions/arn:aws:lambda:us-east-1:000000000000:function:fn/invocations",
			"payloadFormatVersion": "1.0",
		})
	var integ V2Integration
	json.NewDecoder(w2.Body).Decode(&integ)

	do(svc, http.MethodPost, fmt.Sprintf("/apis/%s/routes", api.ApiId),
		map[string]string{"routeKey": "GET /test", "target": "integrations/" + integ.IntegrationId})
	do(svc, http.MethodPost, fmt.Sprintf("/apis/%s/stages", api.ApiId),
		map[string]any{"stageName": "s"})

	execPath := fmt.Sprintf("/apis/%s/s/_user_request_/test", api.ApiId)
	req := httptest.NewRequest(http.MethodGet, execPath, nil)
	w3 := httptest.NewRecorder()
	svc.ServeHTTP(w3, req)

	if w3.Code != http.StatusOK {
		t.Fatalf("v1 format: want 200, got %d — %s", w3.Code, w3.Body)
	}

	// Verify it got a v1 event (has httpMethod, not requestContext.http)
	var event lambdaProxyEvent
	json.Unmarshal(ml.calls[0].Payload, &event)
	if event.HttpMethod != "GET" {
		t.Fatalf("want v1 event with httpMethod GET, got %q", event.HttpMethod)
	}
	if event.Version != "1.0" {
		t.Fatalf("want version 1.0, got %q", event.Version)
	}
}

func TestUpdateHTTPApi(t *testing.T) {
	svc, _ := newTestService()
	w := do(svc, http.MethodPost, "/apis", map[string]string{"name": "original"})
	var api HTTPApi
	json.NewDecoder(w.Body).Decode(&api)

	w2 := do(svc, http.MethodPatch, "/apis/"+api.ApiId, map[string]string{"name": "renamed"})
	if w2.Code != http.StatusOK {
		t.Fatalf("UpdateApi: want 200, got %d — %s", w2.Code, w2.Body)
	}
	var updated HTTPApi
	json.NewDecoder(w2.Body).Decode(&updated)
	if updated.Name != "renamed" {
		t.Fatalf("want name 'renamed', got %q", updated.Name)
	}
}

func TestUpdateRoute(t *testing.T) {
	svc, _ := newTestService()
	apiID := setupV2API(t, svc)

	w := do(svc, http.MethodPost, fmt.Sprintf("/apis/%s/routes", apiID),
		map[string]string{"routeKey": "GET /old"})
	var route V2Route
	json.NewDecoder(w.Body).Decode(&route)

	w2 := do(svc, http.MethodPatch, fmt.Sprintf("/apis/%s/routes/%s", apiID, route.RouteId),
		map[string]string{"routeKey": "GET /new"})
	if w2.Code != http.StatusOK {
		t.Fatalf("UpdateRoute: want 200, got %d — %s", w2.Code, w2.Body)
	}
	var updated V2Route
	json.NewDecoder(w2.Body).Decode(&updated)
	if updated.RouteKey != "GET /new" {
		t.Fatalf("want routeKey 'GET /new', got %q", updated.RouteKey)
	}
}

func TestUpdateIntegration(t *testing.T) {
	svc, _ := newTestService()
	apiID := setupV2API(t, svc)

	w := do(svc, http.MethodPost, fmt.Sprintf("/apis/%s/integrations", apiID),
		map[string]string{"integrationType": "AWS_PROXY", "integrationUri": "arn:aws:apigateway:us-east-1:lambda:path/2015-03-31/functions/arn:aws:lambda:us-east-1:000000000000:function:fn/invocations"})
	var integ V2Integration
	json.NewDecoder(w.Body).Decode(&integ)

	w2 := do(svc, http.MethodPatch, fmt.Sprintf("/apis/%s/integrations/%s", apiID, integ.IntegrationId),
		map[string]string{"payloadFormatVersion": "1.0"})
	if w2.Code != http.StatusOK {
		t.Fatalf("UpdateIntegration: want 200, got %d — %s", w2.Code, w2.Body)
	}
	var updated V2Integration
	json.NewDecoder(w2.Body).Decode(&updated)
	if updated.PayloadFormatVersion != "1.0" {
		t.Fatalf("want payloadFormatVersion '1.0', got %q", updated.PayloadFormatVersion)
	}
}

func TestV2StageLifecycle(t *testing.T) {
	svc, _ := newTestService()
	apiID := setupV2API(t, svc)

	do(svc, http.MethodPost, fmt.Sprintf("/apis/%s/stages", apiID),
		map[string]any{"stageName": "dev"})
	do(svc, http.MethodPost, fmt.Sprintf("/apis/%s/stages", apiID),
		map[string]any{"stageName": "prod"})

	// GetStages
	w := do(svc, http.MethodGet, fmt.Sprintf("/apis/%s/stages", apiID), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GetStages: want 200, got %d", w.Code)
	}
	var list map[string]any
	json.NewDecoder(w.Body).Decode(&list)
	if len(list["items"].([]any)) != 2 {
		t.Fatalf("want 2 stages, got %d", len(list["items"].([]any)))
	}

	// UpdateStage
	w2 := do(svc, http.MethodPatch, fmt.Sprintf("/apis/%s/stages/dev", apiID),
		map[string]any{"autoDeploy": false, "description": "dev stage"})
	if w2.Code != http.StatusOK {
		t.Fatalf("UpdateStage: want 200, got %d — %s", w2.Code, w2.Body)
	}

	// DeleteStage
	w3 := do(svc, http.MethodDelete, fmt.Sprintf("/apis/%s/stages/prod", apiID), nil)
	if w3.Code != http.StatusNoContent {
		t.Fatalf("DeleteStage: want 204, got %d", w3.Code)
	}

	w4 := do(svc, http.MethodGet, fmt.Sprintf("/apis/%s/stages/prod", apiID), nil)
	if w4.Code != http.StatusNotFound {
		t.Fatalf("GetStage after delete: want 404, got %d", w4.Code)
	}
}

func TestV2DeploymentLifecycle(t *testing.T) {
	svc, _ := newTestService()
	apiID := setupV2API(t, svc)
	do(svc, http.MethodPost, fmt.Sprintf("/apis/%s/stages", apiID),
		map[string]any{"stageName": "prod"})

	// CreateDeployment
	w := do(svc, http.MethodPost, fmt.Sprintf("/apis/%s/deployments", apiID),
		map[string]string{"description": "first", "stageName": "prod"})
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateDeployment: want 201, got %d — %s", w.Code, w.Body)
	}
	var dep V2Deployment
	json.NewDecoder(w.Body).Decode(&dep)

	// GetDeployments
	w2 := do(svc, http.MethodGet, fmt.Sprintf("/apis/%s/deployments", apiID), nil)
	if w2.Code != http.StatusOK {
		t.Fatalf("GetDeployments: want 200, got %d", w2.Code)
	}
	var list map[string]any
	json.NewDecoder(w2.Body).Decode(&list)
	if len(list["items"].([]any)) != 1 {
		t.Fatalf("want 1 deployment, got %d", len(list["items"].([]any)))
	}

	// GetDeployment
	w3 := do(svc, http.MethodGet, fmt.Sprintf("/apis/%s/deployments/%s", apiID, dep.DeploymentId), nil)
	if w3.Code != http.StatusOK {
		t.Fatalf("GetDeployment: want 200, got %d", w3.Code)
	}
	var fetched V2Deployment
	json.NewDecoder(w3.Body).Decode(&fetched)
	if fetched.DeploymentStatus != "DEPLOYED" {
		t.Fatalf("want status DEPLOYED, got %q", fetched.DeploymentStatus)
	}

	// DeleteDeployment
	w4 := do(svc, http.MethodDelete, fmt.Sprintf("/apis/%s/deployments/%s", apiID, dep.DeploymentId), nil)
	if w4.Code != http.StatusNoContent {
		t.Fatalf("DeleteDeployment: want 204, got %d", w4.Code)
	}

	w5 := do(svc, http.MethodGet, fmt.Sprintf("/apis/%s/deployments/%s", apiID, dep.DeploymentId), nil)
	if w5.Code != http.StatusNotFound {
		t.Fatalf("GetDeployment after delete: want 404, got %d", w5.Code)
	}
}

func TestCreateWebSocketApi(t *testing.T) {
	svc, _ := newTestService()

	w := do(svc, http.MethodPost, "/apis", map[string]string{
		"name":                     "my-ws-api",
		"protocolType":             "WEBSOCKET",
		"routeSelectionExpression": "$request.body.action",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateApi (WEBSOCKET): want 201, got %d — %s", w.Code, w.Body)
	}

	var api HTTPApi
	json.NewDecoder(w.Body).Decode(&api)
	if api.ApiId == "" {
		t.Fatal("expected non-empty apiId")
	}
	if api.ProtocolType != "WEBSOCKET" {
		t.Fatalf("want protocolType WEBSOCKET, got %q", api.ProtocolType)
	}
	if api.RouteSelectionExpression != "$request.body.action" {
		t.Fatalf("want routeSelectionExpression '$request.body.action', got %q", api.RouteSelectionExpression)
	}
	if !strings.HasPrefix(api.ApiEndpoint, "ws://") {
		t.Fatalf("want ws:// endpoint for WEBSOCKET API, got %q", api.ApiEndpoint)
	}

	// GetApi round-trips the fields correctly
	w2 := do(svc, http.MethodGet, "/apis/"+api.ApiId, nil)
	if w2.Code != http.StatusOK {
		t.Fatalf("GetApi (WEBSOCKET): want 200, got %d", w2.Code)
	}
	var fetched HTTPApi
	json.NewDecoder(w2.Body).Decode(&fetched)
	if fetched.ProtocolType != "WEBSOCKET" {
		t.Fatalf("GetApi: want protocolType WEBSOCKET, got %q", fetched.ProtocolType)
	}
	if fetched.RouteSelectionExpression != "$request.body.action" {
		t.Fatalf("GetApi: want routeSelectionExpression round-tripped, got %q", fetched.RouteSelectionExpression)
	}
}

func TestWebSocketApiWsRoutes(t *testing.T) {
	svc, _ := newTestService()

	w := do(svc, http.MethodPost, "/apis", map[string]string{
		"name":                     "ws",
		"protocolType":             "WEBSOCKET",
		"routeSelectionExpression": "$request.body.action",
	})
	var api HTTPApi
	json.NewDecoder(w.Body).Decode(&api)

	for _, routeKey := range []string{"$connect", "$disconnect", "$default"} {
		wr := do(svc, http.MethodPost, fmt.Sprintf("/apis/%s/routes", api.ApiId),
			map[string]string{"routeKey": routeKey})
		if wr.Code != http.StatusCreated {
			t.Fatalf("CreateRoute %q: want 201, got %d — %s", routeKey, wr.Code, wr.Body)
		}
		var route V2Route
		json.NewDecoder(wr.Body).Decode(&route)
		if route.RouteKey != routeKey {
			t.Fatalf("want routeKey %q, got %q", routeKey, route.RouteKey)
		}
	}

	// All three routes present
	wl := do(svc, http.MethodGet, fmt.Sprintf("/apis/%s/routes", api.ApiId), nil)
	var list map[string]any
	json.NewDecoder(wl.Body).Decode(&list)
	if len(list["items"].([]any)) != 3 {
		t.Fatalf("want 3 WebSocket routes, got %d", len(list["items"].([]any)))
	}
}

func TestHttpApiEndpointScheme(t *testing.T) {
	svc, _ := newTestService()

	wHttp := do(svc, http.MethodPost, "/apis", map[string]string{
		"name":         "http-api",
		"protocolType": "HTTP",
	})
	var httpAPI HTTPApi
	json.NewDecoder(wHttp.Body).Decode(&httpAPI)
	if !strings.HasPrefix(httpAPI.ApiEndpoint, "http://") {
		t.Fatalf("HTTP API: want http:// endpoint, got %q", httpAPI.ApiEndpoint)
	}

	wWs := do(svc, http.MethodPost, "/apis", map[string]string{
		"name":         "ws-api",
		"protocolType": "WEBSOCKET",
	})
	var wsAPI HTTPApi
	json.NewDecoder(wWs.Body).Decode(&wsAPI)
	if !strings.HasPrefix(wsAPI.ApiEndpoint, "ws://") {
		t.Fatalf("WEBSOCKET API: want ws:// endpoint, got %q", wsAPI.ApiEndpoint)
	}
}

// setupV2API creates an HTTP API and returns its ID.
func setupV2API(t *testing.T, svc *Service) string {
	t.Helper()
	w := do(svc, http.MethodPost, "/apis", map[string]string{"name": "x"})
	var api HTTPApi
	json.NewDecoder(w.Body).Decode(&api)
	return api.ApiId
}

func TestV2AndV1Coexist(t *testing.T) {
	svc, _ := newTestService()

	// Create a REST API (v1)
	w1 := do(svc, http.MethodPost, "/restapis", map[string]string{"name": "rest-api"})
	if w1.Code != http.StatusCreated {
		t.Fatalf("CreateRestApi: want 201, got %d", w1.Code)
	}

	// Create an HTTP API (v2)
	w2 := do(svc, http.MethodPost, "/apis", map[string]string{"name": "http-api"})
	if w2.Code != http.StatusCreated {
		t.Fatalf("CreateApi: want 201, got %d", w2.Code)
	}

	// Both should be independently listed
	restList := do(svc, http.MethodGet, "/restapis", nil)
	var restAPIs map[string]any
	json.NewDecoder(restList.Body).Decode(&restAPIs)
	if len(restAPIs["item"].([]any)) != 1 {
		t.Fatalf("want 1 REST API, got %d", len(restAPIs["item"].([]any)))
	}

	httpList := do(svc, http.MethodGet, "/apis", nil)
	var httpAPIs map[string]any
	json.NewDecoder(httpList.Body).Decode(&httpAPIs)
	if len(httpAPIs["items"].([]any)) != 1 {
		t.Fatalf("want 1 HTTP API, got %d", len(httpAPIs["items"].([]any)))
	}
}
