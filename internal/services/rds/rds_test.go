package rds

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func post(t *testing.T, s *Service, form url.Values) string {
	t.Helper()
	req := httptest.NewRequest("POST", "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec.Body.String()
}

func TestCreateDBInstance_PerformanceInsights(t *testing.T) {
	s := New("us-east-1", "localhost", 5432)

	body := post(t, s, url.Values{
		"Action":                    {"CreateDBInstance"},
		"DBInstanceIdentifier":      {"pi-test"},
		"Engine":                    {"aurora-postgresql"},
		"DBInstanceClass":           {"db.serverless"},
		"EnablePerformanceInsights": {"true"},
	})
	if !strings.Contains(body, "<PerformanceInsightsEnabled>true</PerformanceInsightsEnabled>") {
		t.Fatalf("create response missing PerformanceInsightsEnabled=true:\n%s", body)
	}
	if !strings.Contains(body, "<PerformanceInsightsRetentionPeriod>7</PerformanceInsightsRetentionPeriod>") {
		t.Fatalf("create response missing default retention period 7:\n%s", body)
	}
	if !strings.Contains(body, "<DbiResourceId>db-") {
		t.Fatalf("create response missing DbiResourceId:\n%s", body)
	}

	// Describe must round-trip the same values.
	body = post(t, s, url.Values{
		"Action":               {"DescribeDBInstances"},
		"DBInstanceIdentifier": {"pi-test"},
	})
	if !strings.Contains(body, "<PerformanceInsightsEnabled>true</PerformanceInsightsEnabled>") {
		t.Fatalf("describe response missing PerformanceInsightsEnabled=true:\n%s", body)
	}

	// Modify without PI fields must not reset PI state.
	body = post(t, s, url.Values{
		"Action":               {"ModifyDBInstance"},
		"DBInstanceIdentifier": {"pi-test"},
	})
	if !strings.Contains(body, "<PerformanceInsightsEnabled>true</PerformanceInsightsEnabled>") {
		t.Fatalf("modify without PI fields reset PI state:\n%s", body)
	}

	// Modify can disable PI.
	body = post(t, s, url.Values{
		"Action":                    {"ModifyDBInstance"},
		"DBInstanceIdentifier":      {"pi-test"},
		"EnablePerformanceInsights": {"false"},
	})
	if !strings.Contains(body, "<PerformanceInsightsEnabled>false</PerformanceInsightsEnabled>") {
		t.Fatalf("modify failed to disable PI:\n%s", body)
	}
}

func TestCreateDBInstance_PerformanceInsightsDisabledByDefault(t *testing.T) {
	s := New("us-east-1", "localhost", 5432)
	body := post(t, s, url.Values{
		"Action":               {"CreateDBInstance"},
		"DBInstanceIdentifier": {"plain"},
		"Engine":               {"aurora-postgresql"},
		"DBInstanceClass":      {"db.serverless"},
	})
	if !strings.Contains(body, "<PerformanceInsightsEnabled>false</PerformanceInsightsEnabled>") {
		t.Fatalf("expected PerformanceInsightsEnabled=false by default:\n%s", body)
	}
	if strings.Contains(body, "<PerformanceInsightsRetentionPeriod>") {
		t.Fatalf("retention period must be omitted when PI is disabled:\n%s", body)
	}
}

func TestCreateDBCluster_PerformanceInsights(t *testing.T) {
	s := New("us-east-1", "localhost", 5432)
	body := post(t, s, url.Values{
		"Action":                             {"CreateDBCluster"},
		"DBClusterIdentifier":                {"pi-cluster"},
		"Engine":                             {"aurora-postgresql"},
		"EnablePerformanceInsights":          {"true"},
		"PerformanceInsightsKMSKeyId":        {"arn:aws:kms:us-east-1:000000000000:key/test"},
		"PerformanceInsightsRetentionPeriod": {"31"},
	})
	if !strings.Contains(body, "<PerformanceInsightsEnabled>true</PerformanceInsightsEnabled>") {
		t.Fatalf("cluster create missing PerformanceInsightsEnabled=true:\n%s", body)
	}
	if !strings.Contains(body, "<PerformanceInsightsKMSKeyId>arn:aws:kms:us-east-1:000000000000:key/test</PerformanceInsightsKMSKeyId>") {
		t.Fatalf("cluster create missing KMS key:\n%s", body)
	}
	if !strings.Contains(body, "<PerformanceInsightsRetentionPeriod>31</PerformanceInsightsRetentionPeriod>") {
		t.Fatalf("cluster create missing explicit retention period:\n%s", body)
	}
	if !strings.Contains(body, "<DbClusterResourceId>cluster-") {
		t.Fatalf("cluster create missing DbClusterResourceId:\n%s", body)
	}
}
