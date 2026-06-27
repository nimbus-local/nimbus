package appsync_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nimbus-local/nimbus/internal/services/appsync"
)

func newSvc() *appsync.Service { return appsync.New("us-east-1") }

func doJSON(t *testing.T, svc *appsync.Service, method, path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	r := httptest.NewRequest(method, path, &buf)
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, r)
	return w
}

func decode(t *testing.T, w *httptest.ResponseRecorder, v interface{}) {
	t.Helper()
	if err := json.NewDecoder(w.Body).Decode(v); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, w.Body.String())
	}
}

// --- GraphQL API CRUD ---

func TestCreateAndGetGraphqlAPI(t *testing.T) {
	svc := newSvc()

	w := doJSON(t, svc, http.MethodPost, "/v1/apis", map[string]interface{}{
		"name":               "MyAPI",
		"authenticationType": "API_KEY",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create: want 200 got %d", w.Code)
	}
	var createResp struct {
		GraphqlApi struct {
			ApiId string            `json:"apiId"`
			Name  string            `json:"name"`
			ARN   string            `json:"arn"`
			Uris  map[string]string `json:"uris"`
		} `json:"graphqlApi"`
	}
	decode(t, w, &createResp)
	apiID := createResp.GraphqlApi.ApiId
	if apiID == "" {
		t.Fatal("expected apiId")
	}
	if createResp.GraphqlApi.Uris["GRAPHQL"] == "" {
		t.Fatal("expected GRAPHQL uri")
	}

	// Get
	w = doJSON(t, svc, http.MethodGet, "/v1/apis/"+apiID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get: want 200 got %d", w.Code)
	}
}

func TestDeleteGraphqlAPI(t *testing.T) {
	svc := newSvc()
	w := doJSON(t, svc, http.MethodPost, "/v1/apis", map[string]string{"name": "MyAPI"})
	var resp struct {
		GraphqlApi struct {
			ApiId string `json:"apiId"`
		} `json:"graphqlApi"`
	}
	decode(t, w, &resp)
	apiID := resp.GraphqlApi.ApiId

	w = doJSON(t, svc, http.MethodDelete, "/v1/apis/"+apiID, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: want 204 got %d", w.Code)
	}

	// Get after delete -> 404
	w = doJSON(t, svc, http.MethodGet, "/v1/apis/"+apiID, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("get after delete: want 404 got %d", w.Code)
	}
}

// --- Schema ---

func TestSchemaCreation(t *testing.T) {
	svc := newSvc()
	w := doJSON(t, svc, http.MethodPost, "/v1/apis", map[string]string{"name": "MyAPI"})
	var resp struct {
		GraphqlApi struct {
			ApiId string `json:"apiId"`
		} `json:"graphqlApi"`
	}
	decode(t, w, &resp)
	apiID := resp.GraphqlApi.ApiId

	w = doJSON(t, svc, http.MethodPost, "/v1/apis/"+apiID+"/schemacreation", map[string]string{
		"definition": "dHlwZSBRdWVyeSB7IGhlbGxvOiBTdHJpbmcgfQ==",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("startSchemaCreation: want 200 got %d", w.Code)
	}

	w = doJSON(t, svc, http.MethodGet, "/v1/apis/"+apiID+"/schemacreation", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("getSchemaCreationStatus: want 200 got %d", w.Code)
	}
	var statusResp struct {
		Status string `json:"status"`
	}
	decode(t, w, &statusResp)
	if statusResp.Status != "SUCCESS" {
		t.Fatalf("schema status: want SUCCESS got %s", statusResp.Status)
	}
}

// --- Data source ---

func createAPI(t *testing.T, svc *appsync.Service) string {
	t.Helper()
	w := doJSON(t, svc, http.MethodPost, "/v1/apis", map[string]string{"name": "MyAPI"})
	var resp struct {
		GraphqlApi struct {
			ApiId string `json:"apiId"`
		} `json:"graphqlApi"`
	}
	decode(t, w, &resp)
	return resp.GraphqlApi.ApiId
}

func TestDataSourceCRUD(t *testing.T) {
	svc := newSvc()
	apiID := createAPI(t, svc)

	// Create
	w := doJSON(t, svc, http.MethodPost, "/v1/apis/"+apiID+"/datasources", map[string]interface{}{
		"name":           "myLambda",
		"type":           "AWS_LAMBDA",
		"serviceRoleArn": "arn:aws:iam::000000000000:role/AppSyncRole",
		"lambdaConfig": map[string]string{
			"lambdaFunctionArn": "arn:aws:lambda:us-east-1:000000000000:function:myFn",
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create datasource: want 200 got %d: %s", w.Code, w.Body)
	}

	// Get
	w = doJSON(t, svc, http.MethodGet, "/v1/apis/"+apiID+"/datasources/myLambda", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get datasource: want 200 got %d", w.Code)
	}
	var dsResp struct {
		DataSource struct {
			Name string `json:"name"`
		} `json:"dataSource"`
	}
	decode(t, w, &dsResp)
	if dsResp.DataSource.Name != "myLambda" {
		t.Fatalf("name mismatch: %s", dsResp.DataSource.Name)
	}

	// Delete
	w = doJSON(t, svc, http.MethodDelete, "/v1/apis/"+apiID+"/datasources/myLambda", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete datasource: want 204 got %d", w.Code)
	}

	// Get after delete -> 404
	w = doJSON(t, svc, http.MethodGet, "/v1/apis/"+apiID+"/datasources/myLambda", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("get after delete: want 404 got %d", w.Code)
	}
}

// --- Resolver ---

func TestResolverCRUD(t *testing.T) {
	svc := newSvc()
	apiID := createAPI(t, svc)

	// Create
	w := doJSON(t, svc, http.MethodPost, "/v1/apis/"+apiID+"/types/Query/resolvers", map[string]string{
		"fieldName":      "hello",
		"dataSourceName": "myLambda",
		"kind":           "UNIT",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create resolver: want 200 got %d: %s", w.Code, w.Body)
	}

	// Get
	w = doJSON(t, svc, http.MethodGet, "/v1/apis/"+apiID+"/types/Query/resolvers/hello", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get resolver: want 200 got %d", w.Code)
	}

	// Delete
	w = doJSON(t, svc, http.MethodDelete, "/v1/apis/"+apiID+"/types/Query/resolvers/hello", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete resolver: want 204 got %d", w.Code)
	}
}

// --- API key ---

func TestApiKeyCRUD(t *testing.T) {
	svc := newSvc()
	apiID := createAPI(t, svc)

	// Create
	w := doJSON(t, svc, http.MethodPost, "/v1/apis/"+apiID+"/ApiKeys", map[string]string{
		"description": "test key",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create apikey: want 200 got %d: %s", w.Code, w.Body)
	}
	var keyResp struct {
		ApiKey struct {
			ID string `json:"id"`
		} `json:"apiKey"`
	}
	decode(t, w, &keyResp)
	keyID := keyResp.ApiKey.ID
	if keyID == "" {
		t.Fatal("expected key id")
	}

	// List
	w = doJSON(t, svc, http.MethodGet, "/v1/apis/"+apiID+"/ApiKeys", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list apikeys: want 200 got %d", w.Code)
	}
	var listResp struct {
		ApiKeys []struct {
			ID string `json:"id"`
		} `json:"apiKeys"`
	}
	decode(t, w, &listResp)
	if len(listResp.ApiKeys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(listResp.ApiKeys))
	}

	// Delete
	w = doJSON(t, svc, http.MethodDelete, "/v1/apis/"+apiID+"/ApiKeys/"+keyID, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete apikey: want 204 got %d", w.Code)
	}
}

// --- Tags ---

func TestTagResource(t *testing.T) {
	svc := newSvc()
	apiID := createAPI(t, svc)
	arn := "arn:aws:appsync:us-east-1:000000000000:apis/" + apiID

	// Tag
	w := doJSON(t, svc, http.MethodPost, "/v1/tags/"+arn, map[string]interface{}{
		"tags": map[string]string{"env": "test"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("tag: want 200 got %d: %s", w.Code, w.Body)
	}

	// List
	w = doJSON(t, svc, http.MethodGet, "/v1/tags/"+arn, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("listTags: want 200 got %d", w.Code)
	}
	var tagsResp struct {
		Tags map[string]string `json:"tags"`
	}
	decode(t, w, &tagsResp)
	if tagsResp.Tags["env"] != "test" {
		t.Fatalf("tag mismatch: %v", tagsResp.Tags)
	}
}

// --- Detect ---

func TestDetect(t *testing.T) {
	svc := newSvc()
	cases := []struct {
		path string
		want bool
	}{
		{"/v1/apis", true},
		{"/v1/apis/abc123", true},
		{"/v1/apis/abc123/datasources", true},
		{"/v1/tags/arn%3Aaws%3Aappsync%3A...", true},
		{"/restapis/abc", false},
		{"/", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, c.path, nil)
		if got := svc.Detect(r); got != c.want {
			t.Errorf("Detect(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// --- Reset ---

func TestReset(t *testing.T) {
	svc := newSvc()
	doJSON(t, svc, http.MethodPost, "/v1/apis", map[string]string{"name": "MyAPI"})
	if svc.APICount() != 1 {
		t.Fatal("expected 1 api before reset")
	}
	svc.Reset()
	if svc.APICount() != 0 {
		t.Fatal("expected 0 apis after reset")
	}
}
