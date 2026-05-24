package apigateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockLambda satisfies LambdaInvoker for tests.
type mockLambda struct {
	responses map[string][]byte
	calls     []lambdaCall
}

type lambdaCall struct {
	FunctionName string
	Payload      []byte
}

func (m *mockLambda) DirectInvoke(functionName string, payload []byte) ([]byte, error) {
	m.calls = append(m.calls, lambdaCall{FunctionName: functionName, Payload: payload})
	if resp, ok := m.responses[functionName]; ok {
		return resp, nil
	}
	return []byte(`{"statusCode":200,"body":"ok"}`), nil
}

func newTestService() (*Service, *mockLambda) {
	ml := &mockLambda{responses: map[string][]byte{}}
	svc := New("us-east-1", ml)
	return svc, ml
}

func do(svc *Service, method, path string, body any) *httptest.ResponseRecorder {
	var buf *bytes.Buffer
	if body != nil {
		b, _ := json.Marshal(body)
		buf = bytes.NewBuffer(b)
	} else {
		buf = &bytes.Buffer{}
	}
	req := httptest.NewRequest(method, path, buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	return w
}

func TestDetect(t *testing.T) {
	svc, _ := newTestService()
	r := httptest.NewRequest(http.MethodGet, "/restapis", nil)
	if !svc.Detect(r) {
		t.Fatal("Detect should match /restapis")
	}
	r2 := httptest.NewRequest(http.MethodGet, "/2015-03-31/functions", nil)
	if svc.Detect(r2) {
		t.Fatal("Detect should not match lambda path")
	}
}

func TestCreateAndGetRestAPI(t *testing.T) {
	svc, _ := newTestService()

	w := do(svc, http.MethodPost, "/restapis", map[string]string{
		"name":        "my-api",
		"description": "test api",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateRestApi: want 201, got %d — %s", w.Code, w.Body)
	}

	var api RestAPI
	json.NewDecoder(w.Body).Decode(&api)
	if api.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if api.Name != "my-api" {
		t.Fatalf("want name 'my-api', got %q", api.Name)
	}

	// GetRestApi
	w2 := do(svc, http.MethodGet, "/restapis/"+api.ID, nil)
	if w2.Code != http.StatusOK {
		t.Fatalf("GetRestApi: want 200, got %d", w2.Code)
	}

	// GetRestApis
	w3 := do(svc, http.MethodGet, "/restapis", nil)
	if w3.Code != http.StatusOK {
		t.Fatalf("GetRestApis: want 200, got %d", w3.Code)
	}
	var list map[string]any
	json.NewDecoder(w3.Body).Decode(&list)
	items := list["item"].([]any)
	if len(items) != 1 {
		t.Fatalf("want 1 API, got %d", len(items))
	}
}

func TestDeleteRestAPI(t *testing.T) {
	svc, _ := newTestService()
	w := do(svc, http.MethodPost, "/restapis", map[string]string{"name": "x"})
	var api RestAPI
	json.NewDecoder(w.Body).Decode(&api)

	w2 := do(svc, http.MethodDelete, "/restapis/"+api.ID, nil)
	if w2.Code != http.StatusAccepted {
		t.Fatalf("DeleteRestApi: want 202, got %d", w2.Code)
	}

	w3 := do(svc, http.MethodGet, "/restapis/"+api.ID, nil)
	if w3.Code != http.StatusNotFound {
		t.Fatalf("GetRestApi after delete: want 404, got %d", w3.Code)
	}
}

func TestResourceLifecycle(t *testing.T) {
	svc, _ := newTestService()

	// Create API
	w := do(svc, http.MethodPost, "/restapis", map[string]string{"name": "x"})
	var api RestAPI
	json.NewDecoder(w.Body).Decode(&api)

	// Get resources (should have root /)
	w2 := do(svc, http.MethodGet, "/restapis/"+api.ID+"/resources", nil)
	if w2.Code != http.StatusOK {
		t.Fatalf("GetResources: want 200, got %d", w2.Code)
	}
	var res map[string]any
	json.NewDecoder(w2.Body).Decode(&res)
	items := res["item"].([]any)
	if len(items) != 1 {
		t.Fatalf("want 1 root resource, got %d", len(items))
	}
	rootID := items[0].(map[string]any)["id"].(string)

	// Create child resource
	w3 := do(svc, http.MethodPost, fmt.Sprintf("/restapis/%s/resources/%s", api.ID, rootID),
		map[string]string{"pathPart": "users"})
	if w3.Code != http.StatusCreated {
		t.Fatalf("CreateResource: want 201, got %d — %s", w3.Code, w3.Body)
	}
	var child Resource
	json.NewDecoder(w3.Body).Decode(&child)
	if child.Path != "/users" {
		t.Fatalf("want path '/users', got %q", child.Path)
	}

	// GetResource
	w4 := do(svc, http.MethodGet, fmt.Sprintf("/restapis/%s/resources/%s", api.ID, child.ID), nil)
	if w4.Code != http.StatusOK {
		t.Fatalf("GetResource: want 200, got %d", w4.Code)
	}

	// Delete it
	w5 := do(svc, http.MethodDelete, fmt.Sprintf("/restapis/%s/resources/%s", api.ID, child.ID), nil)
	if w5.Code != http.StatusNoContent {
		t.Fatalf("DeleteResource: want 204, got %d", w5.Code)
	}
}

func TestMethodAndIntegration(t *testing.T) {
	svc, _ := newTestService()

	// Setup: API + resource
	w := do(svc, http.MethodPost, "/restapis", map[string]string{"name": "x"})
	var api RestAPI
	json.NewDecoder(w.Body).Decode(&api)

	w2 := do(svc, http.MethodGet, "/restapis/"+api.ID+"/resources", nil)
	var resList map[string]any
	json.NewDecoder(w2.Body).Decode(&resList)
	rootID := resList["item"].([]any)[0].(map[string]any)["id"].(string)

	w3 := do(svc, http.MethodPost, fmt.Sprintf("/restapis/%s/resources/%s", api.ID, rootID),
		map[string]string{"pathPart": "hello"})
	var res Resource
	json.NewDecoder(w3.Body).Decode(&res)

	base := fmt.Sprintf("/restapis/%s/resources/%s/methods/GET", api.ID, res.ID)

	// PutMethod
	w4 := do(svc, http.MethodPut, base, map[string]string{"authorizationType": "NONE"})
	if w4.Code != http.StatusCreated {
		t.Fatalf("PutMethod: want 201, got %d — %s", w4.Code, w4.Body)
	}

	// GetMethod
	w5 := do(svc, http.MethodGet, base, nil)
	if w5.Code != http.StatusOK {
		t.Fatalf("GetMethod: want 200, got %d", w5.Code)
	}

	// PutIntegration (MOCK)
	w6 := do(svc, http.MethodPut, base+"/integration",
		map[string]string{"type": "MOCK"})
	if w6.Code != http.StatusCreated {
		t.Fatalf("PutIntegration: want 201, got %d — %s", w6.Code, w6.Body)
	}

	// GetIntegration
	w7 := do(svc, http.MethodGet, base+"/integration", nil)
	if w7.Code != http.StatusOK {
		t.Fatalf("GetIntegration: want 200, got %d", w7.Code)
	}

	// PutMethodResponse
	w8 := do(svc, http.MethodPut, base+"/responses/200", nil)
	if w8.Code != http.StatusCreated {
		t.Fatalf("PutMethodResponse: want 201, got %d — %s", w8.Code, w8.Body)
	}
}

func TestDeploymentAndStage(t *testing.T) {
	svc, _ := newTestService()

	w := do(svc, http.MethodPost, "/restapis", map[string]string{"name": "x"})
	var api RestAPI
	json.NewDecoder(w.Body).Decode(&api)

	// CreateDeployment with stageName → auto-creates stage
	w2 := do(svc, http.MethodPost, fmt.Sprintf("/restapis/%s/deployments", api.ID),
		map[string]string{"stageName": "dev"})
	if w2.Code != http.StatusCreated {
		t.Fatalf("CreateDeployment: want 201, got %d — %s", w2.Code, w2.Body)
	}
	var dep Deployment
	json.NewDecoder(w2.Body).Decode(&dep)
	if dep.ID == "" {
		t.Fatal("expected deployment ID")
	}

	// GetDeployment
	w3 := do(svc, http.MethodGet, fmt.Sprintf("/restapis/%s/deployments/%s", api.ID, dep.ID), nil)
	if w3.Code != http.StatusOK {
		t.Fatalf("GetDeployment: want 200, got %d", w3.Code)
	}

	// Stage was auto-created
	w4 := do(svc, http.MethodGet, fmt.Sprintf("/restapis/%s/stages/dev", api.ID), nil)
	if w4.Code != http.StatusOK {
		t.Fatalf("GetStage (auto-created): want 200, got %d — %s", w4.Code, w4.Body)
	}
}

func TestUpdateRestAPI(t *testing.T) {
	svc, _ := newTestService()
	w := do(svc, http.MethodPost, "/restapis", map[string]string{"name": "original"})
	var api RestAPI
	json.NewDecoder(w.Body).Decode(&api)

	w2 := do(svc, http.MethodPatch, "/restapis/"+api.ID, map[string]string{"name": "updated"})
	if w2.Code != http.StatusOK {
		t.Fatalf("UpdateRestApi: want 200, got %d — %s", w2.Code, w2.Body)
	}
	var updated RestAPI
	json.NewDecoder(w2.Body).Decode(&updated)
	if updated.Name != "updated" {
		t.Fatalf("want name 'updated', got %q", updated.Name)
	}
}

func TestDeleteMethod(t *testing.T) {
	svc, _ := newTestService()
	apiID, rootID := setupAPI(t, svc)
	resID := setupResource(t, svc, apiID, rootID, "things")
	base := fmt.Sprintf("/restapis/%s/resources/%s/methods/GET", apiID, resID)

	do(svc, http.MethodPut, base, map[string]string{"authorizationType": "NONE"})

	w := do(svc, http.MethodDelete, base, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("DeleteMethod: want 204, got %d — %s", w.Code, w.Body)
	}

	w2 := do(svc, http.MethodGet, base, nil)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("GetMethod after delete: want 404, got %d", w2.Code)
	}
}

func TestDeleteIntegration(t *testing.T) {
	svc, _ := newTestService()
	apiID, rootID := setupAPI(t, svc)
	resID := setupResource(t, svc, apiID, rootID, "things")
	base := fmt.Sprintf("/restapis/%s/resources/%s/methods/GET", apiID, resID)

	do(svc, http.MethodPut, base, map[string]string{"authorizationType": "NONE"})
	do(svc, http.MethodPut, base+"/integration", map[string]string{"type": "MOCK"})

	w := do(svc, http.MethodDelete, base+"/integration", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("DeleteIntegration: want 204, got %d — %s", w.Code, w.Body)
	}

	w2 := do(svc, http.MethodGet, base+"/integration", nil)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("GetIntegration after delete: want 404, got %d", w2.Code)
	}
}

