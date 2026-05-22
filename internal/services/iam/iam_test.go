package iam

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// iamReq sends a form-encoded IAM/STS request and returns the recorder.
func iamReq(t *testing.T, svc *Service, action string, params map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"Action": {action}, "Version": {"2010-05-08"}}
	for k, v := range params {
		form.Set(k, v)
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	return w
}

func stsReq(t *testing.T, svc *Service, action string, params map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"Action": {action}, "Version": {"2011-06-15"}}
	for k, v := range params {
		form.Set(k, v)
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	return w
}

func assertStatus(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	if w.Code != want {
		t.Fatalf("expected status %d, got %d\nbody: %s", want, w.Code, w.Body.String())
	}
}

func assertContains(t *testing.T, w *httptest.ResponseRecorder, substr string) {
	t.Helper()
	if !strings.Contains(w.Body.String(), substr) {
		t.Fatalf("expected body to contain %q\nbody: %s", substr, w.Body.String())
	}
}

func assertXMLValid(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if err := xml.Unmarshal(w.Body.Bytes(), new(interface{})); err != nil {
		t.Fatalf("response is not valid XML: %v\nbody: %s", err, w.Body.String())
	}
}

// --- Detect ---

func TestDetect(t *testing.T) {
	svc := New()
	form := url.Values{"Action": {"CreateRole"}, "Version": {"2010-05-08"}, "RoleName": {"r"}}.Encode()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if !svc.Detect(req) {
		t.Fatal("Detect should return true for IAM form-encoded request")
	}
}

func TestDetect_STSVersion(t *testing.T) {
	svc := New()
	form := url.Values{"Action": {"GetCallerIdentity"}, "Version": {"2011-06-15"}}.Encode()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if !svc.Detect(req) {
		t.Fatal("Detect should return true for STS form-encoded request")
	}
}

func TestDetect_JSON(t *testing.T) {
	svc := New()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	if svc.Detect(req) {
		t.Fatal("Detect should return false for JSON request")
	}
}

// --- Roles ---

func TestCreateRole(t *testing.T) {
	svc := New()
	w := iamReq(t, svc, "CreateRole", map[string]string{
		"RoleName":                 "my-role",
		"AssumeRolePolicyDocument": `{"Version":"2012-10-17"}`,
	})
	assertStatus(t, w, http.StatusOK)
	assertXMLValid(t, w)
	assertContains(t, w, "my-role")
	assertContains(t, w, "arn:aws:iam::000000000000:role/my-role")
}

func TestCreateRole_Duplicate(t *testing.T) {
	svc := New()
	iamReq(t, svc, "CreateRole", map[string]string{"RoleName": "r"})
	w := iamReq(t, svc, "CreateRole", map[string]string{"RoleName": "r"})
	assertStatus(t, w, http.StatusConflict)
	assertContains(t, w, "EntityAlreadyExists")
}

func TestCreateRole_MissingName(t *testing.T) {
	svc := New()
	w := iamReq(t, svc, "CreateRole", nil)
	assertStatus(t, w, http.StatusBadRequest)
}

func TestGetRole(t *testing.T) {
	svc := New()
	iamReq(t, svc, "CreateRole", map[string]string{"RoleName": "my-role"})
	w := iamReq(t, svc, "GetRole", map[string]string{"RoleName": "my-role"})
	assertStatus(t, w, http.StatusOK)
	assertContains(t, w, "my-role")
}

func TestGetRole_NotFound(t *testing.T) {
	svc := New()
	w := iamReq(t, svc, "GetRole", map[string]string{"RoleName": "missing"})
	assertStatus(t, w, http.StatusNotFound)
	assertContains(t, w, "NoSuchEntity")
}

func TestDeleteRole(t *testing.T) {
	svc := New()
	iamReq(t, svc, "CreateRole", map[string]string{"RoleName": "r"})
	w := iamReq(t, svc, "DeleteRole", map[string]string{"RoleName": "r"})
	assertStatus(t, w, http.StatusOK)
	w2 := iamReq(t, svc, "GetRole", map[string]string{"RoleName": "r"})
	assertStatus(t, w2, http.StatusNotFound)
}

func TestListRoles(t *testing.T) {
	svc := New()
	iamReq(t, svc, "CreateRole", map[string]string{"RoleName": "role-a"})
	iamReq(t, svc, "CreateRole", map[string]string{"RoleName": "role-b"})
	w := iamReq(t, svc, "ListRoles", nil)
	assertStatus(t, w, http.StatusOK)
	assertContains(t, w, "role-a")
	assertContains(t, w, "role-b")
	assertContains(t, w, "IsTruncated>false")
}

func TestUpdateRole(t *testing.T) {
	svc := New()
	iamReq(t, svc, "CreateRole", map[string]string{"RoleName": "r"})
	w := iamReq(t, svc, "UpdateRole", map[string]string{
		"RoleName":    "r",
		"Description": "updated",
	})
	assertStatus(t, w, http.StatusOK)
}

