package appsync_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nimbus-local/nimbus/internal/services/appsync"
)

func newSvc() *appsync.Service { return appsync.New("us-east-1", nil) }

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
		host string
		want bool
	}{
		{"/v1/apis", "", true},
		{"/v1/apis/abc123", "", true},
		{"/v1/apis/abc123/datasources", "", true},
		{"/v1/tags/arn%3Aaws%3Aappsync%3A...", "", true},
		{"/_appsync/abc123/graphql", "", true},
		{"/graphql", "abc123.appsync-api.us-east-1.nimbus.local", true},
		{"/restapis/abc", "", false},
		{"/", "", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, c.path, nil)
		if c.host != "" {
			r.Host = c.host
		}
		if got := svc.Detect(r); got != c.want {
			t.Errorf("Detect(path=%q host=%q) = %v, want %v", c.path, c.host, got, c.want)
		}
	}
}

// --- GraphQL execution ---

// mockLambda is a minimal LambdaInvoker for testing.
type mockLambda struct {
	response []byte
	err      error
}

func (m *mockLambda) DirectInvoke(_ string, _ []byte) ([]byte, error) {
	return m.response, m.err
}

func setupExecSvc(t *testing.T) (*appsync.Service, string, string) {
	t.Helper()
	ml := &mockLambda{}
	svc := appsync.New("us-east-1", ml)

	// Create API
	w := doJSON(t, svc, http.MethodPost, "/v1/apis", map[string]interface{}{
		"name":               "TestAPI",
		"authenticationType": "API_KEY",
	})
	var apiResp struct {
		GraphqlApi struct{ ApiId string } `json:"graphqlApi"`
	}
	decode(t, w, &apiResp)
	apiID := apiResp.GraphqlApi.ApiId

	// Create API key
	w = doJSON(t, svc, http.MethodPost, "/v1/apis/"+apiID+"/apikeys", map[string]interface{}{
		"description": "test-key",
	})
	var keyResp struct {
		ApiKey struct {
			ID string `json:"id"`
		} `json:"apiKey"`
	}
	decode(t, w, &keyResp)
	keyID := keyResp.ApiKey.ID

	// Create Lambda data source
	doJSON(t, svc, http.MethodPost, "/v1/apis/"+apiID+"/datasources", map[string]interface{}{
		"name":           "LambdaDS",
		"type":           "AWS_LAMBDA",
		"lambdaConfig":   map[string]string{"lambdaFunctionArn": "arn:aws:lambda:us-east-1:000000000000:function:notes-fn"},
		"serviceRoleArn": "arn:aws:iam::000000000000:role/role",
	})

	// Create resolver for Mutation.createNote
	doJSON(t, svc, http.MethodPost, "/v1/apis/"+apiID+"/types/Mutation/resolvers", map[string]interface{}{
		"fieldName":               "createNote",
		"dataSourceName":          "LambdaDS",
		"kind":                    "UNIT",
		"requestMappingTemplate":  `{"version":"2017-02-28","operation":"Invoke","payload":{"field":"createNote","args":$util.toJson($context.arguments)}}`,
		"responseMappingTemplate": "$util.toJson($context.result)",
	})

	ml.response = []byte(`{"id":"note-1","content":"Hello","createdAt":"2024-01-01T00:00:00Z"}`)
	return svc, apiID, keyID
}

func doGraphQL(t *testing.T, svc *appsync.Service, apiID, keyID, query string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"query": query})
	r := httptest.NewRequest(http.MethodPost, "/_appsync/"+apiID+"/graphql", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-api-key", keyID)
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, r)
	return w
}

func TestGraphQLExecution_PathBased(t *testing.T) {
	svc, apiID, keyID := setupExecSvc(t)

	w := doGraphQL(t, svc, apiID, keyID, `mutation { createNote(id: "note-1", content: "Hello") { id content } }`)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200 got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			CreateNote struct {
				ID      string `json:"id"`
				Content string `json:"content"`
			} `json:"createNote"`
		} `json:"data"`
	}
	decode(t, w, &resp)
	if resp.Data.CreateNote.ID != "note-1" {
		t.Fatalf("unexpected response: %s", w.Body.String())
	}
}