func TestMethodResponseLifecycle(t *testing.T) {
	svc, _ := newTestService()
	apiID, rootID := setupAPI(t, svc)
	resID := setupResource(t, svc, apiID, rootID, "things")
	base := fmt.Sprintf("/restapis/%s/resources/%s/methods/GET", apiID, resID)

	do(svc, http.MethodPut, base, map[string]string{"authorizationType": "NONE"})
	do(svc, http.MethodPut, base+"/responses/200", nil)

	// GetMethodResponse
	w := do(svc, http.MethodGet, base+"/responses/200", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GetMethodResponse: want 200, got %d — %s", w.Code, w.Body)
	}
	var mr MethodResponse
	json.NewDecoder(w.Body).Decode(&mr)
	if mr.StatusCode != "200" {
		t.Fatalf("want statusCode '200', got %q", mr.StatusCode)
	}

	// DeleteMethodResponse
	w2 := do(svc, http.MethodDelete, base+"/responses/200", nil)
	if w2.Code != http.StatusNoContent {
		t.Fatalf("DeleteMethodResponse: want 204, got %d — %s", w2.Code, w2.Body)
	}

	w3 := do(svc, http.MethodGet, base+"/responses/200", nil)
	if w3.Code != http.StatusNotFound {
		t.Fatalf("GetMethodResponse after delete: want 404, got %d", w3.Code)
	}
}

