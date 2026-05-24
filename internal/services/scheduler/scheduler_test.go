package scheduler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newSvc() *Service { return New("us-east-1", "http://localhost:4566") }

func doReq(t *testing.T, s *Service, method, path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	return w
}

func createGroup(t *testing.T, s *Service, name string) {
	t.Helper()
	w := doReq(t, s, http.MethodPost, "/schedule-groups/"+name, map[string]interface{}{})
	if w.Code != http.StatusCreated {
		t.Fatalf("createGroup %s: expected 201, got %d\n%s", name, w.Code, w.Body.String())
	}
}

func createSchedule(t *testing.T, s *Service, name, groupName string) string {
	t.Helper()
	body := map[string]interface{}{
		"GroupName":          groupName,
		"ScheduleExpression": "rate(5 minutes)",
		"FlexibleTimeWindow": map[string]string{"Mode": "OFF"},
		"Target":             map[string]string{"Arn": "arn:aws:lambda:us-east-1:000000000000:function:my-fn"},
	}
	w := doReq(t, s, http.MethodPost, "/schedules/"+name, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("createSchedule %s: expected 201, got %d\n%s", name, w.Code, w.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	return resp["ScheduleArn"]
}

// --- Name / Detect ---

func TestName(t *testing.T) {
	s := newSvc()
	if s.Name() != "scheduler" {
		t.Errorf("expected Name()=scheduler, got %s", s.Name())
	}
}

func TestDetect(t *testing.T) {
	s := newSvc()
	cases := []struct {
		path string
		want bool
	}{
		{"/schedules", true},
		{"/schedules/my-sched", true},
		{"/schedule-groups", true},
		{"/schedule-groups/default", true},
		{"/tags/arn:aws:scheduler:us-east-1:000000000000:schedule-group/default", true},
		{"/other", false},
		{"/tags/arn:aws:sqs:us-east-1:000000000000:queue/q", false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		if got := s.Detect(req); got != tc.want {
			t.Errorf("Detect(%s) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// --- Schedule groups ---

func TestCreateScheduleGroup(t *testing.T) {
	s := newSvc()
	w := doReq(t, s, http.MethodPost, "/schedule-groups/mygroup", map[string]interface{}{})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", w.Code)
	}
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if !strings.Contains(resp["ScheduleGroupArn"], "mygroup") {
		t.Errorf("expected ARN to contain group name, got %s", resp["ScheduleGroupArn"])
	}
}

func TestCreateScheduleGroup_Conflict(t *testing.T) {
	s := newSvc()
	createGroup(t, s, "dup")
	w := doReq(t, s, http.MethodPost, "/schedule-groups/dup", map[string]interface{}{})
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}
}

func TestGetScheduleGroup(t *testing.T) {
	s := newSvc()
	createGroup(t, s, "g1")
	w := doReq(t, s, http.MethodGet, "/schedule-groups/g1", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "g1") {
		t.Error("expected group name in response")
	}
}

func TestGetScheduleGroup_Default(t *testing.T) {
	s := newSvc()
	w := doReq(t, s, http.MethodGet, "/schedule-groups/default", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestGetScheduleGroup_NotFound(t *testing.T) {
	s := newSvc()
	w := doReq(t, s, http.MethodGet, "/schedule-groups/nope", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestDeleteScheduleGroup(t *testing.T) {
	s := newSvc()
	createGroup(t, s, "todelete")
	// Add a schedule to the group to test cascade.
	createSchedule(t, s, "sched-in-group", "todelete")

	w := doReq(t, s, http.MethodDelete, "/schedule-groups/todelete", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	// Group is gone.
	w2 := doReq(t, s, http.MethodGet, "/schedule-groups/todelete", nil)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", w2.Code)
	}
	// Cascaded schedule is gone.
	w3 := doReq(t, s, http.MethodGet, "/schedules/sched-in-group?groupName=todelete", nil)
	if w3.Code != http.StatusNotFound {
		t.Fatalf("expected schedule 404 after group delete, got %d", w3.Code)
	}
}

func TestDeleteScheduleGroup_DefaultBlocked(t *testing.T) {
	s := newSvc()
	w := doReq(t, s, http.MethodDelete, "/schedule-groups/default", nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}
}

func TestDeleteScheduleGroup_NotFound(t *testing.T) {
	s := newSvc()
	w := doReq(t, s, http.MethodDelete, "/schedule-groups/nope", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestListScheduleGroups(t *testing.T) {
	s := newSvc()
	createGroup(t, s, "group-a")
	createGroup(t, s, "group-b")

	w := doReq(t, s, http.MethodGet, "/schedule-groups", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "group-a") || !strings.Contains(body, "group-b") {
		t.Error("expected both groups in response")
	}
}

func TestListScheduleGroups_NamePrefix(t *testing.T) {
	s := newSvc()
	createGroup(t, s, "prod-alpha")
	createGroup(t, s, "dev-beta")

	w := doReq(t, s, http.MethodGet, "/schedule-groups?NamePrefix=prod", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "prod-alpha") {
		t.Error("expected prod-alpha in filtered response")
	}
	if strings.Contains(body, "dev-beta") {
		t.Error("expected dev-beta to be filtered out")
	}
}

func TestScheduleGroup_MethodNotAllowed(t *testing.T) {
	s := newSvc()
	createGroup(t, s, "g")
	w := doReq(t, s, http.MethodPatch, "/schedule-groups/g", nil)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

// --- Schedules ---

func TestCreateSchedule(t *testing.T) {
	s := newSvc()
	arn := createSchedule(t, s, "my-schedule", "default")
	if !strings.Contains(arn, "my-schedule") {
		t.Errorf("expected ARN to contain schedule name, got %s", arn)
	}
}

func TestCreateSchedule_GroupNotFound(t *testing.T) {
	s := newSvc()
	body := map[string]interface{}{
		"GroupName":          "nonexistent",
		"ScheduleExpression": "rate(1 minute)",
		"FlexibleTimeWindow": map[string]string{"Mode": "OFF"},
		"Target":             map[string]string{"Arn": "arn:aws:lambda:us-east-1:000000000000:function:fn"},
	}
	w := doReq(t, s, http.MethodPost, "/schedules/s1", body)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestCreateSchedule_Conflict(t *testing.T) {
	s := newSvc()
	createSchedule(t, s, "dup", "default")
	body := map[string]interface{}{
		"ScheduleExpression": "rate(1 minute)",
		"FlexibleTimeWindow": map[string]string{"Mode": "OFF"},
		"Target":             map[string]string{"Arn": "arn:aws:lambda:us-east-1:000000000000:function:fn"},
	}
	w := doReq(t, s, http.MethodPost, "/schedules/dup", body)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}
}

func TestGetSchedule(t *testing.T) {
	s := newSvc()
	createSchedule(t, s, "s1", "default")

	w := doReq(t, s, http.MethodGet, "/schedules/s1", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "s1") {
		t.Error("expected schedule name in response")
	}
}

func TestGetSchedule_WithGroupParam(t *testing.T) {
	s := newSvc()
	createGroup(t, s, "mygrp")
	createSchedule(t, s, "s2", "mygrp")

	w := doReq(t, s, http.MethodGet, "/schedules/s2?groupName=mygrp", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}
}

func TestGetSchedule_NotFound(t *testing.T) {
	s := newSvc()
	w := doReq(t, s, http.MethodGet, "/schedules/nope", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestUpdateSchedule(t *testing.T) {
	s := newSvc()
	createSchedule(t, s, "upd", "default")

	body := map[string]interface{}{
		"ScheduleExpression": "rate(10 minutes)",
		"FlexibleTimeWindow": map[string]string{"Mode": "OFF"},
		"Target":             map[string]string{"Arn": "arn:aws:lambda:us-east-1:000000000000:function:fn2"},
	}
	w := doReq(t, s, http.MethodPut, "/schedules/upd", body)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	// Verify the update persisted.
	w2 := doReq(t, s, http.MethodGet, "/schedules/upd", nil)
	if !strings.Contains(w2.Body.String(), "rate(10 minutes)") {
		t.Error("expected updated schedule expression in response")
	}
}

func TestUpdateSchedule_NotFound(t *testing.T) {
	s := newSvc()
	body := map[string]interface{}{
		"ScheduleExpression": "rate(1 minute)",
		"FlexibleTimeWindow": map[string]string{"Mode": "OFF"},
	}
	w := doReq(t, s, http.MethodPut, "/schedules/nope", body)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestDeleteSchedule(t *testing.T) {
	s := newSvc()
	createSchedule(t, s, "del", "default")

	w := doReq(t, s, http.MethodDelete, "/schedules/del", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	w2 := doReq(t, s, http.MethodGet, "/schedules/del", nil)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", w2.Code)
	}
}

func TestDeleteSchedule_WithGroupParam(t *testing.T) {
	s := newSvc()
	createGroup(t, s, "grpdel")
	createSchedule(t, s, "s3", "grpdel")

	w := doReq(t, s, http.MethodDelete, "/schedules/s3?groupName=grpdel", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestDeleteSchedule_NotFound(t *testing.T) {
	s := newSvc()
	w := doReq(t, s, http.MethodDelete, "/schedules/nope", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestListSchedules(t *testing.T) {
	s := newSvc()
	createSchedule(t, s, "s-a", "default")
	createSchedule(t, s, "s-b", "default")

	w := doReq(t, s, http.MethodGet, "/schedules", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "s-a") || !strings.Contains(body, "s-b") {
		t.Error("expected both schedules in response")
	}
}

func TestListSchedules_GroupFilter(t *testing.T) {
	s := newSvc()
	createGroup(t, s, "prod")
	createSchedule(t, s, "s-prod", "prod")
	createSchedule(t, s, "s-default", "default")

	w := doReq(t, s, http.MethodGet, "/schedules?ScheduleGroup=prod", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "s-prod") {
		t.Error("expected s-prod in filtered response")
	}
	if strings.Contains(body, "s-default") {
		t.Error("expected s-default to be filtered out")
	}
}

func TestListSchedules_NamePrefix(t *testing.T) {
	s := newSvc()
	createSchedule(t, s, "alpha-one", "default")
	createSchedule(t, s, "beta-two", "default")

	w := doReq(t, s, http.MethodGet, "/schedules?NamePrefix=alpha", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "alpha-one") {
		t.Error("expected alpha-one in filtered response")
	}
	if strings.Contains(body, "beta-two") {
		t.Error("expected beta-two to be filtered out")
	}
}

func TestSchedule_MethodNotAllowed(t *testing.T) {
	s := newSvc()
	w := doReq(t, s, http.MethodPatch, "/schedules/s", nil)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

// --- Tags ---

func TestListTagsForResource(t *testing.T) {
	s := newSvc()
	// Create a group with tags.
	doReq(t, s, http.MethodPost, "/schedule-groups/tagged", map[string]interface{}{
		"Tags": []map[string]string{{"Key": "env", "Value": "dev"}},
	})

	// Look up tags by the group's ARN.
	w := doReq(t, s, http.MethodGet, "/schedule-groups/tagged", nil)
	var groupResp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&groupResp)
	arn, _ := groupResp["Arn"].(string)

	wt := doReq(t, s, http.MethodGet, "/tags/"+arn, nil)
	if wt.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", wt.Code)
	}
	if !strings.Contains(wt.Body.String(), "env") {
		t.Error("expected tag key 'env' in response")
	}
}

func TestTagResource(t *testing.T) {
	s := newSvc()
	arn := "arn:aws:scheduler:us-east-1:000000000000:schedule-group/default"
	w := doReq(t, s, http.MethodPost, "/tags/"+arn, map[string]interface{}{
		"Tags": []map[string]string{{"Key": "k", "Value": "v"}},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestUntagResource(t *testing.T) {
	s := newSvc()
	arn := "arn:aws:scheduler:us-east-1:000000000000:schedule-group/default"
	w := doReq(t, s, http.MethodDelete, "/tags/"+arn, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestTags_MethodNotAllowed(t *testing.T) {
	s := newSvc()
	arn := "arn:aws:scheduler:us-east-1:000000000000:schedule-group/default"
	w := doReq(t, s, http.MethodPut, "/tags/"+arn, nil)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

// --- Unknown path ---

func TestUnknownPath(t *testing.T) {
	s := newSvc()
	w := doReq(t, s, http.MethodGet, "/unknown-resource", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// --- SchedulesHandler ---

func TestSchedulesHandler(t *testing.T) {
	s := newSvc()
	createSchedule(t, s, "inspect-me", "default")

	req := httptest.NewRequest(http.MethodGet, "/_nimbus/scheduler/schedules", nil)
	w := httptest.NewRecorder()
	s.SchedulesHandler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "inspect-me") {
		t.Error("expected schedule name in inspection response")
	}
}
