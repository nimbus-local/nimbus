package secretsmanager

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

// smRequest sends a JSON request to the Secrets Manager service with the
// given operation and body, returning the response recorder.
func smRequest(t *testing.T, svc *Service, operation string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatalf("failed to encode request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", &buf)
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "secretsmanager."+operation)
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	return w
}

// responseBody decodes the JSON response body into a map
func responseBody(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var result map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v\nbody: %s", err, w.Body.String())
	}
	return result
}

// --- CreateSecret ---

func TestCreateSecret(t *testing.T) {
	svc := newTestService()

	w := smRequest(t, svc, "CreateSecret", map[string]interface{}{
		"Name":         "my-secret",
		"SecretString": "supersecret",
	})

	if w.Code != http.StatusOK {
		t.Fatalf("CreateSecret: expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	body := responseBody(t, w)
	if body["Name"] != "my-secret" {
		t.Errorf("expected Name=my-secret, got %v", body["Name"])
	}
	if body["ARN"] == "" {
		t.Error("expected non-empty ARN")
	}
	if body["VersionId"] == "" {
		t.Error("expected non-empty VersionId")
	}
}

func TestCreateSecret_ARNFormat(t *testing.T) {
	svc := newTestService()

	w := smRequest(t, svc, "CreateSecret", map[string]interface{}{
		"Name": "arn-test",
	})

	body := responseBody(t, w)
	arn, ok := body["ARN"].(string)
	if !ok || arn == "" {
		t.Fatalf("expected ARN string, got %v", body["ARN"])
	}

	// ARN format: arn:aws:secretsmanager:region:account:secret:name
	if arn != "arn:aws:secretsmanager:us-east-1:000000000000:secret:arn-test" {
		t.Errorf("unexpected ARN format: %s", arn)
	}
}

func TestCreateSecret_MissingName(t *testing.T) {
	svc := newTestService()

	w := smRequest(t, svc, "CreateSecret", map[string]interface{}{})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing name, got %d", w.Code)
	}
}

func TestCreateSecret_Duplicate(t *testing.T) {
	svc := newTestService()

	smRequest(t, svc, "CreateSecret", map[string]interface{}{"Name": "dupe"})
	w := smRequest(t, svc, "CreateSecret", map[string]interface{}{"Name": "dupe"})

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for duplicate, got %d", w.Code)
	}

	body := responseBody(t, w)
	if body["__type"] != "ResourceExistsException" {
		t.Errorf("expected ResourceExistsException, got %v", body["__type"])
	}
}

func TestCreateSecret_WithDescription(t *testing.T) {
	svc := newTestService()

	smRequest(t, svc, "CreateSecret", map[string]interface{}{
		"Name":        "described-secret",
		"Description": "my important secret",
	})

	w := smRequest(t, svc, "DescribeSecret", map[string]interface{}{
		"SecretId": "described-secret",
	})

	body := responseBody(t, w)
	if body["Description"] != "my important secret" {
		t.Errorf("expected description, got %v", body["Description"])
	}
}

// --- GetSecretValue ---

