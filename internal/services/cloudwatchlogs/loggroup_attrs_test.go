package cloudwatchlogs

import (
	"net/http"
	"strings"
	"testing"
)

// describeGroup returns the DescribeLogGroups entry for name, failing if absent.
func describeGroup(t *testing.T, svc *Service, name string) map[string]interface{} {
	t.Helper()
	w := cwlReq(t, svc, "DescribeLogGroups", map[string]interface{}{})
	if w.Code != http.StatusOK {
		t.Fatalf("DescribeLogGroups: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	groups, ok := decode(t, w)["logGroups"].([]interface{})
	if !ok {
		t.Fatalf("DescribeLogGroups: expected a logGroups array, got %s", w.Body.String())
	}
	for _, g := range groups {
		entry, ok := g.(map[string]interface{})
		if ok && entry["logGroupName"] == name {
			return entry
		}
	}
	t.Fatalf("DescribeLogGroups: log group %q not found", name)
	return nil
}

func TestCreateLogGroup_KmsKeyIdReadsBack(t *testing.T) {
	svc := newSvc()
	const key = "arn:aws:kms:us-east-1:000000000000:key/abcd-1234"

	cwlReq(t, svc, "CreateLogGroup", map[string]interface{}{
		"logGroupName": "/aws/lambda/encrypted",
		"kmsKeyId":     key,
	})

	if got := describeGroup(t, svc, "/aws/lambda/encrypted")["kmsKeyId"]; got != key {
		t.Errorf("kmsKeyId: expected %q, got %v", key, got)
	}
}

func TestDescribeLogGroups_ReportsRetention(t *testing.T) {
	svc := newSvc()
	cwlReq(t, svc, "CreateLogGroup", map[string]interface{}{"logGroupName": "/aws/lambda/retained"})

	// Retention is set after creation, so it only reads back if DescribeLogGroups
	// reflects post-creation state rather than the creation request.
	cwlReq(t, svc, "PutRetentionPolicy", map[string]interface{}{
		"logGroupName":    "/aws/lambda/retained",
		"retentionInDays": 14,
	})

	if got := describeGroup(t, svc, "/aws/lambda/retained")["retentionInDays"]; got != float64(14) {
		t.Errorf("retentionInDays: expected 14, got %v", got)
	}
}

func TestDescribeLogGroups_OmitsUnsetAttributes(t *testing.T) {
	svc := newSvc()
	cwlReq(t, svc, "CreateLogGroup", map[string]interface{}{"logGroupName": "/aws/lambda/plain"})

	entry := describeGroup(t, svc, "/aws/lambda/plain")
	if _, present := entry["retentionInDays"]; present {
		t.Errorf("retentionInDays: expected omitted when unset, got %v", entry["retentionInDays"])
	}
	if _, present := entry["kmsKeyId"]; present {
		t.Errorf("kmsKeyId: expected omitted when unset, got %v", entry["kmsKeyId"])
	}
}

func TestAssociateAndDisassociateKmsKey(t *testing.T) {
	svc := newSvc()
	const key = "arn:aws:kms:us-east-1:000000000000:key/abcd-1234"
	cwlReq(t, svc, "CreateLogGroup", map[string]interface{}{"logGroupName": "/aws/lambda/rekeyed"})

	w := cwlReq(t, svc, "AssociateKmsKey", map[string]interface{}{
		"logGroupName": "/aws/lambda/rekeyed",
		"kmsKeyId":     key,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("AssociateKmsKey: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	if got := describeGroup(t, svc, "/aws/lambda/rekeyed")["kmsKeyId"]; got != key {
		t.Errorf("kmsKeyId after associate: expected %q, got %v", key, got)
	}

	w = cwlReq(t, svc, "DisassociateKmsKey", map[string]interface{}{
		"logGroupName": "/aws/lambda/rekeyed",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("DisassociateKmsKey: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	if _, present := describeGroup(t, svc, "/aws/lambda/rekeyed")["kmsKeyId"]; present {
		t.Error("kmsKeyId: expected omitted after disassociate")
	}
}

func TestAssociateKmsKey_GroupNotFound(t *testing.T) {
	svc := newSvc()
	w := cwlReq(t, svc, "AssociateKmsKey", map[string]interface{}{
		"logGroupName": "/aws/lambda/missing",
		"kmsKeyId":     "arn:aws:kms:us-east-1:000000000000:key/abcd-1234",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d\n%s", w.Code, w.Body.String())
	}
	if got := decode(t, w)["__type"]; got != "ResourceNotFoundException" {
		t.Errorf("__type: expected ResourceNotFoundException, got %v", got)
	}
}

// ── Ingest (used by services forwarding container output) ─────────────────────

// AWS delivers a workload's output without anyone calling CreateLogGroup, so
// the forwarding path must create the group and stream on demand.
func TestIngest_CreatesGroupAndStreamOnDemand(t *testing.T) {
	svc := newSvc()
	svc.Ingest("/aws/lambda/fn", "2024/01/01/[$LATEST]abc", []string{"first", "second"})

	entry := describeGroup(t, svc, "/aws/lambda/fn")
	if entry["logGroupName"] != "/aws/lambda/fn" {
		t.Fatalf("expected the group to be created, got %v", entry)
	}

	w := cwlReq(t, svc, "GetLogEvents", map[string]interface{}{
		"logGroupName":  "/aws/lambda/fn",
		"logStreamName": "2024/01/01/[$LATEST]abc",
	})
	body := w.Body.String()
	for _, want := range []string{"first", "second"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected the stream to hold %q, got %s", want, body)
		}
	}
}

func TestIngest_AppendsToAnExistingStream(t *testing.T) {
	svc := newSvc()
	svc.Ingest("/aws/lambda/fn", "s", []string{"one"})
	svc.Ingest("/aws/lambda/fn", "s", []string{"two"})

	w := cwlReq(t, svc, "GetLogEvents", map[string]interface{}{
		"logGroupName":  "/aws/lambda/fn",
		"logStreamName": "s",
	})
	body := w.Body.String()
	if !strings.Contains(body, "one") || !strings.Contains(body, "two") {
		t.Errorf("expected both batches in the stream, got %s", body)
	}
}

// Ingest is called from a log pump that may flush an empty batch.
func TestIngest_IgnoresEmptyInput(t *testing.T) {
	svc := newSvc()
	svc.Ingest("/aws/lambda/fn", "s", nil)
	svc.Ingest("", "s", []string{"x"})
	svc.Ingest("/aws/lambda/fn", "", []string{"x"})

	w := cwlReq(t, svc, "DescribeLogGroups", map[string]interface{}{})
	if strings.Contains(w.Body.String(), "/aws/lambda/fn") {
		t.Errorf("expected no group to be created for empty input, got %s", w.Body.String())
	}
}

// A container that logs forever must not grow the stream without bound.
func TestIngest_CapsStreamLength(t *testing.T) {
	svc := newSvc()
	batch := make([]string, maxEvents+50)
	for i := range batch {
		batch[i] = "line"
	}
	svc.Ingest("/aws/lambda/fn", "s", batch)

	svc.mu.RLock()
	got := len(svc.groups["/aws/lambda/fn"].streams["s"].events)
	svc.mu.RUnlock()

	if got != maxEvents {
		t.Errorf("expected the stream capped at %d events, got %d", maxEvents, got)
	}
}