func TestIntegrationResponseLifecycle(t *testing.T) {
	svc, _ := newTestService()
	apiID, rootID := setupAPI(t, svc)
	resID := setupResource(t, svc, apiID, rootID, "things")
	base := fmt.Sprintf("/restapis/%s/resources/%s/methods/GET", apiID, resID)

	do(svc, http.MethodPut, base, map[string]string{"authorizationType": "NONE"})
	do(svc, http.MethodPut, base+"/integration", map[string]string{"type": "MOCK"})

	// PutIntegrationResponse
	w := do(svc, http.MethodPut, base+"/integration/responses/200",
		map[string]any{"statusCode": "200", "responseTemplates": map[string]string{"application/json": "{}"}})
	if w.Code != http.StatusCreated {
		t.Fatalf("PutIntegrationResponse: want 201, got %d — %s", w.Code, w.Body)
	}

	// GetIntegrationResponse
	w2 := do(svc, http.MethodGet, base+"/integration/responses/200", nil)
	if w2.Code != http.StatusOK {
		t.Fatalf("GetIntegrationResponse: want 200, got %d — %s", w2.Code, w2.Body)
	}
	var ir IntegrationResponse
	json.NewDecoder(w2.Body).Decode(&ir)
	if ir.StatusCode != "200" {
		t.Fatalf("want statusCode '200', got %q", ir.StatusCode)
	}

	// DeleteIntegrationResponse
	w3 := do(svc, http.MethodDelete, base+"/integration/responses/200", nil)
	if w3.Code != http.StatusNoContent {
		t.Fatalf("DeleteIntegrationResponse: want 204, got %d — %s", w3.Code, w3.Body)
	}

	w4 := do(svc, http.MethodGet, base+"/integration/responses/200", nil)
	if w4.Code != http.StatusNotFound {
		t.Fatalf("GetIntegrationResponse after delete: want 404, got %d", w4.Code)
	}
}

