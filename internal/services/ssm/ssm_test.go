package ssm

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestService() *Service {
	return New("us-east-1")
}

func ssmRequest(t *testing.T, svc *Service, operation string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatalf("failed to encode request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", &buf)
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "AmazonSSM."+operation)
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	return w
}

func responseBody(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var result map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v\nbody: %s", err, w.Body.String())
	}
	return result
}

// --- PutParameter ---

func TestPutParameter(t *testing.T) {
	svc := newTestService()

	w := ssmRequest(t, svc, "PutParameter", map[string]interface{}{
		"Name":  "/myapp/db-password",
		"Value": "secret123",
		"Type":  "SecureString",
	})

	if w.Code != http.StatusOK {
		t.Fatalf("PutParameter: expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	body := responseBody(t, w)
	if body["Version"] != float64(1) {
		t.Errorf("expected Version=1, got %v", body["Version"])
	}
}

func TestPutParameter_DefaultTypeIsString(t *testing.T) {
	svc := newTestService()

	ssmRequest(t, svc, "PutParameter", map[string]interface{}{
		"Name":  "/myapp/feature-flag",
		"Value": "true",
	})

	w := ssmRequest(t, svc, "GetParameter", map[string]interface{}{
		"Name": "/myapp/feature-flag",
	})

	body := responseBody(t, w)
	param := body["Parameter"].(map[string]interface{})
	if param["Type"] != "String" {
		t.Errorf("expected default type String, got %v", param["Type"])
	}
}

func TestPutParameter_InvalidType(t *testing.T) {
	svc := newTestService()

	w := ssmRequest(t, svc, "PutParameter", map[string]interface{}{
		"Name":  "/myapp/test",
		"Value": "value",
		"Type":  "InvalidType",
	})

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid type, got %d", w.Code)
	}
}

func TestPutParameter_MissingName(t *testing.T) {
	svc := newTestService()

	w := ssmRequest(t, svc, "PutParameter", map[string]interface{}{
		"Value": "value",
	})

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing name, got %d", w.Code)
	}
}

func TestPutParameter_MissingValue(t *testing.T) {
	svc := newTestService()

	w := ssmRequest(t, svc, "PutParameter", map[string]interface{}{
		"Name": "/myapp/test",
	})

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing value, got %d", w.Code)
	}
}

func TestPutParameter_DuplicateWithoutOverwrite(t *testing.T) {
	svc := newTestService()

	ssmRequest(t, svc, "PutParameter", map[string]interface{}{
		"Name":  "/myapp/test",
		"Value": "v1",
		"Type":  "String",
	})

	w := ssmRequest(t, svc, "PutParameter", map[string]interface{}{
		"Name":  "/myapp/test",
		"Value": "v2",
		"Type":  "String",
	})

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for duplicate without overwrite, got %d", w.Code)
	}

	body := responseBody(t, w)
	if body["__type"] != "ParameterAlreadyExists" {
		t.Errorf("expected ParameterAlreadyExists, got %v", body["__type"])
	}
}

