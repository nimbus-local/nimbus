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

// createSubnetGroup creates a subnet group holding two subnets.
func createSubnetGroup(t *testing.T, s *Service, name string, subnetIDs ...string) string {
	t.Helper()
	form := url.Values{
		"Action":                   {"CreateDBSubnetGroup"},
		"DBSubnetGroupName":        {name},
		"DBSubnetGroupDescription": {"test"},
	}
	for i, id := range subnetIDs {
		form.Set(fmt.Sprintf("SubnetIds.SubnetIdentifier.%d", i+1), id)
	}
	return post(t, s, form)
}

func TestDBSubnetGroup_SubnetsRoundTrip(t *testing.T) {
	s := New("us-east-1", "localhost", 5432)
	s.SetSubnetInfo(func(id string) (string, string, bool) {
		if id == "subnet-a" {
			return "vpc-real", "us-east-1c", true
		}
		return "", "", false
	})

	body := createSubnetGroup(t, s, "group-1", "subnet-a", "subnet-b")
	for _, want := range []string{
		"<SubnetIdentifier>subnet-a</SubnetIdentifier>",
		"<SubnetIdentifier>subnet-b</SubnetIdentifier>",
		"<Name>us-east-1c</Name>", // resolved through the EC2 lookup
		"<VpcId>vpc-real</VpcId>",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("create subnet group response missing %s:\n%s", want, body)
		}
	}

	// Describe must report the same subnet list — the provider reads
	// subnet_ids back from here.
	body = post(t, s, url.Values{
		"Action":            {"DescribeDBSubnetGroups"},
		"DBSubnetGroupName": {"group-1"},
	})
	if !strings.Contains(body, "<SubnetIdentifier>subnet-b</SubnetIdentifier>") {
		t.Fatalf("describe subnet group dropped the subnet list:\n%s", body)
	}

	// A subnet replace re-points the group.
	post(t, s, url.Values{
		"Action":                       {"ModifyDBSubnetGroup"},
		"DBSubnetGroupName":            {"group-1"},
		"SubnetIds.SubnetIdentifier.1": {"subnet-c"},
		"SubnetIds.SubnetIdentifier.2": {"subnet-d"},
	})
	body = post(t, s, url.Values{
		"Action":            {"DescribeDBSubnetGroups"},
		"DBSubnetGroupName": {"group-1"},
	})
	if strings.Contains(body, "subnet-a") || !strings.Contains(body, "subnet-c") {
		t.Fatalf("modify did not replace the subnet list:\n%s", body)
	}
}

func TestCreateDB_SubnetGroupAssociation(t *testing.T) {
	s := New("us-east-1", "localhost", 5432)
	createSubnetGroup(t, s, "group-1", "subnet-a", "subnet-b")

	// Cluster reports the subnet group as a bare name.
	body := post(t, s, url.Values{
		"Action":              {"CreateDBCluster"},
		"DBClusterIdentifier": {"cluster-1"},
		"Engine":              {"aurora-postgresql"},
		"DBSubnetGroupName":   {"group-1"},
	})
	if !strings.Contains(body, "<DBSubnetGroup>group-1</DBSubnetGroup>") {
		t.Fatalf("cluster create dropped DBSubnetGroupName:\n%s", body)
	}
	body = post(t, s, url.Values{
		"Action":              {"DescribeDBClusters"},
		"DBClusterIdentifier": {"cluster-1"},
	})
	if !strings.Contains(body, "<DBSubnetGroup>group-1</DBSubnetGroup>") {
		t.Fatalf("describe cluster dropped DBSubnetGroupName:\n%s", body)
	}

	// Standalone instance reports the nested structure.
	post(t, s, url.Values{
		"Action":               {"CreateDBInstance"},
		"DBInstanceIdentifier": {"standalone"},
		"Engine":               {"postgres"},
		"DBInstanceClass":      {"db.t3.micro"},
		"DBSubnetGroupName":    {"group-1"},
	})
	body = post(t, s, url.Values{
		"Action":               {"DescribeDBInstances"},
		"DBInstanceIdentifier": {"standalone"},
	})
	if !strings.Contains(body, "<DBSubnetGroupName>group-1</DBSubnetGroupName>") {
		t.Fatalf("describe instance dropped the subnet group:\n%s", body)
	}

	// A cluster member inherits the cluster's subnet group without passing
	// DBSubnetGroupName itself.
	post(t, s, url.Values{
		"Action":               {"CreateDBInstance"},
		"DBInstanceIdentifier": {"member-1"},
		"DBClusterIdentifier":  {"cluster-1"},
		"Engine":               {"aurora-postgresql"},
		"DBInstanceClass":      {"db.serverless"},
	})
	body = post(t, s, url.Values{
		"Action":               {"DescribeDBInstances"},
		"DBInstanceIdentifier": {"member-1"},
	})
	if !strings.Contains(body, "<DBSubnetGroupName>group-1</DBSubnetGroupName>") {
		t.Fatalf("cluster member did not inherit the subnet group:\n%s", body)
	}
}