func TestDeploymentLifecycle(t *testing.T) {
	svc, _ := newTestService()
	w := do(svc, http.MethodPost, "/restapis", map[string]string{"name": "x"})
	var api RestAPI
	json.NewDecoder(w.Body).Decode(&api)

	// CreateDeployment
	w2 := do(svc, http.MethodPost, fmt.Sprintf("/restapis/%s/deployments", api.ID),
		map[string]string{"description": "v1"})
	var dep Deployment
	json.NewDecoder(w2.Body).Decode(&dep)

	// GetDeployments
	w3 := do(svc, http.MethodGet, fmt.Sprintf("/restapis/%s/deployments", api.ID), nil)
	if w3.Code != http.StatusOK {
		t.Fatalf("GetDeployments: want 200, got %d", w3.Code)
	}
	var list map[string]any
	json.NewDecoder(w3.Body).Decode(&list)
	if len(list["item"].([]any)) != 1 {
		t.Fatalf("want 1 deployment, got %d", len(list["item"].([]any)))
	}

	// GetDeployment
	w4 := do(svc, http.MethodGet, fmt.Sprintf("/restapis/%s/deployments/%s", api.ID, dep.ID), nil)
	if w4.Code != http.StatusOK {
		t.Fatalf("GetDeployment: want 200, got %d", w4.Code)
	}

	// DeleteDeployment
	w5 := do(svc, http.MethodDelete, fmt.Sprintf("/restapis/%s/deployments/%s", api.ID, dep.ID), nil)
	if w5.Code != http.StatusAccepted {
		t.Fatalf("DeleteDeployment: want 202, got %d", w5.Code)
	}

	w6 := do(svc, http.MethodGet, fmt.Sprintf("/restapis/%s/deployments/%s", api.ID, dep.ID), nil)
	if w6.Code != http.StatusNotFound {
		t.Fatalf("GetDeployment after delete: want 404, got %d", w6.Code)
	}
}