func TestPutParameter_OverwriteIncrementsVersion(t *testing.T) {
	svc := newTestService()

	ssmRequest(t, svc, "PutParameter", map[string]interface{}{
		"Name":  "/myapp/test",
		"Value": "v1",
		"Type":  "String",
	})

	w := ssmRequest(t, svc, "PutParameter", map[string]interface{}{
		"Name":      "/myapp/test",
		"Value":     "v2",
		"Type":      "String",
		"Overwrite": true,
	})

	if w.Code != http.StatusOK {
		t.Fatalf("PutParameter overwrite: expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	body := responseBody(t, w)
	if body["Version"] != float64(2) {
		t.Errorf("expected Version=2 after overwrite, got %v", body["Version"])
	}
}

func TestPutParameter_AllTypes(t *testing.T) {
	svc := newTestService()

	cases := []struct {
		name      string
		paramType string
	}{
		{"String", "String"},
		{"StringList", "StringList"},
		{"SecureString", "SecureString"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := ssmRequest(t, svc, "PutParameter", map[string]interface{}{
				"Name":  "/test/" + tc.name,
				"Value": "value",
				"Type":  tc.paramType,
			})
			if w.Code != http.StatusOK {
				t.Errorf("PutParameter type %s: expected 200, got %d", tc.paramType, w.Code)
			}
		})
	}
}

// --- GetParameter ---

func TestGetParameter(t *testing.T) {
	svc := newTestService()

	ssmRequest(t, svc, "PutParameter", map[string]interface{}{
		"Name":  "/myapp/db-host",
		"Value": "localhost",
		"Type":  "String",
	})

	w := ssmRequest(t, svc, "GetParameter", map[string]interface{}{
		"Name": "/myapp/db-host",
	})

	if w.Code != http.StatusOK {
		t.Fatalf("GetParameter: expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	body := responseBody(t, w)
	param := body["Parameter"].(map[string]interface{})

	if param["Name"] != "/myapp/db-host" {
		t.Errorf("expected Name=/myapp/db-host, got %v", param["Name"])
	}
	if param["Value"] != "localhost" {
		t.Errorf("expected Value=localhost, got %v", param["Value"])
	}
	if param["Type"] != "String" {
		t.Errorf("expected Type=String, got %v", param["Type"])
	}
	if param["Version"] != float64(1) {
		t.Errorf("expected Version=1, got %v", param["Version"])
	}
	if param["ARN"] == "" {
		t.Error("expected non-empty ARN")
	}
}

func TestGetParameter_ARNFormat(t *testing.T) {
	svc := newTestService()

	ssmRequest(t, svc, "PutParameter", map[string]interface{}{
		"Name":  "/myapp/test",
		"Value": "value",
		"Type":  "String",
	})

	w := ssmRequest(t, svc, "GetParameter", map[string]interface{}{
		"Name": "/myapp/test",
	})

	body := responseBody(t, w)
	param := body["Parameter"].(map[string]interface{})
	arn := param["ARN"].(string)

	expected := "arn:aws:ssm:us-east-1:000000000000:parameter/myapp/test"
	if arn != expected {
		t.Errorf("expected ARN %s, got %s", expected, arn)
	}
}

func TestGetParameter_NotFound(t *testing.T) {
	svc := newTestService()

	w := ssmRequest(t, svc, "GetParameter", map[string]interface{}{
		"Name": "/ghost/parameter",
	})

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing parameter, got %d", w.Code)
	}

	body := responseBody(t, w)
	if body["__type"] != "ParameterNotFound" {
		t.Errorf("expected ParameterNotFound, got %v", body["__type"])
	}
}

func TestGetParameter_MissingName(t *testing.T) {
	svc := newTestService()

	w := ssmRequest(t, svc, "GetParameter", map[string]interface{}{})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing name, got %d", w.Code)
	}
}

// --- GetParameters ---

func TestGetParameters(t *testing.T) {
	svc := newTestService()

	for _, name := range []string{"/app/a", "/app/b", "/app/c"} {
		ssmRequest(t, svc, "PutParameter", map[string]interface{}{
			"Name":  name,
			"Value": "value-" + name,
			"Type":  "String",
		})
	}

	w := ssmRequest(t, svc, "GetParameters", map[string]interface{}{
		"Names": []string{"/app/a", "/app/b", "/app/ghost"},
	})

	if w.Code != http.StatusOK {
		t.Fatalf("GetParameters: expected 200, got %d", w.Code)
	}

	body := responseBody(t, w)
	params := body["Parameters"].([]interface{})
	invalid := body["InvalidParameters"].([]interface{})

	if len(params) != 2 {
		t.Errorf("expected 2 found parameters, got %d", len(params))
	}
	if len(invalid) != 1 {
		t.Errorf("expected 1 invalid parameter, got %d", len(invalid))
	}
	if invalid[0] != "/app/ghost" {
		t.Errorf("expected /app/ghost in invalid list, got %v", invalid[0])
	}
}

func TestGetParameters_AllInvalid(t *testing.T) {
	svc := newTestService()

	w := ssmRequest(t, svc, "GetParameters", map[string]interface{}{
		"Names": []string{"/ghost/a", "/ghost/b"},
	})

	if w.Code != http.StatusOK {
		t.Fatalf("GetParameters all invalid: expected 200, got %d", w.Code)
	}

	body := responseBody(t, w)
	invalid := body["InvalidParameters"].([]interface{})
	if len(invalid) != 2 {
		t.Errorf("expected 2 invalid parameters, got %d", len(invalid))
	}
}

// --- GetParametersByPath ---

func TestGetParametersByPath(t *testing.T) {
	svc := newTestService()

	params := []string{
		"/myapp/prod/db-host",
		"/myapp/prod/db-pass",
		"/myapp/prod/nested/value",
		"/myapp/dev/db-host",
		"/otherapp/prod/key",
	}
	for _, name := range params {
		ssmRequest(t, svc, "PutParameter", map[string]interface{}{
			"Name":  name,
			"Value": "value",
			"Type":  "String",
		})
	}

	// Non-recursive — only direct children of /myapp/prod/
	w := ssmRequest(t, svc, "GetParametersByPath", map[string]interface{}{
		"Path":      "/myapp/prod",
		"Recursive": false,
	})

	if w.Code != http.StatusOK {
		t.Fatalf("GetParametersByPath: expected 200, got %d", w.Code)
	}

	body := responseBody(t, w)
	results := body["Parameters"].([]interface{})

	// Should get db-host and db-pass but NOT nested/value
	if len(results) != 2 {
		t.Errorf("non-recursive: expected 2 results, got %d", len(results))
	}
}

func TestGetParametersByPath_Recursive(t *testing.T) {
	svc := newTestService()

	params := []string{
		"/myapp/prod/db-host",
		"/myapp/prod/db-pass",
		"/myapp/prod/nested/value",
		"/myapp/dev/db-host",
	}
	for _, name := range params {
		ssmRequest(t, svc, "PutParameter", map[string]interface{}{
			"Name":  name,
			"Value": "value",
			"Type":  "String",
		})
	}

	w := ssmRequest(t, svc, "GetParametersByPath", map[string]interface{}{
		"Path":      "/myapp/prod",
		"Recursive": true,
	})

	body := responseBody(t, w)
	results := body["Parameters"].([]interface{})

	// Should get db-host, db-pass, AND nested/value
	if len(results) != 3 {
		t.Errorf("recursive: expected 3 results, got %d", len(results))
	}
}

func TestGetParametersByPath_NoResults(t *testing.T) {
	svc := newTestService()

	w := ssmRequest(t, svc, "GetParametersByPath", map[string]interface{}{
		"Path":      "/nothing/here",
		"Recursive": true,
	})

	if w.Code != http.StatusOK {
		t.Fatalf("GetParametersByPath empty: expected 200, got %d", w.Code)
	}
}

func TestGetParametersByPath_MissingPath(t *testing.T) {
	svc := newTestService()

	w := ssmRequest(t, svc, "GetParametersByPath", map[string]interface{}{})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing path, got %d", w.Code)
	}
}

