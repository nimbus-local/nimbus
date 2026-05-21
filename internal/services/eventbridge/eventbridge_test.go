package eventbridge

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

func ebRequest(t *testing.T, svc *Service, action string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	req := httptest.NewRequest(http.MethodPost, "/", &buf)
	req.Header.Set("X-Amz-Target", "AmazonEventBridge."+action)
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	return w
}

func decodeJSON(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
		t.Fatalf("failed to decode JSON response: %v\nbody: %s", err, w.Body.String())
	}
	return out
}

// --- Detect ---

func TestDetect(t *testing.T) {
	svc := newTestService()
	cases := []struct {
		target   string
		expected bool
	}{
		{"AmazonEventBridge.PutEvents", true},
		{"AmazonEventBridge.CreateEventBus", true},
		{"AmazonSQS.SendMessage", false},
		{"AmazonSimpleEmailService.SendEmail", false},
		{"", false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		if tc.target != "" {
			req.Header.Set("X-Amz-Target", tc.target)
		}
		if got := svc.Detect(req); got != tc.expected {
			t.Errorf("Detect(%q): expected %v, got %v", tc.target, tc.expected, got)
		}
	}
}

// --- PutEvents ---

func TestPutEvents(t *testing.T) {
	svc := newTestService()

	w := ebRequest(t, svc, "PutEvents", map[string]interface{}{
		"Entries": []map[string]interface{}{
			{
				"Source":     "my-app",
				"DetailType": "order-placed",
				"Detail":     `{"orderId":"123"}`,
			},
		},
	})

	if w.Code != http.StatusOK {
		t.Fatalf("PutEvents: expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	resp := decodeJSON(t, w)
	if resp["FailedEntryCount"].(float64) != 0 {
		t.Errorf("expected 0 failures")
	}
	entries := resp["Entries"].([]interface{})
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry in response, got %d", len(entries))
	}
	entry := entries[0].(map[string]interface{})
	if entry["EventId"] == "" {
		t.Error("expected non-empty EventId")
	}
}

func TestPutEvents_CapturesEvent(t *testing.T) {
	svc := newTestService()

	ebRequest(t, svc, "PutEvents", map[string]interface{}{
		"Entries": []map[string]interface{}{
			{
				"EventBusName": "default",
				"Source":       "my-app",
				"DetailType":   "user-created",
				"Detail":       `{"userId":"456"}`,
				"Resources":    []string{"arn:aws:ec2:us-east-1:123456789012:instance/i-1234567890abcdef0"},
			},
		},
	})

	if svc.EventCount() != 1 {
		t.Fatalf("expected 1 captured event, got %d", svc.EventCount())
	}

	req := httptest.NewRequest(http.MethodGet, "/_nimbus/eventbridge/events", nil)
	rw := httptest.NewRecorder()
	svc.EventsHandler(rw, req)

	var events []*CapturedEvent
	json.NewDecoder(rw.Body).Decode(&events)

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	e := events[0]
	if e.Source != "my-app" {
		t.Errorf("expected Source=my-app, got %q", e.Source)
	}
	if e.DetailType != "user-created" {
		t.Errorf("expected DetailType=user-created, got %q", e.DetailType)
	}
	if e.Detail != `{"userId":"456"}` {
		t.Errorf("unexpected Detail: %q", e.Detail)
	}
	if len(e.Resources) != 1 {
		t.Errorf("expected 1 resource, got %d", len(e.Resources))
	}
	if e.EventBusName != "default" {
		t.Errorf("expected EventBusName=default, got %q", e.EventBusName)
	}
}

func TestPutEvents_DefaultBusWhenOmitted(t *testing.T) {
	svc := newTestService()

	ebRequest(t, svc, "PutEvents", map[string]interface{}{
		"Entries": []map[string]interface{}{
			{"Source": "app", "DetailType": "event", "Detail": "{}"},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/_nimbus/eventbridge/events", nil)
	rw := httptest.NewRecorder()
	svc.EventsHandler(rw, req)

	var events []*CapturedEvent
	json.NewDecoder(rw.Body).Decode(&events)
	if len(events) != 1 || events[0].EventBusName != "default" {
		t.Errorf("expected EventBusName=default, got %q", events[0].EventBusName)
	}
}

func TestPutEvents_MultipleEntries(t *testing.T) {
	svc := newTestService()

	entries := make([]map[string]interface{}, 5)
	for i := range entries {
		entries[i] = map[string]interface{}{
			"Source":     "app",
			"DetailType": "tick",
			"Detail":     "{}",
		}
	}

	w := ebRequest(t, svc, "PutEvents", map[string]interface{}{"Entries": entries})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if svc.EventCount() != 5 {
		t.Errorf("expected 5 captured events, got %d", svc.EventCount())
	}
}

// --- Event buses ---

func TestCreateEventBus(t *testing.T) {
	svc := newTestService()

	w := ebRequest(t, svc, "CreateEventBus", map[string]string{"Name": "my-bus"})
	if w.Code != http.StatusOK {
		t.Fatalf("CreateEventBus: expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	resp := decodeJSON(t, w)
	arn, _ := resp["EventBusArn"].(string)
	if !contains(arn, "my-bus") {
		t.Errorf("expected ARN to contain bus name, got %q", arn)
	}
}

func TestDescribeEventBus_Default(t *testing.T) {
	svc := newTestService()

	w := ebRequest(t, svc, "DescribeEventBus", map[string]string{})
	if w.Code != http.StatusOK {
		t.Fatalf("DescribeEventBus default: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	resp := decodeJSON(t, w)
	if resp["Name"] != "default" {
		t.Errorf("expected Name=default, got %v", resp["Name"])
	}
}

func TestDescribeEventBus_NotFound(t *testing.T) {
	svc := newTestService()

	w := ebRequest(t, svc, "DescribeEventBus", map[string]string{"Name": "no-such-bus"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing bus, got %d", w.Code)
	}
}

func TestDeleteEventBus(t *testing.T) {
	svc := newTestService()

	ebRequest(t, svc, "CreateEventBus", map[string]string{"Name": "temp-bus"})
	w := ebRequest(t, svc, "DeleteEventBus", map[string]string{"Name": "temp-bus"})
	if w.Code != http.StatusOK {
		t.Fatalf("DeleteEventBus: expected 200, got %d", w.Code)
	}

	dw := ebRequest(t, svc, "DescribeEventBus", map[string]string{"Name": "temp-bus"})
	if dw.Code != http.StatusBadRequest {
		t.Errorf("expected bus to be gone, got %d", dw.Code)
	}
}

func TestDeleteEventBus_DefaultForbidden(t *testing.T) {
	svc := newTestService()

	w := ebRequest(t, svc, "DeleteEventBus", map[string]string{"Name": "default"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 when deleting default bus, got %d", w.Code)
	}
}

func TestListEventBuses(t *testing.T) {
	svc := newTestService()

	ebRequest(t, svc, "CreateEventBus", map[string]string{"Name": "bus-a"})
	ebRequest(t, svc, "CreateEventBus", map[string]string{"Name": "bus-b"})

	w := ebRequest(t, svc, "ListEventBuses", map[string]string{})
	if w.Code != http.StatusOK {
		t.Fatalf("ListEventBuses: expected 200, got %d", w.Code)
	}

	resp := decodeJSON(t, w)
	buses := resp["EventBuses"].([]interface{})
	if len(buses) < 3 { // default + bus-a + bus-b
		t.Errorf("expected at least 3 buses, got %d", len(buses))
	}
}

func TestListEventBuses_NamePrefix(t *testing.T) {
	svc := newTestService()

	ebRequest(t, svc, "CreateEventBus", map[string]string{"Name": "prod-orders"})
	ebRequest(t, svc, "CreateEventBus", map[string]string{"Name": "prod-users"})
	ebRequest(t, svc, "CreateEventBus", map[string]string{"Name": "dev-orders"})

	w := ebRequest(t, svc, "ListEventBuses", map[string]string{"NamePrefix": "prod"})
	resp := decodeJSON(t, w)
	buses := resp["EventBuses"].([]interface{})
	if len(buses) != 2 {
		t.Errorf("expected 2 prod buses, got %d", len(buses))
	}
}

// --- Rules ---

func TestPutRule(t *testing.T) {
	svc := newTestService()

	w := ebRequest(t, svc, "PutRule", map[string]interface{}{
		"Name":         "my-rule",
		"EventPattern": `{"source":["my-app"]}`,
		"State":        "ENABLED",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("PutRule: expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	resp := decodeJSON(t, w)
	arn, _ := resp["RuleArn"].(string)
	if !contains(arn, "my-rule") {
		t.Errorf("expected RuleArn to contain rule name, got %q", arn)
	}
}

func TestDescribeRule(t *testing.T) {
	svc := newTestService()

	ebRequest(t, svc, "PutRule", map[string]interface{}{
		"Name":         "my-rule",
		"EventPattern": `{"source":["my-app"]}`,
		"State":        "ENABLED",
		"Description":  "a test rule",
	})

	w := ebRequest(t, svc, "DescribeRule", map[string]string{"Name": "my-rule"})
	if w.Code != http.StatusOK {
		t.Fatalf("DescribeRule: expected 200, got %d", w.Code)
	}

	resp := decodeJSON(t, w)
	if resp["Name"] != "my-rule" {
		t.Errorf("expected Name=my-rule, got %v", resp["Name"])
	}
	if resp["State"] != "ENABLED" {
		t.Errorf("expected State=ENABLED, got %v", resp["State"])
	}
	if resp["Description"] != "a test rule" {
		t.Errorf("expected Description='a test rule', got %v", resp["Description"])
	}
}

func TestDescribeRule_NotFound(t *testing.T) {
	svc := newTestService()

	w := ebRequest(t, svc, "DescribeRule", map[string]string{"Name": "no-such-rule"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing rule, got %d", w.Code)
	}
}

func TestDeleteRule(t *testing.T) {
	svc := newTestService()

	ebRequest(t, svc, "PutRule", map[string]interface{}{"Name": "tmp", "EventPattern": "{}"})
	w := ebRequest(t, svc, "DeleteRule", map[string]string{"Name": "tmp"})
	if w.Code != http.StatusOK {
		t.Fatalf("DeleteRule: expected 200, got %d", w.Code)
	}

	dw := ebRequest(t, svc, "DescribeRule", map[string]string{"Name": "tmp"})
	if dw.Code != http.StatusBadRequest {
		t.Errorf("expected rule to be gone, got %d", dw.Code)
	}
}

func TestListRules(t *testing.T) {
	svc := newTestService()

	ebRequest(t, svc, "PutRule", map[string]interface{}{"Name": "rule-1", "EventPattern": "{}"})
	ebRequest(t, svc, "PutRule", map[string]interface{}{"Name": "rule-2", "EventPattern": "{}"})

	w := ebRequest(t, svc, "ListRules", map[string]string{})
	if w.Code != http.StatusOK {
		t.Fatalf("ListRules: expected 200, got %d", w.Code)
	}

	resp := decodeJSON(t, w)
	rules := resp["Rules"].([]interface{})
	if len(rules) != 2 {
		t.Errorf("expected 2 rules, got %d", len(rules))
	}
}

func TestEnableDisableRule(t *testing.T) {
	svc := newTestService()

	ebRequest(t, svc, "PutRule", map[string]interface{}{"Name": "my-rule", "EventPattern": "{}", "State": "ENABLED"})

	ebRequest(t, svc, "DisableRule", map[string]string{"Name": "my-rule"})
	w := ebRequest(t, svc, "DescribeRule", map[string]string{"Name": "my-rule"})
	resp := decodeJSON(t, w)
	if resp["State"] != "DISABLED" {
		t.Errorf("expected State=DISABLED after DisableRule, got %v", resp["State"])
	}

	ebRequest(t, svc, "EnableRule", map[string]string{"Name": "my-rule"})
	w = ebRequest(t, svc, "DescribeRule", map[string]string{"Name": "my-rule"})
	resp = decodeJSON(t, w)
	if resp["State"] != "ENABLED" {
		t.Errorf("expected State=ENABLED after EnableRule, got %v", resp["State"])
	}
}

// --- Targets ---

func TestPutTargets(t *testing.T) {
	svc := newTestService()

	ebRequest(t, svc, "PutRule", map[string]interface{}{"Name": "my-rule", "EventPattern": "{}"})
	w := ebRequest(t, svc, "PutTargets", map[string]interface{}{
		"Rule": "my-rule",
		"Targets": []map[string]string{
			{"Id": "1", "Arn": "arn:aws:lambda:us-east-1:000000000000:function:my-func"},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("PutTargets: expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	resp := decodeJSON(t, w)
	if resp["FailedEntryCount"].(float64) != 0 {
		t.Errorf("expected 0 failures")
	}
}

func TestListTargetsByRule(t *testing.T) {
	svc := newTestService()

	ebRequest(t, svc, "PutRule", map[string]interface{}{"Name": "my-rule", "EventPattern": "{}"})
	ebRequest(t, svc, "PutTargets", map[string]interface{}{
		"Rule": "my-rule",
		"Targets": []map[string]string{
			{"Id": "1", "Arn": "arn:aws:lambda:us-east-1:000000000000:function:fn-a"},
			{"Id": "2", "Arn": "arn:aws:sqs:us-east-1:000000000000:my-queue"},
		},
	})

	w := ebRequest(t, svc, "ListTargetsByRule", map[string]string{"Rule": "my-rule"})
	if w.Code != http.StatusOK {
		t.Fatalf("ListTargetsByRule: expected 200, got %d", w.Code)
	}

	resp := decodeJSON(t, w)
	targets := resp["Targets"].([]interface{})
	if len(targets) != 2 {
		t.Errorf("expected 2 targets, got %d", len(targets))
	}
}

func TestRemoveTargets(t *testing.T) {
	svc := newTestService()

	ebRequest(t, svc, "PutRule", map[string]interface{}{"Name": "my-rule", "EventPattern": "{}"})
	ebRequest(t, svc, "PutTargets", map[string]interface{}{
		"Rule": "my-rule",
		"Targets": []map[string]string{
			{"Id": "1", "Arn": "arn:aws:lambda:us-east-1:000000000000:function:fn"},
			{"Id": "2", "Arn": "arn:aws:sqs:us-east-1:000000000000:q"},
		},
	})
	ebRequest(t, svc, "RemoveTargets", map[string]interface{}{
		"Rule": "my-rule",
		"Ids":  []string{"1"},
	})

	w := ebRequest(t, svc, "ListTargetsByRule", map[string]string{"Rule": "my-rule"})
	resp := decodeJSON(t, w)
	targets := resp["Targets"].([]interface{})
	if len(targets) != 1 {
		t.Errorf("expected 1 remaining target, got %d", len(targets))
	}
}

// --- Inspection endpoints ---

func TestEventsHandler_Empty(t *testing.T) {
	svc := newTestService()

	req := httptest.NewRequest(http.MethodGet, "/_nimbus/eventbridge/events", nil)
	w := httptest.NewRecorder()
	svc.EventsHandler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("EventsHandler empty: expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if body != "[]\n" && body != "[]" {
		t.Errorf("expected empty array, got %q", body)
	}
}

func TestClearEventsHandler(t *testing.T) {
	svc := newTestService()

	ebRequest(t, svc, "PutEvents", map[string]interface{}{
		"Entries": []map[string]interface{}{
			{"Source": "app", "DetailType": "ev", "Detail": "{}"},
		},
	})

	if svc.EventCount() != 1 {
		t.Fatalf("expected 1 event before clear, got %d", svc.EventCount())
	}

	req := httptest.NewRequest(http.MethodDelete, "/_nimbus/eventbridge/events", nil)
	w := httptest.NewRecorder()
	svc.ClearEventsHandler(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("ClearEvents: expected 204, got %d", w.Code)
	}
	if svc.EventCount() != 0 {
		t.Errorf("expected 0 events after clear, got %d", svc.EventCount())
	}
}

// --- Unknown action ---

func TestUnknownAction(t *testing.T) {
	svc := newTestService()

	w := ebRequest(t, svc, "TagResource", map[string]string{})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown action, got %d", w.Code)
	}
}

// --- helpers ---

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && s != "" && substr != "" &&
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}()
}