func TestStageLifecycle(t *testing.T) {
	svc, _ := newTestService()
	w := do(svc, http.MethodPost, "/restapis", map[string]string{"name": "x"})
	var api RestAPI
	json.NewDecoder(w.Body).Decode(&api)

	// CreateDeployment first (stage needs one)
	w2 := do(svc, http.MethodPost, fmt.Sprintf("/restapis/%s/deployments", api.ID),
		map[string]string{})
	var dep Deployment
	json.NewDecoder(w2.Body).Decode(&dep)

	// CreateStage explicitly
	w3 := do(svc, http.MethodPost, fmt.Sprintf("/restapis/%s/stages", api.ID),
		map[string]any{"stageName": "staging", "deploymentId": dep.ID})
	if w3.Code != http.StatusCreated {
		t.Fatalf("CreateStage: want 201, got %d — %s", w3.Code, w3.Body)
	}

	// GetStages
	w4 := do(svc, http.MethodGet, fmt.Sprintf("/restapis/%s/stages", api.ID), nil)
	if w4.Code != http.StatusOK {
		t.Fatalf("GetStages: want 200, got %d", w4.Code)
	}
	var stageList map[string]any
	json.NewDecoder(w4.Body).Decode(&stageList)
	if len(stageList["item"].([]any)) != 1 {
		t.Fatalf("want 1 stage, got %d", len(stageList["item"].([]any)))
	}

	// GetStage
	w5 := do(svc, http.MethodGet, fmt.Sprintf("/restapis/%s/stages/staging", api.ID), nil)
	if w5.Code != http.StatusOK {
		t.Fatalf("GetStage: want 200, got %d", w5.Code)
	}
	var stage Stage
	json.NewDecoder(w5.Body).Decode(&stage)
	if stage.StageName != "staging" {
		t.Fatalf("want stageName 'staging', got %q", stage.StageName)
	}

	// UpdateStage
	w6 := do(svc, http.MethodPatch, fmt.Sprintf("/restapis/%s/stages/staging", api.ID),
		map[string]string{"description": "updated"})
	if w6.Code != http.StatusOK {
		t.Fatalf("UpdateStage: want 200, got %d — %s", w6.Code, w6.Body)
	}

	// DeleteStage
	w7 := do(svc, http.MethodDelete, fmt.Sprintf("/restapis/%s/stages/staging", api.ID), nil)
	if w7.Code != http.StatusAccepted {
		t.Fatalf("DeleteStage: want 202, got %d", w7.Code)
	}

	w8 := do(svc, http.MethodGet, fmt.Sprintf("/restapis/%s/stages/staging", api.ID), nil)
	if w8.Code != http.StatusNotFound {
		t.Fatalf("GetStage after delete: want 404, got %d", w8.Code)
	}
}

// setupAPI creates a REST API and returns its ID and root resource ID.
func setupAPI(t *testing.T, svc *Service) (apiID, rootID string) {
	t.Helper()
	w := do(svc, http.MethodPost, "/restapis", map[string]string{"name": "x"})
	var api RestAPI
	json.NewDecoder(w.Body).Decode(&api)

	w2 := do(svc, http.MethodGet, "/restapis/"+api.ID+"/resources", nil)
	var res map[string]any
	json.NewDecoder(w2.Body).Decode(&res)
	return api.ID, res["item"].([]any)[0].(map[string]any)["id"].(string)
}

// setupResource creates a child resource under parentID and returns its ID.
func setupResource(t *testing.T, svc *Service, apiID, parentID, pathPart string) string {
	t.Helper()
	w := do(svc, http.MethodPost, fmt.Sprintf("/restapis/%s/resources/%s", apiID, parentID),
		map[string]string{"pathPart": pathPart})
	var res Resource
	json.NewDecoder(w.Body).Decode(&res)
	return res.ID
}