// --- DeleteParameter ---

func TestDeleteParameter(t *testing.T) {
	svc := newTestService()

	ssmRequest(t, svc, "PutParameter", map[string]interface{}{
		"Name":  "/myapp/delete-me",
		"Value": "value",
		"Type":  "String",
	})

	w := ssmRequest(t, svc, "DeleteParameter", map[string]interface{}{
		"Name": "/myapp/delete-me",
	})

	if w.Code != http.StatusOK {
		t.Fatalf("DeleteParameter: expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	// Parameter should be gone
	gw := ssmRequest(t, svc, "GetParameter", map[string]interface{}{
		"Name": "/myapp/delete-me",
	})
	if gw.Code != http.StatusBadRequest {
		t.Errorf("after delete: expected 400, got %d", gw.Code)
	}
}

func TestDeleteParameter_NotFound(t *testing.T) {
	svc := newTestService()

	w := ssmRequest(t, svc, "DeleteParameter", map[string]interface{}{
		"Name": "/ghost/param",
	})

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing parameter, got %d", w.Code)
	}

	body := responseBody(t, w)
	if body["__type"] != "ParameterNotFound" {
		t.Errorf("expected ParameterNotFound, got %v", body["__type"])
	}
}

// --- DeleteParameters ---

func TestDeleteParameters(t *testing.T) {
	svc := newTestService()

	for _, name := range []string{"/app/a", "/app/b"} {
		ssmRequest(t, svc, "PutParameter", map[string]interface{}{
			"Name":  name,
			"Value": "value",
			"Type":  "String",
		})
	}

	w := ssmRequest(t, svc, "DeleteParameters", map[string]interface{}{
		"Names": []string{"/app/a", "/app/b", "/app/ghost"},
	})

	if w.Code != http.StatusOK {
		t.Fatalf("DeleteParameters: expected 200, got %d", w.Code)
	}

	body := responseBody(t, w)
	deleted := body["DeletedParameters"].([]interface{})
	invalid := body["InvalidParameters"].([]interface{})

	if len(deleted) != 2 {
		t.Errorf("expected 2 deleted, got %d", len(deleted))
	}
	if len(invalid) != 1 {
		t.Errorf("expected 1 invalid, got %d", len(invalid))
	}
}

// --- DescribeParameters ---

func TestDescribeParameters(t *testing.T) {
	svc := newTestService()

	ssmRequest(t, svc, "PutParameter", map[string]interface{}{
		"Name":        "/myapp/param",
		"Value":       "value",
		"Type":        "String",
		"Description": "a test parameter",
	})

	w := ssmRequest(t, svc, "DescribeParameters", map[string]interface{}{})

	if w.Code != http.StatusOK {
		t.Fatalf("DescribeParameters: expected 200, got %d", w.Code)
	}

	body := responseBody(t, w)
	params := body["Parameters"].([]interface{})
	if len(params) != 1 {
		t.Fatalf("expected 1 parameter, got %d", len(params))
	}

	p := params[0].(map[string]interface{})
	if p["Name"] != "/myapp/param" {
		t.Errorf("expected Name=/myapp/param, got %v", p["Name"])
	}
	// DescribeParameters must NOT return the value
	if p["Value"] != nil {
		t.Error("DescribeParameters must not return Value")
	}
}

// --- Detect ---

func TestDetect(t *testing.T) {
	svc := newTestService()

	cases := []struct {
		name     string
		target   string
		expected bool
	}{
		{"PutParameter", "AmazonSSM.PutParameter", true},
		{"GetParameter", "AmazonSSM.GetParameter", true},
		{"GetParametersByPath", "AmazonSSM.GetParametersByPath", true},
		{"DynamoDB - not SSM", "DynamoDB_20120810.ListTables", false},
		{"SQS - not SSM", "AmazonSQS.SendMessage", false},
		{"Secrets Manager - not SSM", "secretsmanager.GetSecretValue", false},
		{"empty", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			req.Header.Set("X-Amz-Target", tc.target)
			got := svc.Detect(req)
			if got != tc.expected {
				t.Errorf("Detect(%q): expected %v, got %v", tc.target, tc.expected, got)
			}
		})
	}
}

// --- Unknown operation ---

func TestUnknownOperation(t *testing.T) {
	svc := newTestService()

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("{}"))
	req.Header.Set("X-Amz-Target", "AmazonSSM.GetInventory") // not implemented
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown operation, got %d", w.Code)
	}
}