func TestTagUntagListRoleTags(t *testing.T) {
	svc := New()
	iamReq(t, svc, "CreateRole", map[string]string{"RoleName": "r"})

	iamReq(t, svc, "TagRole", map[string]string{
		"RoleName":            "r",
		"Tags.member.1.Key":   "env",
		"Tags.member.1.Value": "dev",
	})
	w := iamReq(t, svc, "ListRoleTags", map[string]string{"RoleName": "r"})
	assertStatus(t, w, http.StatusOK)
	assertContains(t, w, "env")
	assertContains(t, w, "dev")

	iamReq(t, svc, "UntagRole", map[string]string{
		"RoleName":         "r",
		"TagKeys.member.1": "env",
	})
	w2 := iamReq(t, svc, "ListRoleTags", map[string]string{"RoleName": "r"})
	if strings.Contains(w2.Body.String(), "env") {
		t.Fatal("tag should have been removed")
	}
}

// --- Policy attachments ---

func TestAttachDetachListRolePolicy(t *testing.T) {
	svc := New()
	iamReq(t, svc, "CreateRole", map[string]string{"RoleName": "r"})

	policyArn := "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
	iamReq(t, svc, "AttachRolePolicy", map[string]string{
		"RoleName":  "r",
		"PolicyArn": policyArn,
	})

	w := iamReq(t, svc, "ListAttachedRolePolicies", map[string]string{"RoleName": "r"})
	assertStatus(t, w, http.StatusOK)
	assertContains(t, w, "AmazonECSTaskExecutionRolePolicy")

	iamReq(t, svc, "DetachRolePolicy", map[string]string{
		"RoleName":  "r",
		"PolicyArn": policyArn,
	})
	w2 := iamReq(t, svc, "ListAttachedRolePolicies", map[string]string{"RoleName": "r"})
	if strings.Contains(w2.Body.String(), "AmazonECSTaskExecutionRolePolicy") {
		t.Fatal("policy should have been detached")
	}
}

func TestAttachRolePolicy_IdempotentOnDuplicate(t *testing.T) {
	svc := New()
	iamReq(t, svc, "CreateRole", map[string]string{"RoleName": "r"})
	p := map[string]string{"RoleName": "r", "PolicyArn": "arn:aws:iam::aws:policy/ReadOnly"}
	iamReq(t, svc, "AttachRolePolicy", p)
	iamReq(t, svc, "AttachRolePolicy", p)

	w := iamReq(t, svc, "ListAttachedRolePolicies", map[string]string{"RoleName": "r"})
	// Each attachment produces one <member> element; count those.
	count := strings.Count(w.Body.String(), "<member>")
	if count != 1 {
		t.Fatalf("expected 1 attachment member, found %d\nbody: %s", count, w.Body.String())
	}
}

// --- Managed policies ---

func TestCreateGetDeletePolicy(t *testing.T) {
	svc := New()
	w := iamReq(t, svc, "CreatePolicy", map[string]string{
		"PolicyName":     "my-policy",
		"PolicyDocument": `{"Version":"2012-10-17","Statement":[]}`,
	})
	assertStatus(t, w, http.StatusOK)
	assertContains(t, w, "my-policy")
	arn := "arn:aws:iam::000000000000:policy/my-policy"
	assertContains(t, w, arn)

	wg := iamReq(t, svc, "GetPolicy", map[string]string{"PolicyArn": arn})
	assertStatus(t, wg, http.StatusOK)
	assertContains(t, wg, "my-policy")

	iamReq(t, svc, "DeletePolicy", map[string]string{"PolicyArn": arn})
	wd := iamReq(t, svc, "GetPolicy", map[string]string{"PolicyArn": arn})
	assertStatus(t, wd, http.StatusNotFound)
}

func TestGetPolicy_AWSManaged(t *testing.T) {
	svc := New()
	w := iamReq(t, svc, "GetPolicy", map[string]string{
		"PolicyArn": "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy",
	})
	assertStatus(t, w, http.StatusOK)
	assertContains(t, w, "AmazonECSTaskExecutionRolePolicy")
}

func TestGetPolicyVersion(t *testing.T) {
	svc := New()
	iamReq(t, svc, "CreatePolicy", map[string]string{
		"PolicyName":     "p",
		"PolicyDocument": `{"Version":"2012-10-17"}`,
	})
	w := iamReq(t, svc, "GetPolicyVersion", map[string]string{
		"PolicyArn": "arn:aws:iam::000000000000:policy/p",
		"VersionId": "v1",
	})
	assertStatus(t, w, http.StatusOK)
	assertContains(t, w, "v1")
	assertContains(t, w, "IsDefaultVersion>true")
}

func TestListPolicies(t *testing.T) {
	svc := New()
	iamReq(t, svc, "CreatePolicy", map[string]string{"PolicyName": "p1", "PolicyDocument": "{}"})
	iamReq(t, svc, "CreatePolicy", map[string]string{"PolicyName": "p2", "PolicyDocument": "{}"})
	w := iamReq(t, svc, "ListPolicies", nil)
	assertStatus(t, w, http.StatusOK)
	assertContains(t, w, "p1")
	assertContains(t, w, "p2")
}