func TestMockExecution(t *testing.T) {
	svc, _ := newTestService()

	// Create API
	w := do(svc, http.MethodPost, "/restapis", map[string]string{"name": "x"})
	var api RestAPI
	json.NewDecoder(w.Body).Decode(&api)

	// Get root resource
	w2 := do(svc, http.MethodGet, "/restapis/"+api.ID+"/resources", nil)
	var resList map[string]any
	json.NewDecoder(w2.Body).Decode(&resList)
	rootID := resList["item"].([]any)[0].(map[string]any)["id"].(string)

	// Create /ping resource
	w3 := do(svc, http.MethodPost, fmt.Sprintf("/restapis/%s/resources/%s", api.ID, rootID),
		map[string]string{"pathPart": "ping"})
	var pingRes Resource
	json.NewDecoder(w3.Body).Decode(&pingRes)

	base := fmt.Sprintf("/restapis/%s/resources/%s/methods/GET", api.ID, pingRes.ID)

	// PutMethod + MOCK integration
	do(svc, http.MethodPut, base, map[string]string{"authorizationType": "NONE"})
	do(svc, http.MethodPut, base+"/integration",
		map[string]any{
			"type": "MOCK",
			"integrationResponses": map[string]any{
				"200": map[string]any{
					"statusCode": "200",
					"responseTemplates": map[string]string{
						"application/json": `{"pong":true}`,
					},
				},
			},
		})
	do(svc, http.MethodPut, base+"/integration/responses/200",
		map[string]any{
			"statusCode": "200",
			"responseTemplates": map[string]string{
				"application/json": `{"pong":true}`,
			},
		})

	// Deploy + stage
	do(svc, http.MethodPost, fmt.Sprintf("/restapis/%s/deployments", api.ID),
		map[string]string{"stageName": "dev"})

	// Execute
	execPath := fmt.Sprintf("/restapis/%s/dev/_user_request_/ping", api.ID)
	req := httptest.NewRequest(http.MethodGet, execPath, nil)
	w4 := httptest.NewRecorder()
	svc.ServeHTTP(w4, req)
	if w4.Code != http.StatusOK {
		t.Fatalf("MOCK execution: want 200, got %d — %s", w4.Code, w4.Body)
	}
}

func TestLambdaProxyExecution(t *testing.T) {
	svc, ml := newTestService()

	// Configure Lambda mock to return a proxy response
	ml.responses["my-func"] = []byte(`{"statusCode":201,"headers":{"X-Custom":"yes"},"body":"created"}`)

	// Create API + resource + method + integration
	w := do(svc, http.MethodPost, "/restapis", map[string]string{"name": "x"})
	var api RestAPI
	json.NewDecoder(w.Body).Decode(&api)

	w2 := do(svc, http.MethodGet, "/restapis/"+api.ID+"/resources", nil)
	var resList map[string]any
	json.NewDecoder(w2.Body).Decode(&resList)
	rootID := resList["item"].([]any)[0].(map[string]any)["id"].(string)

	w3 := do(svc, http.MethodPost, fmt.Sprintf("/restapis/%s/resources/%s", api.ID, rootID),
		map[string]string{"pathPart": "items"})
	var res Resource
	json.NewDecoder(w3.Body).Decode(&res)

	base := fmt.Sprintf("/restapis/%s/resources/%s/methods/POST", api.ID, res.ID)
	do(svc, http.MethodPut, base, map[string]string{"authorizationType": "NONE"})
	do(svc, http.MethodPut, base+"/integration", map[string]string{
		"type":       "AWS_PROXY",
		"httpMethod": "POST",
		"uri":        "arn:aws:apigateway:us-east-1:lambda:path/2015-03-31/functions/arn:aws:lambda:us-east-1:000000000000:function:my-func/invocations",
	})

	do(svc, http.MethodPost, fmt.Sprintf("/restapis/%s/deployments", api.ID),
		map[string]string{"stageName": "prod"})

	// Execute
	execPath := fmt.Sprintf("/restapis/%s/prod/_user_request_/items", api.ID)
	req := httptest.NewRequest(http.MethodPost, execPath,
		strings.NewReader(`{"name":"thing"}`))
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = int64(len(`{"name":"thing"}`))
	w4 := httptest.NewRecorder()
	svc.ServeHTTP(w4, req)

	if w4.Code != 201 {
		t.Fatalf("Lambda proxy execution: want 201, got %d — %s", w4.Code, w4.Body)
	}
	if w4.Header().Get("X-Custom") != "yes" {
		t.Fatalf("want X-Custom: yes, got %q", w4.Header().Get("X-Custom"))
	}
	if w4.Body.String() != "created" {
		t.Fatalf("want body 'created', got %q", w4.Body.String())
	}

	// Verify Lambda was called with proxy event
	if len(ml.calls) != 1 {
		t.Fatalf("want 1 Lambda call, got %d", len(ml.calls))
	}
	if ml.calls[0].FunctionName != "my-func" {
		t.Fatalf("want function 'my-func', got %q", ml.calls[0].FunctionName)
	}
	var event lambdaProxyEvent
	json.Unmarshal(ml.calls[0].Payload, &event)
	if event.HttpMethod != "POST" {
		t.Fatalf("want HttpMethod POST, got %q", event.HttpMethod)
	}
}

