package rds

import (
	"fmt"
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

func TestDescribeDBInstances_Filters(t *testing.T) {
	s := New("us-east-1", "localhost", 5432)
	post(t, s, url.Values{
		"Action":              {"CreateDBCluster"},
		"DBClusterIdentifier": {"filter-cluster"},
		"Engine":              {"aurora-postgresql"},
	})
	post(t, s, url.Values{
		"Action":               {"CreateDBInstance"},
		"DBInstanceIdentifier": {"member-1"},
		"DBClusterIdentifier":  {"filter-cluster"},
		"Engine":               {"aurora-postgresql"},
		"DBInstanceClass":      {"db.serverless"},
	})
	post(t, s, url.Values{
		"Action":               {"CreateDBInstance"},
		"DBInstanceIdentifier": {"standalone-1"},
		"Engine":               {"postgres"},
		"DBInstanceClass":      {"db.t3.micro"},
	})

	describe := func(filterName string, values ...string) string {
		t.Helper()
		form := url.Values{"Action": {"DescribeDBInstances"}}
		if filterName != "" {
			form.Set("Filters.Filter.1.Name", filterName)
			for i, v := range values {
				form.Set(fmt.Sprintf("Filters.Filter.1.Values.Value.%d", i+1), v)
			}
		}
		return post(t, s, form)
	}

	// db-instance-id by identifier returns exactly one instance.
	body := describe("db-instance-id", "standalone-1")
	if !strings.Contains(body, "<DBInstanceIdentifier>standalone-1</DBInstanceIdentifier>") ||
		strings.Contains(body, "member-1") {
		t.Fatalf("db-instance-id by name: wrong result:\n%s", body)
	}

	// db-instance-id by ARN (the TF provider passes either form).
	body = describe("db-instance-id", "arn:aws:rds:us-east-1:000000000000:db:member-1")
	if !strings.Contains(body, "<DBInstanceIdentifier>member-1</DBInstanceIdentifier>") ||
		strings.Contains(body, "standalone-1") {
		t.Fatalf("db-instance-id by ARN: wrong result:\n%s", body)
	}

	// db-cluster-id matches only the cluster member.
	body = describe("db-cluster-id", "filter-cluster")
	if !strings.Contains(body, "member-1") || strings.Contains(body, "standalone-1") {
		t.Fatalf("db-cluster-id: wrong result:\n%s", body)
	}

	// No match returns an empty list, not an error.
	body = describe("db-instance-id", "no-such-instance")
	if !strings.Contains(body, "<DBInstances></DBInstances>") {
		t.Fatalf("no-match filter must return empty list:\n%s", body)
	}

	// Filter combined with the identifier param: both must hold.
	form := url.Values{
		"Action":                          {"DescribeDBInstances"},
		"DBInstanceIdentifier":            {"standalone-1"},
		"Filters.Filter.1.Name":           {"db-instance-id"},
		"Filters.Filter.1.Values.Value.1": {"member-1"},
	}
	body = post(t, s, form)
	if !strings.Contains(body, "<DBInstances></DBInstances>") {
		t.Fatalf("conflicting identifier+filter must return empty list:\n%s", body)
	}
}

func TestDescribeDBClusters_Filters(t *testing.T) {
	s := New("us-east-1", "localhost", 5432)
	post(t, s, url.Values{
		"Action":              {"CreateDBCluster"},
		"DBClusterIdentifier": {"cluster-a"},
		"Engine":              {"aurora-postgresql"},
	})
	post(t, s, url.Values{
		"Action":              {"CreateDBCluster"},
		"DBClusterIdentifier": {"cluster-b"},
		"Engine":              {"aurora-postgresql"},
	})

	body := post(t, s, url.Values{
		"Action":                          {"DescribeDBClusters"},
		"Filters.Filter.1.Name":           {"db-cluster-id"},
		"Filters.Filter.1.Values.Value.1": {"arn:aws:rds:us-east-1:000000000000:cluster:cluster-b"},
	})
	if !strings.Contains(body, "cluster-b") || strings.Contains(body, "cluster-a<") {
		t.Fatalf("db-cluster-id ARN filter: wrong result:\n%s", body)
	}
}

func TestCreateDBInstance_StandaloneFields(t *testing.T) {
	s := New("us-east-1", "localhost", 5432)
	body := post(t, s, url.Values{
		"Action":               {"CreateDBInstance"},
		"DBInstanceIdentifier": {"legacy-db"},
		"Engine":               {"postgres"},
		"EngineVersion":        {"16.1"},
		"DBInstanceClass":      {"db.t3.micro"},
		"MasterUsername":       {"nimbus"},
		"DBName":               {"legacy"},
		"AllocatedStorage":     {"20"},
	})
	for _, want := range []string{
		"<EngineVersion>16.1</EngineVersion>",
		"<MasterUsername>nimbus</MasterUsername>",
		"<DBName>legacy</DBName>",
		"<AllocatedStorage>20</AllocatedStorage>",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("create response missing %s:\n%s", want, body)
		}
	}
}