func TestGraphQLExecution_VirtualHost(t *testing.T) {
	svc, apiID, keyID := setupExecSvc(t)

	body, _ := json.Marshal(map[string]string{"query": `mutation { createNote(id: "note-1", content: "Hello") { id } }`})
	r := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))
	r.Host = apiID + ".appsync-api.us-east-1.nimbus.local"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-api-key", keyID)
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	decode(t, w, &resp)
	if resp["data"] == nil {
		t.Fatalf("expected data field: %s", w.Body.String())
	}
}

func TestGraphQLExecution_InvalidAPIKey(t *testing.T) {
	svc, apiID, _ := setupExecSvc(t)
	w := doGraphQL(t, svc, apiID, "wrong-key", `mutation { createNote(id: "1") { id } }`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 got %d", w.Code)
	}
}

func TestGraphQLExecution_WithVariables(t *testing.T) {
	svc, apiID, keyID := setupExecSvc(t)

	body, _ := json.Marshal(map[string]interface{}{
		"query":     `mutation CreateNote($id: String!, $content: String!) { createNote(id: $id, content: $content) { id content } }`,
		"variables": map[string]string{"id": "note-2", "content": "World"},
	})
	r := httptest.NewRequest(http.MethodPost, "/_appsync/"+apiID+"/graphql", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-api-key", keyID)
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 got %d: %s", w.Code, w.Body.String())
	}
}

func TestGraphQLExecution_NoResolver_ReturnsNullData(t *testing.T) {
	svc, apiID, keyID := setupExecSvc(t)
	// Query.nonExistent has no resolver registered
	w := doGraphQL(t, svc, apiID, keyID, `query { nonExistent { id } }`)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", w.Code)
	}
	var resp map[string]map[string]interface{}
	decode(t, w, &resp)
	if _, ok := resp["data"]["nonExistent"]; !ok {
		t.Fatalf("expected data.nonExistent key: %s", w.Body.String())
	}
	if resp["data"]["nonExistent"] != nil {
		t.Fatalf("expected null for missing resolver, got %v", resp["data"]["nonExistent"])
	}
}

func TestGraphQLExecution_NotFoundAPI(t *testing.T) {
	svc := appsync.New("us-east-1", nil)
	body, _ := json.Marshal(map[string]string{"query": `query { hello }`})
	r := httptest.NewRequest(http.MethodPost, "/_appsync/doesnotexist/graphql", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", w.Code) // GraphQL errors are 200
	}
	var resp map[string]interface{}
	decode(t, w, &resp)
	if resp["errors"] == nil {
		t.Fatalf("expected errors field: %s", w.Body.String())
	}
}

