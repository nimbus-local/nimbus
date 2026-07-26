package cloudwatchlogs

import (
	"net/http"
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
