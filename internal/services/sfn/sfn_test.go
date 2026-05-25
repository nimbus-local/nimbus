package sfn

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

func sfnRequest(t *testing.T, svc *Service, op string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatalf("encode: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", &buf)
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonStepFunctions."+op)
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	return w
}

func body(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&m); err != nil {
		t.Fatalf("decode response: %v\nbody: %s", err, w.Body.String())
	}
	return m
}

const testDef = `{"Comment":"test","StartAt":"Pass","States":{"Pass":{"Type":"Pass","End":true}}}`

func TestCreateStateMachine(t *testing.T) {
	svc := newTestService()

	w := sfnRequest(t, svc, "CreateStateMachine", map[string]interface{}{
		"name":       "my-sm",
		"definition": testDef,
		"roleArn":    "arn:aws:iam::000000000000:role/test",
	})

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	b := body(t, w)
	arn, ok := b["stateMachineArn"].(string)
	if !ok || arn == "" {
		t.Fatalf("expected stateMachineArn in response, got %v", b)
	}
	if !contains(arn, "my-sm") {
		t.Errorf("ARN %q should contain state machine name", arn)
	}
}

func TestCreateStateMachineDuplicate(t *testing.T) {
	svc := newTestService()
	req := map[string]interface{}{"name": "dup", "definition": testDef, "roleArn": "arn:aws:iam::000000000000:role/r"}
	sfnRequest(t, svc, "CreateStateMachine", req)
	w := sfnRequest(t, svc, "CreateStateMachine", req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on duplicate, got %d", w.Code)
	}
}

func TestDescribeStateMachine(t *testing.T) {
	svc := newTestService()
	w := sfnRequest(t, svc, "CreateStateMachine", map[string]interface{}{
		"name": "desc-sm", "definition": testDef, "roleArn": "arn:aws:iam::000000000000:role/r",
	})
	arn := body(t, w)["stateMachineArn"].(string)

	w2 := sfnRequest(t, svc, "DescribeStateMachine", map[string]interface{}{"stateMachineArn": arn})
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w2.Code, w2.Body.String())
	}
	b := body(t, w2)
	if b["definition"] != testDef {
		t.Errorf("definition mismatch: got %v", b["definition"])
	}
	if b["status"] != "ACTIVE" {
		t.Errorf("expected status ACTIVE, got %v", b["status"])
	}
}

func TestUpdateStateMachine(t *testing.T) {
	svc := newTestService()
	w := sfnRequest(t, svc, "CreateStateMachine", map[string]interface{}{
		"name": "upd-sm", "definition": testDef, "roleArn": "arn:aws:iam::000000000000:role/r",
	})
	arn := body(t, w)["stateMachineArn"].(string)

	newDef := `{"Comment":"updated","StartAt":"Pass","States":{"Pass":{"Type":"Pass","End":true}}}`
	w2 := sfnRequest(t, svc, "UpdateStateMachine", map[string]interface{}{
		"stateMachineArn": arn,
		"definition":      newDef,
	})
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w2.Code, w2.Body.String())
	}

	w3 := sfnRequest(t, svc, "DescribeStateMachine", map[string]interface{}{"stateMachineArn": arn})
	if body(t, w3)["definition"] != newDef {
		t.Error("definition was not updated")
	}
}

func TestDeleteStateMachine(t *testing.T) {
	svc := newTestService()
	w := sfnRequest(t, svc, "CreateStateMachine", map[string]interface{}{
		"name": "del-sm", "definition": testDef, "roleArn": "arn:aws:iam::000000000000:role/r",
	})
	arn := body(t, w)["stateMachineArn"].(string)

	w2 := sfnRequest(t, svc, "DeleteStateMachine", map[string]interface{}{"stateMachineArn": arn})
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w2.Code, w2.Body.String())
	}

	w3 := sfnRequest(t, svc, "DescribeStateMachine", map[string]interface{}{"stateMachineArn": arn})
	if w3.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 after delete, got %d", w3.Code)
	}
}

func TestListStateMachines(t *testing.T) {
	svc := newTestService()
	sfnRequest(t, svc, "CreateStateMachine", map[string]interface{}{"name": "sm1", "definition": testDef, "roleArn": "r"})
	sfnRequest(t, svc, "CreateStateMachine", map[string]interface{}{"name": "sm2", "definition": testDef, "roleArn": "r"})

	w := sfnRequest(t, svc, "ListStateMachines", map[string]interface{}{})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	b := body(t, w)
	list, ok := b["stateMachines"].([]interface{})
	if !ok || len(list) != 2 {
		t.Errorf("expected 2 state machines, got %v", b["stateMachines"])
	}
}

func TestTagging(t *testing.T) {
	svc := newTestService()
	w := sfnRequest(t, svc, "CreateStateMachine", map[string]interface{}{
		"name": "tagged-sm", "definition": testDef, "roleArn": "r",
		"tags": []map[string]string{{"key": "env", "value": "local"}},
	})
	arn := body(t, w)["stateMachineArn"].(string)

	// ListTags
	w2 := sfnRequest(t, svc, "ListTagsForResource", map[string]interface{}{"resourceArn": arn})
	b := body(t, w2)
	tags := b["tags"].([]interface{})
	if len(tags) != 1 {
		t.Fatalf("expected 1 tag, got %v", tags)
	}

	// TagResource
	sfnRequest(t, svc, "TagResource", map[string]interface{}{
		"resourceArn": arn,
		"tags":        []map[string]string{{"key": "team", "value": "forge"}},
	})
	w3 := sfnRequest(t, svc, "ListTagsForResource", map[string]interface{}{"resourceArn": arn})
	b3 := body(t, w3)
	if len(b3["tags"].([]interface{})) != 2 {
		t.Errorf("expected 2 tags after TagResource, got %v", b3["tags"])
	}

	// UntagResource
	sfnRequest(t, svc, "UntagResource", map[string]interface{}{
		"resourceArn": arn,
		"tagKeys":     []string{"env"},
	})
	w4 := sfnRequest(t, svc, "ListTagsForResource", map[string]interface{}{"resourceArn": arn})
	b4 := body(t, w4)
	if len(b4["tags"].([]interface{})) != 1 {
		t.Errorf("expected 1 tag after UntagResource, got %v", b4["tags"])
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