func TestGraphQLExecution_NoneDataSource(t *testing.T) {
	svc := appsync.New("us-east-1", nil)

	// Create API
	w := doJSON(t, svc, http.MethodPost, "/v1/apis", map[string]interface{}{
		"name":               "PingAPI",
		"authenticationType": "NONE",
	})
	var apiResp struct {
		GraphqlApi struct{ ApiId string } `json:"graphqlApi"`
	}
	decode(t, w, &apiResp)
	apiID := apiResp.GraphqlApi.ApiId

	// NONE data source
	doJSON(t, svc, http.MethodPost, "/v1/apis/"+apiID+"/datasources", map[string]interface{}{
		"name": "NoneDS",
		"type": "NONE",
	})
	// Resolver with hardcoded response template
	doJSON(t, svc, http.MethodPost, "/v1/apis/"+apiID+"/types/Query/resolvers", map[string]interface{}{
		"fieldName":               "ping",
		"dataSourceName":          "NoneDS",
		"kind":                    "UNIT",
		"requestMappingTemplate":  `{"version":"2018-05-29","payload":null}`,
		"responseMappingTemplate": `"pong"`,
	})

	body, _ := json.Marshal(map[string]string{"query": `query { ping }`})
	r := httptest.NewRequest(http.MethodPost, "/_appsync/"+apiID+"/graphql", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	svc.ServeHTTP(rw, r)

	if rw.Code != http.StatusOK {
		t.Fatalf("want 200 got %d: %s", rw.Code, rw.Body.String())
	}
	var resp struct {
		Data struct {
			Ping string `json:"ping"`
		} `json:"data"`
	}
	decode(t, rw, &resp)
	if resp.Data.Ping != "pong" {
		t.Fatalf("expected pong, got %q: %s", resp.Data.Ping, rw.Body.String())
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

// --- ForceNew attributes the Terraform provider reads ---

// apiType and visibility are ForceNew for the provider: omitting them from the
// read made every re-apply plan an API replacement. introspectionConfig and
// xrayEnabled are the same class of gap, in-place rather than destructive.
func TestCreateGraphqlAPIDefaultsProviderAttributes(t *testing.T) {
	svc := newSvc()

	w := doJSON(t, svc, http.MethodPost, "/v1/apis", map[string]interface{}{
		"name":               "MyAPI",
		"authenticationType": "API_KEY",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var created struct {
		GraphqlAPI map[string]interface{} `json:"graphqlApi"`
	}
	decode(t, w, &created)
	for field, want := range map[string]interface{}{
		"apiType":             "GRAPHQL",
		"visibility":          "GLOBAL",
		"introspectionConfig": "ENABLED",
		"xrayEnabled":         false,
	} {
		if got := created.GraphqlAPI[field]; got != want {
			t.Errorf("create: %s = %v, want %v", field, got, want)
		}
	}

	// The provider re-reads through GetGraphqlApi on every plan.
	apiID := created.GraphqlAPI["apiId"].(string)
	w = doJSON(t, svc, http.MethodGet, "/v1/apis/"+apiID, nil)
	var fetched struct {
		GraphqlAPI map[string]interface{} `json:"graphqlApi"`
	}
	decode(t, w, &fetched)
	for field, want := range map[string]interface{}{
		"apiType":             "GRAPHQL",
		"visibility":          "GLOBAL",
		"introspectionConfig": "ENABLED",
		"xrayEnabled":         false,
	} {
		if got := fetched.GraphqlAPI[field]; got != want {
			t.Errorf("get: %s = %v, want %v", field, got, want)
		}
	}
}

func TestCreateGraphqlAPIRoundTripsProviderAttributes(t *testing.T) {
	svc := newSvc()

	w := doJSON(t, svc, http.MethodPost, "/v1/apis", map[string]interface{}{
		"name":                "MergedAPI",
		"authenticationType":  "API_KEY",
		"apiType":             "MERGED",
		"visibility":          "PRIVATE",
		"introspectionConfig": "DISABLED",
		"xrayEnabled":         true,
	})
	var created struct {
		GraphqlAPI map[string]interface{} `json:"graphqlApi"`
	}
	decode(t, w, &created)
	for field, want := range map[string]interface{}{
		"apiType":             "MERGED",
		"visibility":          "PRIVATE",
		"introspectionConfig": "DISABLED",
		"xrayEnabled":         true,
	} {
		if got := created.GraphqlAPI[field]; got != want {
			t.Errorf("%s = %v, want %v", field, got, want)
		}
	}

	// ...and survive the read-back.
	apiID := created.GraphqlAPI["apiId"].(string)
	w = doJSON(t, svc, http.MethodGet, "/v1/apis/"+apiID, nil)
	var fetched struct {
		GraphqlAPI map[string]interface{} `json:"graphqlApi"`
	}
	decode(t, w, &fetched)
	if fetched.GraphqlAPI["apiType"] != "MERGED" || fetched.GraphqlAPI["visibility"] != "PRIVATE" {
		t.Errorf("read-back lost the values: %v", fetched.GraphqlAPI)
	}
}

// ListGraphqlApis is the other read path the provider uses.
func TestListGraphqlAPIsReportsProviderAttributes(t *testing.T) {
	svc := newSvc()
	doJSON(t, svc, http.MethodPost, "/v1/apis", map[string]interface{}{
		"name":               "MyAPI",
		"authenticationType": "API_KEY",
	})

	w := doJSON(t, svc, http.MethodGet, "/v1/apis", nil)
	var list struct {
		GraphqlAPIs []map[string]interface{} `json:"graphqlApis"`
	}
	decode(t, w, &list)
	if len(list.GraphqlAPIs) != 1 {
		t.Fatalf("expected 1 API, got %d", len(list.GraphqlAPIs))
	}
	if got := list.GraphqlAPIs[0]["apiType"]; got != "GRAPHQL" {
		t.Errorf("list: apiType = %v, want GRAPHQL", got)
	}
	if got := list.GraphqlAPIs[0]["visibility"]; got != "GLOBAL" {
		t.Errorf("list: visibility = %v, want GLOBAL", got)
	}
}