func TestListPolicies_AWSScopeReturnsEmpty(t *testing.T) {
	svc := New()
	iamReq(t, svc, "CreatePolicy", map[string]string{"PolicyName": "local", "PolicyDocument": "{}"})
	w := iamReq(t, svc, "ListPolicies", map[string]string{"Scope": "AWS"})
	assertStatus(t, w, http.StatusOK)
	if strings.Contains(w.Body.String(), "local") {
		t.Fatal("AWS scope should not return local policies")
	}
}

// --- Inline policies ---

func TestPutGetDeleteRolePolicy(t *testing.T) {
	svc := New()
	iamReq(t, svc, "CreateRole", map[string]string{"RoleName": "r"})

	iamReq(t, svc, "PutRolePolicy", map[string]string{
		"RoleName":       "r",
		"PolicyName":     "inline",
		"PolicyDocument": `{"Version":"2012-10-17"}`,
	})

	w := iamReq(t, svc, "GetRolePolicy", map[string]string{
		"RoleName":   "r",
		"PolicyName": "inline",
	})
	assertStatus(t, w, http.StatusOK)
	assertContains(t, w, "inline")

	wl := iamReq(t, svc, "ListRolePolicies", map[string]string{"RoleName": "r"})
	assertContains(t, wl, "inline")

	iamReq(t, svc, "DeleteRolePolicy", map[string]string{"RoleName": "r", "PolicyName": "inline"})
	wd := iamReq(t, svc, "GetRolePolicy", map[string]string{"RoleName": "r", "PolicyName": "inline"})
	assertStatus(t, wd, http.StatusNotFound)
}

// --- Instance profiles ---

func TestCreateGetDeleteInstanceProfile(t *testing.T) {
	svc := New()
	w := iamReq(t, svc, "CreateInstanceProfile", map[string]string{
		"InstanceProfileName": "my-profile",
	})
	assertStatus(t, w, http.StatusOK)
	assertContains(t, w, "my-profile")
	assertContains(t, w, "arn:aws:iam::000000000000:instance-profile/my-profile")

	wg := iamReq(t, svc, "GetInstanceProfile", map[string]string{
		"InstanceProfileName": "my-profile",
	})
	assertStatus(t, wg, http.StatusOK)

	iamReq(t, svc, "DeleteInstanceProfile", map[string]string{"InstanceProfileName": "my-profile"})
	wd := iamReq(t, svc, "GetInstanceProfile", map[string]string{"InstanceProfileName": "my-profile"})
	assertStatus(t, wd, http.StatusNotFound)
}

func TestAddRemoveRoleFromInstanceProfile(t *testing.T) {
	svc := New()
	iamReq(t, svc, "CreateRole", map[string]string{"RoleName": "r"})
	iamReq(t, svc, "CreateInstanceProfile", map[string]string{"InstanceProfileName": "p"})

	iamReq(t, svc, "AddRoleToInstanceProfile", map[string]string{
		"InstanceProfileName": "p",
		"RoleName":            "r",
	})
	w := iamReq(t, svc, "GetInstanceProfile", map[string]string{"InstanceProfileName": "p"})
	assertContains(t, w, "<RoleName>r</RoleName>")

	iamReq(t, svc, "RemoveRoleFromInstanceProfile", map[string]string{
		"InstanceProfileName": "p",
		"RoleName":            "r",
	})
	w2 := iamReq(t, svc, "GetInstanceProfile", map[string]string{"InstanceProfileName": "p"})
	if strings.Contains(w2.Body.String(), "<RoleName>r</RoleName>") {
		t.Fatal("role should have been removed from profile")
	}
}

func TestListInstanceProfiles(t *testing.T) {
	svc := New()
	iamReq(t, svc, "CreateInstanceProfile", map[string]string{"InstanceProfileName": "p1"})
	iamReq(t, svc, "CreateInstanceProfile", map[string]string{"InstanceProfileName": "p2"})
	w := iamReq(t, svc, "ListInstanceProfiles", nil)
	assertStatus(t, w, http.StatusOK)
	assertContains(t, w, "p1")
	assertContains(t, w, "p2")
}

// --- STS ---

func TestGetCallerIdentity(t *testing.T) {
	svc := New()
	w := stsReq(t, svc, "GetCallerIdentity", nil)
	assertStatus(t, w, http.StatusOK)
	assertContains(t, w, "000000000000")
}

func TestAssumeRole(t *testing.T) {
	svc := New()
	w := stsReq(t, svc, "AssumeRole", map[string]string{
		"RoleArn":         "arn:aws:iam::000000000000:role/my-role",
		"RoleSessionName": "test-session",
	})
	assertStatus(t, w, http.StatusOK)
	assertContains(t, w, "AccessKeyId")
	assertContains(t, w, "SecretAccessKey")
	assertContains(t, w, "SessionToken")
	assertContains(t, w, "test-session")
}

// --- Unknown action ---

func TestUnknownAction(t *testing.T) {
	svc := New()
	w := iamReq(t, svc, "BogusAction", nil)
	assertStatus(t, w, http.StatusBadRequest)
	assertContains(t, w, "InvalidAction")
}