func TestGetSecretValue(t *testing.T) {
	svc := newTestService()

	secretVal := "my-super-secret-value"
	smRequest(t, svc, "CreateSecret", map[string]interface{}{
		"Name":         "get-test",
		"SecretString": secretVal,
	})

	w := smRequest(t, svc, "GetSecretValue", map[string]interface{}{
		"SecretId": "get-test",
	})

	if w.Code != http.StatusOK {
		t.Fatalf("GetSecretValue: expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	body := responseBody(t, w)
	if body["SecretString"] != secretVal {
		t.Errorf("expected SecretString=%q, got %v", secretVal, body["SecretString"])
	}
	if body["Name"] != "get-test" {
		t.Errorf("expected Name=get-test, got %v", body["Name"])
	}
	if body["VersionId"] == "" {
		t.Error("expected non-empty VersionId")
	}
}

func TestGetSecretValue_ByARN(t *testing.T) {
	svc := newTestService()

	smRequest(t, svc, "CreateSecret", map[string]interface{}{
		"Name":         "arn-lookup",
		"SecretString": "value",
	})

	arn := "arn:aws:secretsmanager:us-east-1:000000000000:secret:arn-lookup"
	w := smRequest(t, svc, "GetSecretValue", map[string]interface{}{
		"SecretId": arn,
	})

	if w.Code != http.StatusOK {
		t.Errorf("GetSecretValue by ARN: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
}

func TestGetSecretValue_NotFound(t *testing.T) {
	svc := newTestService()

	w := smRequest(t, svc, "GetSecretValue", map[string]interface{}{
		"SecretId": "ghost-secret",
	})

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing secret, got %d", w.Code)
	}

	body := responseBody(t, w)
	if body["__type"] != "ResourceNotFoundException" {
		t.Errorf("expected ResourceNotFoundException, got %v", body["__type"])
	}
}

func TestGetSecretValue_NoValue(t *testing.T) {
	svc := newTestService()

	// Create secret without a value
	smRequest(t, svc, "CreateSecret", map[string]interface{}{
		"Name": "empty-secret",
	})

	w := smRequest(t, svc, "GetSecretValue", map[string]interface{}{
		"SecretId": "empty-secret",
	})

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for secret with no value, got %d", w.Code)
	}
}

// --- PutSecretValue ---

func TestPutSecretValue(t *testing.T) {
	svc := newTestService()

	smRequest(t, svc, "CreateSecret", map[string]interface{}{
		"Name":         "put-test",
		"SecretString": "original",
	})

	w := smRequest(t, svc, "PutSecretValue", map[string]interface{}{
		"SecretId":     "put-test",
		"SecretString": "updated",
	})

	if w.Code != http.StatusOK {
		t.Fatalf("PutSecretValue: expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	// Verify the new value
	gw := smRequest(t, svc, "GetSecretValue", map[string]interface{}{
		"SecretId": "put-test",
	})
	body := responseBody(t, gw)
	if body["SecretString"] != "updated" {
		t.Errorf("expected updated value, got %v", body["SecretString"])
	}
}

func TestPutSecretValue_UpdatesVersionID(t *testing.T) {
	svc := newTestService()

	smRequest(t, svc, "CreateSecret", map[string]interface{}{
		"Name":         "version-test",
		"SecretString": "v1",
	})

	// Get original version
	gw1 := smRequest(t, svc, "GetSecretValue", map[string]interface{}{
		"SecretId": "version-test",
	})
	body1 := responseBody(t, gw1)
	v1 := body1["VersionId"]

	// Update
	smRequest(t, svc, "PutSecretValue", map[string]interface{}{
		"SecretId":     "version-test",
		"SecretString": "v2",
	})

	// Get new version
	gw2 := smRequest(t, svc, "GetSecretValue", map[string]interface{}{
		"SecretId": "version-test",
	})
	body2 := responseBody(t, gw2)
	v2 := body2["VersionId"]

	if v1 == v2 {
		t.Error("expected VersionId to change after PutSecretValue")
	}
}

// --- UpdateSecret ---

func TestUpdateSecret_Value(t *testing.T) {
	svc := newTestService()

	smRequest(t, svc, "CreateSecret", map[string]interface{}{
		"Name":         "update-test",
		"SecretString": "original",
	})

	w := smRequest(t, svc, "UpdateSecret", map[string]interface{}{
		"SecretId":     "update-test",
		"SecretString": "updated-via-update",
	})

	if w.Code != http.StatusOK {
		t.Fatalf("UpdateSecret: expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	gw := smRequest(t, svc, "GetSecretValue", map[string]interface{}{
		"SecretId": "update-test",
	})
	body := responseBody(t, gw)
	if body["SecretString"] != "updated-via-update" {
		t.Errorf("expected updated-via-update, got %v", body["SecretString"])
	}
}

func TestUpdateSecret_DescriptionOnly(t *testing.T) {
	svc := newTestService()

	smRequest(t, svc, "CreateSecret", map[string]interface{}{
		"Name":         "desc-update",
		"SecretString": "value",
		"Description":  "old description",
	})

	smRequest(t, svc, "UpdateSecret", map[string]interface{}{
		"SecretId":    "desc-update",
		"Description": "new description",
	})

	w := smRequest(t, svc, "DescribeSecret", map[string]interface{}{
		"SecretId": "desc-update",
	})
	body := responseBody(t, w)
	if body["Description"] != "new description" {
		t.Errorf("expected new description, got %v", body["Description"])
	}
}

// --- DeleteSecret ---

func TestDeleteSecret(t *testing.T) {
	svc := newTestService()

	smRequest(t, svc, "CreateSecret", map[string]interface{}{
		"Name":         "delete-test",
		"SecretString": "value",
	})

	w := smRequest(t, svc, "DeleteSecret", map[string]interface{}{
		"SecretId":                   "delete-test",
		"ForceDeleteWithoutRecovery": true,
	})

	if w.Code != http.StatusOK {
		t.Fatalf("DeleteSecret: expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	// Secret should be gone
	gw := smRequest(t, svc, "GetSecretValue", map[string]interface{}{
		"SecretId": "delete-test",
	})
	if gw.Code != http.StatusBadRequest {
		t.Errorf("after force delete: expected 400, got %d", gw.Code)
	}
}

func TestDeleteSecret_ScheduledDeletion(t *testing.T) {
	svc := newTestService()

	smRequest(t, svc, "CreateSecret", map[string]interface{}{
		"Name":         "scheduled-delete",
		"SecretString": "value",
	})

	// Delete without force — schedules deletion
	w := smRequest(t, svc, "DeleteSecret", map[string]interface{}{
		"SecretId": "scheduled-delete",
	})

	if w.Code != http.StatusOK {
		t.Fatalf("DeleteSecret scheduled: expected 200, got %d", w.Code)
	}

	body := responseBody(t, w)
	if body["DeletionDate"] == nil {
		t.Error("expected DeletionDate in response")
	}

	// GetSecretValue should fail on a scheduled-for-deletion secret
	gw := smRequest(t, svc, "GetSecretValue", map[string]interface{}{
		"SecretId": "scheduled-delete",
	})
	if gw.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for deleted secret, got %d", gw.Code)
	}
}

func TestDeleteSecret_NotFound(t *testing.T) {
	svc := newTestService()

	w := smRequest(t, svc, "DeleteSecret", map[string]interface{}{
		"SecretId": "ghost",
	})

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing secret, got %d", w.Code)
	}
}

// --- RestoreSecret ---

func TestRestoreSecret(t *testing.T) {
	svc := newTestService()

	smRequest(t, svc, "CreateSecret", map[string]interface{}{
		"Name":         "restore-test",
		"SecretString": "value",
	})

	// Schedule deletion
	smRequest(t, svc, "DeleteSecret", map[string]interface{}{
		"SecretId": "restore-test",
	})

	// Restore
	w := smRequest(t, svc, "RestoreSecret", map[string]interface{}{
		"SecretId": "restore-test",
	})

	if w.Code != http.StatusOK {
		t.Fatalf("RestoreSecret: expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	// Should be accessible again
	gw := smRequest(t, svc, "GetSecretValue", map[string]interface{}{
		"SecretId": "restore-test",
	})
	if gw.Code != http.StatusOK {
		t.Errorf("after restore: expected 200, got %d", gw.Code)
	}
}

// --- ListSecrets ---

func TestListSecrets(t *testing.T) {
	svc := newTestService()

	smRequest(t, svc, "CreateSecret", map[string]interface{}{"Name": "secret-a"})
	smRequest(t, svc, "CreateSecret", map[string]interface{}{"Name": "secret-b"})
	smRequest(t, svc, "CreateSecret", map[string]interface{}{"Name": "secret-c"})

	w := smRequest(t, svc, "ListSecrets", map[string]interface{}{})

	if w.Code != http.StatusOK {
		t.Fatalf("ListSecrets: expected 200, got %d", w.Code)
	}

	body := responseBody(t, w)
	list, ok := body["SecretList"].([]interface{})
	if !ok {
		t.Fatalf("expected SecretList array, got %T", body["SecretList"])
	}
	if len(list) != 3 {
		t.Errorf("expected 3 secrets, got %d", len(list))
	}
}

func TestListSecrets_Empty(t *testing.T) {
	svc := newTestService()

	w := smRequest(t, svc, "ListSecrets", map[string]interface{}{})

	if w.Code != http.StatusOK {
		t.Fatalf("ListSecrets empty: expected 200, got %d", w.Code)
	}

	body := responseBody(t, w)
	list, ok := body["SecretList"].([]interface{})
	if ok && len(list) != 0 {
		t.Errorf("expected empty list, got %d items", len(list))
	}
}

// --- DescribeSecret ---

func TestDescribeSecret(t *testing.T) {
	svc := newTestService()

	smRequest(t, svc, "CreateSecret", map[string]interface{}{
		"Name":         "describe-me",
		"Description":  "a very important secret",
		"SecretString": "value",
	})

	w := smRequest(t, svc, "DescribeSecret", map[string]interface{}{
		"SecretId": "describe-me",
	})

	if w.Code != http.StatusOK {
		t.Fatalf("DescribeSecret: expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	body := responseBody(t, w)
	if body["Name"] != "describe-me" {
		t.Errorf("expected Name=describe-me, got %v", body["Name"])
	}
	if body["Description"] != "a very important secret" {
		t.Errorf("expected description, got %v", body["Description"])
	}
	// DescribeSecret must NOT return the secret value
	if body["SecretString"] != nil {
		t.Error("DescribeSecret must not return SecretString")
	}
}

func TestDescribeSecret_NotFound(t *testing.T) {
	svc := newTestService()

	w := smRequest(t, svc, "DescribeSecret", map[string]interface{}{
		"SecretId": "nobody",
	})

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing secret, got %d", w.Code)
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
		{"CreateSecret", "secretsmanager.CreateSecret", true},
		{"GetSecretValue", "secretsmanager.GetSecretValue", true},
		{"DynamoDB - not SM", "DynamoDB_20120810.ListTables", false},
		{"SQS - not SM", "AmazonSQS.SendMessage", false},
		{"empty target", "", false},
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
	req.Header.Set("X-Amz-Target", "secretsmanager.RotateSecret") // not implemented
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown operation, got %d", w.Code)
	}
}