func TestPathParameterExecution(t *testing.T) {
	svc, ml := newTestService()
	ml.responses["handler"] = []byte(`{"statusCode":200,"body":"got it"}`)

	// Create API
	w := do(svc, http.MethodPost, "/restapis", map[string]string{"name": "x"})
	var api RestAPI
	json.NewDecoder(w.Body).Decode(&api)

	w2 := do(svc, http.MethodGet, "/restapis/"+api.ID+"/resources", nil)
	var resList map[string]any
	json.NewDecoder(w2.Body).Decode(&resList)
	rootID := resList["item"].([]any)[0].(map[string]any)["id"].(string)

	// /users resource
	w3 := do(svc, http.MethodPost, fmt.Sprintf("/restapis/%s/resources/%s", api.ID, rootID),
		map[string]string{"pathPart": "users"})
	var usersRes Resource
	json.NewDecoder(w3.Body).Decode(&usersRes)

	// /users/{userId} resource
	w4 := do(svc, http.MethodPost, fmt.Sprintf("/restapis/%s/resources/%s", api.ID, usersRes.ID),
		map[string]string{"pathPart": "{userId}"})
	var userRes Resource
	json.NewDecoder(w4.Body).Decode(&userRes)

	base := fmt.Sprintf("/restapis/%s/resources/%s/methods/GET", api.ID, userRes.ID)
	do(svc, http.MethodPut, base, map[string]string{"authorizationType": "NONE"})
	do(svc, http.MethodPut, base+"/integration", map[string]string{
		"type":       "AWS_PROXY",
		"httpMethod": "POST",
		"uri":        "arn:aws:apigateway:us-east-1:lambda:path/2015-03-31/functions/arn:aws:lambda:us-east-1:000000000000:function:handler/invocations",
	})

	do(svc, http.MethodPost, fmt.Sprintf("/restapis/%s/deployments", api.ID),
		map[string]string{"stageName": "v1"})

	// Execute
	execPath := fmt.Sprintf("/restapis/%s/v1/_user_request_/users/42", api.ID)
	req := httptest.NewRequest(http.MethodGet, execPath, nil)
	w5 := httptest.NewRecorder()
	svc.ServeHTTP(w5, req)

	if w5.Code != http.StatusOK {
		t.Fatalf("path param execution: want 200, got %d — %s", w5.Code, w5.Body)
	}

	// Verify path params were passed
	var event lambdaProxyEvent
	json.Unmarshal(ml.calls[0].Payload, &event)
	if event.PathParameters["userId"] != "42" {
		t.Fatalf("want userId=42, got %v", event.PathParameters)
	}
}

// --- Name ---

func TestName(t *testing.T) {
	svc, _ := newTestService()
	if svc.Name() != "apigateway" {
		t.Errorf("expected Name()=apigateway, got %s", svc.Name())
	}
}