func TestCreateDB_UnknownSubnetGroupRejected(t *testing.T) {
	s := New("us-east-1", "localhost", 5432)
	for _, action := range []string{"CreateDBCluster", "CreateDBInstance"} {
		body := post(t, s, url.Values{
			"Action":               {action},
			"DBClusterIdentifier":  {"c"},
			"DBInstanceIdentifier": {"i"},
			"Engine":               {"postgres"},
			"DBInstanceClass":      {"db.t3.micro"},
			"DBSubnetGroupName":    {"missing"},
		})
		if !strings.Contains(body, "<Code>DBSubnetGroupNotFoundFault</Code>") {
			t.Fatalf("%s accepted an unknown subnet group:\n%s", action, body)
		}
	}
}

func TestSubnetInUse(t *testing.T) {
	s := New("us-east-1", "localhost", 5432)
	createSubnetGroup(t, s, "group-1", "subnet-a", "subnet-b")
	createSubnetGroup(t, s, "unused-group", "subnet-z")

	if _, inUse := s.SubnetInUse("subnet-a"); inUse {
		t.Fatal("an empty subnet group must not hold subnets hostage")
	}

	post(t, s, url.Values{
		"Action":               {"CreateDBInstance"},
		"DBInstanceIdentifier": {"standalone"},
		"Engine":               {"postgres"},
		"DBInstanceClass":      {"db.t3.micro"},
		"DBSubnetGroupName":    {"group-1"},
	})

	user, inUse := s.SubnetInUse("subnet-a")
	if !inUse || !strings.Contains(user, "standalone") {
		t.Fatalf("SubnetInUse(subnet-a) = %q, %v; want the instance identifier, true", user, inUse)
	}
	if _, inUse := s.SubnetInUse("subnet-z"); inUse {
		t.Error("subnet-z belongs to a group nothing uses")
	}

	// The subnet group cannot be dropped while the instance sits in it.
	body := post(t, s, url.Values{
		"Action":            {"DeleteDBSubnetGroup"},
		"DBSubnetGroupName": {"group-1"},
	})
	if !strings.Contains(body, "<Code>InvalidDBSubnetGroupStateFault</Code>") {
		t.Fatalf("delete of an in-use subnet group must be rejected:\n%s", body)
	}

	// Deleting the instance releases both.
	post(t, s, url.Values{
		"Action":               {"DeleteDBInstance"},
		"DBInstanceIdentifier": {"standalone"},
	})
	if _, inUse := s.SubnetInUse("subnet-a"); inUse {
		t.Error("subnet must be released once the instance is deleted")
	}
	body = post(t, s, url.Values{
		"Action":            {"DeleteDBSubnetGroup"},
		"DBSubnetGroupName": {"group-1"},
	})
	if strings.Contains(body, "<Code>") {
		t.Fatalf("delete of a released subnet group must succeed:\n%s", body)
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
