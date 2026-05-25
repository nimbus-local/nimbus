package sfn

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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
	req.Header.Set("X-Amz-Target", "AWSStepFunctions."+op)
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

// --- Part 2: execution engine helpers ---

// createSM is a test helper that creates a state machine and returns its ARN.
func createSM(t *testing.T, svc *Service, name, definition string) string {
	t.Helper()
	w := sfnRequest(t, svc, "CreateStateMachine", map[string]interface{}{
		"name":       name,
		"definition": definition,
		"roleArn":    "arn:aws:iam::000000000000:role/test",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("CreateStateMachine: %d %s", w.Code, w.Body.String())
	}
	return body(t, w)["stateMachineArn"].(string)
}

// startExec starts an execution and returns its ARN, polling until done (up to 2 s).
func startExec(t *testing.T, svc *Service, smARN, input string) string {
	t.Helper()
	req := map[string]interface{}{"stateMachineArn": smARN}
	if input != "" {
		req["input"] = input
	}
	w := sfnRequest(t, svc, "StartExecution", req)
	if w.Code != http.StatusOK {
		t.Fatalf("StartExecution: %d %s", w.Code, w.Body.String())
	}
	return body(t, w)["executionArn"].(string)
}

// waitDone polls DescribeExecution until status != RUNNING (max 5 s).
func waitDone(t *testing.T, svc *Service, execARN string) map[string]interface{} {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		w := sfnRequest(t, svc, "DescribeExecution", map[string]interface{}{"executionArn": execARN})
		if w.Code != http.StatusOK {
			t.Fatalf("DescribeExecution: %d %s", w.Code, w.Body.String())
		}
		b := body(t, w)
		if b["status"] != "RUNNING" {
			return b
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("execution did not complete within 5 s")
	return nil
}

// --- Part 2 tests ---

func TestStartExecutionPassSucceed(t *testing.T) {
	svc := newTestService()
	def := `{"StartAt":"S1","States":{"S1":{"Type":"Pass","Next":"S2"},"S2":{"Type":"Succeed"}}}`
	smARN := createSM(t, svc, "pass-succeed", def)

	execARN := startExec(t, svc, smARN, `{"x":1}`)
	result := waitDone(t, svc, execARN)

	if result["status"] != "SUCCEEDED" {
		t.Fatalf("expected SUCCEEDED, got %v", result["status"])
	}
	if result["output"] != `{"x":1}` {
		t.Errorf("expected input passed through, got %v", result["output"])
	}
}

func TestStartExecutionFail(t *testing.T) {
	svc := newTestService()
	def := `{"StartAt":"F","States":{"F":{"Type":"Fail","Error":"MyError","Cause":"test cause"}}}`
	smARN := createSM(t, svc, "fail-sm", def)

	execARN := startExec(t, svc, smARN, "")
	result := waitDone(t, svc, execARN)

	if result["status"] != "FAILED" {
		t.Fatalf("expected FAILED, got %v", result["status"])
	}
	if result["error"] != "MyError" {
		t.Errorf("expected error=MyError, got %v", result["error"])
	}
}

func TestPassWithResult(t *testing.T) {
	svc := newTestService()
	def := `{"StartAt":"P","States":{"P":{"Type":"Pass","Result":{"hello":"world"},"End":true}}}`
	smARN := createSM(t, svc, "pass-result", def)

	execARN := startExec(t, svc, smARN, `{}`)
	result := waitDone(t, svc, execARN)

	if result["status"] != "SUCCEEDED" {
		t.Fatalf("expected SUCCEEDED, got %v", result["status"])
	}
	if result["output"] != `{"hello":"world"}` {
		t.Errorf("expected Result as output, got %v", result["output"])
	}
}

func TestPassResultPath(t *testing.T) {
	svc := newTestService()
	def := `{"StartAt":"P","States":{"P":{"Type":"Pass","Result":42,"ResultPath":"$.count","End":true}}}`
	smARN := createSM(t, svc, "pass-resultpath", def)

	execARN := startExec(t, svc, smARN, `{"name":"test"}`)
	result := waitDone(t, svc, execARN)

	if result["status"] != "SUCCEEDED" {
		t.Fatalf("expected SUCCEEDED, got %v", result["status"])
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(result["output"].(string)), &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if out["name"] != "test" || out["count"] != float64(42) {
		t.Errorf("unexpected output: %v", out)
	}
}

// --- Part 3 tests ---

func TestChoiceStringEquals(t *testing.T) {
	svc := newTestService()
	def := `{"StartAt":"C","States":{` +
		`"C":{"Type":"Choice","Choices":[{"Variable":"$.env","StringEquals":"prod","Next":"Prod"}],"Default":"Dev"},` +
		`"Prod":{"Type":"Pass","Result":"production","End":true},` +
		`"Dev":{"Type":"Pass","Result":"development","End":true}}}`
	smARN := createSM(t, svc, "choice-str", def)

	// Match first rule
	e1 := startExec(t, svc, smARN, `{"env":"prod"}`)
	r1 := waitDone(t, svc, e1)
	if r1["output"] != `"production"` {
		t.Errorf("expected 'production', got %v", r1["output"])
	}

	// Fall through to Default
	e2 := startExec(t, svc, smARN, `{"env":"staging"}`)
	r2 := waitDone(t, svc, e2)
	if r2["output"] != `"development"` {
		t.Errorf("expected 'development', got %v", r2["output"])
	}
}

func TestChoiceNumeric(t *testing.T) {
	svc := newTestService()
	def := `{"StartAt":"C","States":{` +
		`"C":{"Type":"Choice","Choices":[` +
		`{"Variable":"$.n","NumericGreaterThan":10,"Next":"Big"},` +
		`{"Variable":"$.n","NumericLessThan":0,"Next":"Neg"}` +
		`],"Default":"Mid"},` +
		`"Big":{"Type":"Pass","Result":"big","End":true},` +
		`"Neg":{"Type":"Pass","Result":"neg","End":true},` +
		`"Mid":{"Type":"Pass","Result":"mid","End":true}}}`
	smARN := createSM(t, svc, "choice-num", def)

	for _, tc := range []struct{ n int; want string }{{20, `"big"`}, {-1, `"neg"`}, {5, `"mid"`}} {
		input := fmt.Sprintf(`{"n":%d}`, tc.n)
		e := startExec(t, svc, smARN, input)
		r := waitDone(t, svc, e)
		if r["output"] != tc.want {
			t.Errorf("n=%d: expected %s, got %v", tc.n, tc.want, r["output"])
		}
	}
}

func TestChoiceAndOr(t *testing.T) {
	svc := newTestService()
	def := `{"StartAt":"C","States":{` +
		`"C":{"Type":"Choice","Choices":[` +
		`{"And":[{"Variable":"$.a","BooleanEquals":true},{"Variable":"$.b","BooleanEquals":true}],"Next":"Both"},` +
		`{"Or":[{"Variable":"$.a","BooleanEquals":true},{"Variable":"$.b","BooleanEquals":true}],"Next":"Either"}` +
		`],"Default":"Neither"},` +
		`"Both":{"Type":"Pass","Result":"both","End":true},` +
		`"Either":{"Type":"Pass","Result":"either","End":true},` +
		`"Neither":{"Type":"Pass","Result":"neither","End":true}}}`
	smARN := createSM(t, svc, "choice-andor", def)

	for _, tc := range []struct{ a, b bool; want string }{
		{true, true, `"both"`},
		{true, false, `"either"`},
		{false, false, `"neither"`},
	} {
		input := fmt.Sprintf(`{"a":%v,"b":%v}`, tc.a, tc.b)
		e := startExec(t, svc, smARN, input)
		r := waitDone(t, svc, e)
		if r["output"] != tc.want {
			t.Errorf("a=%v b=%v: expected %s, got %v", tc.a, tc.b, tc.want, r["output"])
		}
	}
}

func TestWaitSeconds(t *testing.T) {
	svc := newTestService()
	def := `{"StartAt":"W","States":{"W":{"Type":"Wait","Seconds":1,"Next":"Done"},"Done":{"Type":"Succeed"}}}`
	smARN := createSM(t, svc, "wait-sm", def)

	execARN := startExec(t, svc, smARN, `{"x":42}`)
	// Should still be RUNNING immediately
	w := sfnRequest(t, svc, "DescribeExecution", map[string]interface{}{"executionArn": execARN})
	b := body(t, w)
	if b["status"] != "RUNNING" {
		t.Errorf("expected RUNNING immediately after start, got %v", b["status"])
	}
	// After waiting, should SUCCEED with input passed through
	result := waitDone(t, svc, execARN)
	if result["status"] != "SUCCEEDED" {
		t.Fatalf("expected SUCCEEDED after wait, got %v", result["status"])
	}
	if result["output"] != `{"x":42}` {
		t.Errorf("expected input passed through, got %v", result["output"])
	}
}

func TestStopExecution(t *testing.T) {
	svc := newTestService()
	def := `{"StartAt":"W","States":{"W":{"Type":"Wait","Seconds":60,"Next":"Done"},"Done":{"Type":"Succeed"}}}`
	smARN := createSM(t, svc, "stop-sm", def)

	execARN := startExec(t, svc, smARN, `{}`)
	// Stop immediately while waiting
	w := sfnRequest(t, svc, "StopExecution", map[string]interface{}{
		"executionArn": execARN,
		"error":        "ManualStop",
		"cause":        "test",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("StopExecution: %d %s", w.Code, w.Body.String())
	}

	result := waitDone(t, svc, execARN)
	if result["status"] != "ABORTED" {
		t.Fatalf("expected ABORTED, got %v", result["status"])
	}
}

func TestGetExecutionHistory(t *testing.T) {
	svc := newTestService()
	def := `{"StartAt":"P","States":{"P":{"Type":"Pass","End":true}}}`
	smARN := createSM(t, svc, "hist-sm", def)

	execARN := startExec(t, svc, smARN, `{}`)
	waitDone(t, svc, execARN)

	w := sfnRequest(t, svc, "GetExecutionHistory", map[string]interface{}{"executionArn": execARN})
	if w.Code != http.StatusOK {
		t.Fatalf("GetExecutionHistory: %d %s", w.Code, w.Body.String())
	}
	b := body(t, w)
	events := b["events"].([]interface{})
	if len(events) < 3 {
		t.Errorf("expected at least 3 events (started, entered, exited/succeeded), got %d", len(events))
	}
	// First event must be ExecutionStarted
	first := events[0].(map[string]interface{})
	if first["type"] != "ExecutionStarted" {
		t.Errorf("first event should be ExecutionStarted, got %v", first["type"])
	}
}
