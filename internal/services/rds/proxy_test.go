package rds

import (
	"net/url"
	"strings"
	"testing"
)

const (
	testSecretArn = "arn:aws:secretsmanager:us-east-1:000000000000:secret:db-creds-AbCdEf"
	testRoleArn   = "arn:aws:iam::000000000000:role/rds-proxy-role"
)

// newProxySvc creates a proxy named "app-proxy" over a cluster with one
// instance, the shape the Terraform fixture builds.
func newProxySvc(t *testing.T) *Service {
	t.Helper()
	s := New("us-east-1", "postgres", 5432)
	post(t, s, url.Values{
		"Action":              {"CreateDBCluster"},
		"DBClusterIdentifier": {"app-db"},
		"Engine":              {"aurora-postgresql"},
	})
	post(t, s, url.Values{
		"Action":               {"CreateDBInstance"},
		"DBInstanceIdentifier": {"app-db-1"},
		"DBClusterIdentifier":  {"app-db"},
		"Engine":               {"aurora-postgresql"},
		"DBInstanceClass":      {"db.serverless"},
	})
	body := post(t, s, createProxyForm())
	if !strings.Contains(body, "<DBProxyName>app-proxy</DBProxyName>") {
		t.Fatalf("CreateDBProxy did not return the proxy:\n%s", body)
	}
	return s
}

func createProxyForm() url.Values {
	return url.Values{
		"Action":                               {"CreateDBProxy"},
		"DBProxyName":                          {"app-proxy"},
		"EngineFamily":                         {"POSTGRESQL"},
		"RoleArn":                              {testRoleArn},
		"VpcSubnetIds.member.1":                {"subnet-1111"},
		"VpcSubnetIds.member.2":                {"subnet-2222"},
		"VpcSecurityGroupIds.member.1":         {"sg-3333"},
		"Auth.member.1.AuthScheme":             {"SECRETS"},
		"Auth.member.1.SecretArn":              {testSecretArn},
		"Auth.member.1.IAMAuth":                {"DISABLED"},
		"Auth.member.1.Description":            {"app credentials"},
		"Auth.member.1.ClientPasswordAuthType": {"POSTGRES_SCRAM_SHA_256"},
		"RequireTLS":                           {"true"},
		"IdleClientTimeout":                    {"900"},
		"DebugLogging":                         {"true"},
		"Tags.Tag.1.Key":                       {"env"},
		"Tags.Tag.1.Value":                     {"smoke"},
	}
}

// --- Create / Describe ---

func TestCreateDBProxy_RoundTrips(t *testing.T) {
	s := newProxySvc(t)

	body := post(t, s, url.Values{"Action": {"DescribeDBProxies"}})
	for _, want := range []string{
		"<DBProxyName>app-proxy</DBProxyName>",
		"<DBProxyArn>arn:aws:rds:us-east-1:000000000000:db-proxy:prx-",
		"<Status>available</Status>",
		"<EngineFamily>POSTGRESQL</EngineFamily>",
		"<RoleArn>" + testRoleArn + "</RoleArn>",
		"<RequireTLS>true</RequireTLS>",
		"<IdleClientTimeout>900</IdleClientTimeout>",
		"<DebugLogging>true</DebugLogging>",
		"<member>subnet-1111</member>",
		"<member>subnet-2222</member>",
		"<member>sg-3333</member>",
		"<SecretArn>" + testSecretArn + "</SecretArn>",
		"<AuthScheme>SECRETS</AuthScheme>",
		"<ClientPasswordAuthType>POSTGRES_SCRAM_SHA_256</ClientPasswordAuthType>",
		"<VpcId>vpc-",
		"<CreatedDate>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("DescribeDBProxies missing %q:\n%s", want, body)
		}
	}
}

// The endpoint must resolve to the Postgres sidecar, like a cluster endpoint —
// an app pointed at the proxy has to reach a real database.
func TestCreateDBProxy_EndpointIsPostgresSidecar(t *testing.T) {
	s := newProxySvc(t)
	body := post(t, s, url.Values{"Action": {"DescribeDBProxies"}})
	if !strings.Contains(body, "<Endpoint>postgres</Endpoint>") {
		t.Errorf("expected the proxy endpoint to be the Postgres host:\n%s", body)
	}
}

// The proxy lists use the query protocol's default `member` element, unlike
// DBInstances/DBSubnetGroups which name their members.
func TestDescribeDBProxies_UsesMemberElements(t *testing.T) {
	s := newProxySvc(t)
	body := post(t, s, url.Values{"Action": {"DescribeDBProxies"}})
	if !strings.Contains(body, "<DBProxies><member>") {
		t.Errorf("DBProxies list should wrap entries in <member>:\n%s", body)
	}
	if strings.Contains(body, "<DBProxies><DBProxy>") {
		t.Errorf("DBProxies must not name its member element:\n%s", body)
	}
}

func TestCreateDBProxy_Duplicate(t *testing.T) {
	s := newProxySvc(t)
	body := post(t, s, createProxyForm())
	if !strings.Contains(body, "DBProxyAlreadyExistsFault") {
		t.Errorf("expected DBProxyAlreadyExistsFault:\n%s", body)
	}
}

func TestCreateDBProxy_RequiresNameAndEngineFamily(t *testing.T) {
	s := New("us-east-1", "postgres", 5432)
	body := post(t, s, url.Values{"Action": {"CreateDBProxy"}, "EngineFamily": {"POSTGRESQL"}})
	if !strings.Contains(body, "InvalidParameterValue") {
		t.Errorf("expected an error without DBProxyName:\n%s", body)
	}
	body = post(t, s, url.Values{"Action": {"CreateDBProxy"}, "DBProxyName": {"p"}})
	if !strings.Contains(body, "InvalidParameterValue") {
		t.Errorf("expected an error without EngineFamily:\n%s", body)
	}
}

func TestDescribeDBProxies_FilterByName(t *testing.T) {
	s := newProxySvc(t)
	post(t, s, url.Values{
		"Action":                {"CreateDBProxy"},
		"DBProxyName":           {"other-proxy"},
		"EngineFamily":          {"MYSQL"},
		"RoleArn":               {testRoleArn},
		"VpcSubnetIds.member.1": {"subnet-1111"},
	})

	body := post(t, s, url.Values{"Action": {"DescribeDBProxies"}, "DBProxyName": {"app-proxy"}})
	if !strings.Contains(body, "app-proxy") {
		t.Errorf("expected app-proxy in the filtered response:\n%s", body)
	}
	if strings.Contains(body, "other-proxy") {
		t.Errorf("filtered response should not include other-proxy:\n%s", body)
	}
}

func TestDescribeDBProxies_UnknownName(t *testing.T) {
	s := newProxySvc(t)
	body := post(t, s, url.Values{"Action": {"DescribeDBProxies"}, "DBProxyName": {"nope"}})
	if !strings.Contains(body, "DBProxyNotFoundFault") {
		t.Errorf("expected DBProxyNotFoundFault:\n%s", body)
	}
}

func TestDescribeDBProxies_EmptyWithoutFilter(t *testing.T) {
	s := New("us-east-1", "postgres", 5432)
	body := post(t, s, url.Values{"Action": {"DescribeDBProxies"}})
	if strings.Contains(body, "Fault") {
		t.Errorf("an unfiltered describe over no proxies must not fault:\n%s", body)
	}
	if !strings.Contains(body, "<DBProxies></DBProxies>") {
		t.Errorf("expected an empty DBProxies list:\n%s", body)
	}
}

func TestProxyTagsRoundTrip(t *testing.T) {
	s := newProxySvc(t)
	body := post(t, s, url.Values{"Action": {"DescribeDBProxies"}})
	arn := between(body, "<DBProxyArn>", "</DBProxyArn>")
	if arn == "" {
		t.Fatal("no proxy ARN in response")
	}

	body = post(t, s, url.Values{"Action": {"ListTagsForResource"}, "ResourceName": {arn}})
	if !strings.Contains(body, "<Key>env</Key>") || !strings.Contains(body, "<Value>smoke</Value>") {
		t.Errorf("CreateDBProxy tags did not reach the tag store:\n%s", body)
	}
}

// --- Modify / Delete ---

func TestModifyDBProxy(t *testing.T) {
	s := newProxySvc(t)
	body := post(t, s, url.Values{
		"Action":            {"ModifyDBProxy"},
		"DBProxyName":       {"app-proxy"},
		"IdleClientTimeout": {"120"},
		"DebugLogging":      {"false"},
	})
	if !strings.Contains(body, "<IdleClientTimeout>120</IdleClientTimeout>") {
		t.Errorf("ModifyDBProxy did not apply IdleClientTimeout:\n%s", body)
	}
	if !strings.Contains(body, "<DebugLogging>false</DebugLogging>") {
		t.Errorf("ModifyDBProxy did not apply DebugLogging:\n%s", body)
	}
	// Untouched fields must survive.
	if !strings.Contains(body, "<RequireTLS>true</RequireTLS>") {
		t.Errorf("ModifyDBProxy clobbered RequireTLS:\n%s", body)
	}
}

func TestModifyDBProxy_UnknownProxy(t *testing.T) {
	s := newProxySvc(t)
	body := post(t, s, url.Values{"Action": {"ModifyDBProxy"}, "DBProxyName": {"nope"}})
	if !strings.Contains(body, "DBProxyNotFoundFault") {
		t.Errorf("expected DBProxyNotFoundFault:\n%s", body)
	}
}

func TestDeleteDBProxy(t *testing.T) {
	s := newProxySvc(t)
	body := post(t, s, url.Values{"Action": {"DeleteDBProxy"}, "DBProxyName": {"app-proxy"}})
	if !strings.Contains(body, "<Status>deleting</Status>") {
		t.Errorf("DeleteDBProxy should report the proxy as deleting:\n%s", body)
	}
	body = post(t, s, url.Values{"Action": {"DescribeDBProxies"}, "DBProxyName": {"app-proxy"}})
	if !strings.Contains(body, "DBProxyNotFoundFault") {
		t.Errorf("proxy should be gone after delete:\n%s", body)
	}
}

func TestDeleteDBProxy_UnknownProxy(t *testing.T) {
	s := newProxySvc(t)
	body := post(t, s, url.Values{"Action": {"DeleteDBProxy"}, "DBProxyName": {"nope"}})
	if !strings.Contains(body, "DBProxyNotFoundFault") {
		t.Errorf("expected DBProxyNotFoundFault:\n%s", body)
	}
}

// --- Target groups ---

// Real RDS creates the default target group with the proxy; Terraform's
// aws_db_proxy_default_target_group modifies it rather than creating it.
func TestCreateDBProxy_CreatesDefaultTargetGroup(t *testing.T) {
	s := newProxySvc(t)
	body := post(t, s, url.Values{
		"Action":      {"DescribeDBProxyTargetGroups"},
		"DBProxyName": {"app-proxy"},
	})
	for _, want := range []string{
		"<TargetGroupName>default</TargetGroupName>",
		"<TargetGroupArn>arn:aws:rds:us-east-1:000000000000:target-group:prx-tg-",
		"<IsDefault>true</IsDefault>",
		"<Status>available</Status>",
		"<MaxConnectionsPercent>100</MaxConnectionsPercent>",
		"<MaxIdleConnectionsPercent>50</MaxIdleConnectionsPercent>",
		"<ConnectionBorrowTimeout>120</ConnectionBorrowTimeout>",
		"<TargetGroups><member>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("default target group missing %q:\n%s", want, body)
		}
	}
}

func TestModifyDBProxyTargetGroup(t *testing.T) {
	s := newProxySvc(t)
	body := post(t, s, url.Values{
		"Action":          {"ModifyDBProxyTargetGroup"},
		"DBProxyName":     {"app-proxy"},
		"TargetGroupName": {"default"},
		"ConnectionPoolConfig.MaxConnectionsPercent":          {"75"},
		"ConnectionPoolConfig.MaxIdleConnectionsPercent":      {"25"},
		"ConnectionPoolConfig.ConnectionBorrowTimeout":        {"30"},
		"ConnectionPoolConfig.SessionPinningFilters.member.1": {"EXCLUDE_VARIABLE_SETS"},
		"ConnectionPoolConfig.InitQuery":                      {"SET x=1"},
	})
	for _, want := range []string{
		"<MaxConnectionsPercent>75</MaxConnectionsPercent>",
		"<MaxIdleConnectionsPercent>25</MaxIdleConnectionsPercent>",
		"<ConnectionBorrowTimeout>30</ConnectionBorrowTimeout>",
		"<member>EXCLUDE_VARIABLE_SETS</member>",
		"<InitQuery>SET x=1</InitQuery>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("ModifyDBProxyTargetGroup missing %q:\n%s", want, body)
		}
	}

	// The change must persist for the read-back Terraform does next.
	body = post(t, s, url.Values{
		"Action": {"DescribeDBProxyTargetGroups"}, "DBProxyName": {"app-proxy"},
	})
	if !strings.Contains(body, "<MaxConnectionsPercent>75</MaxConnectionsPercent>") {
		t.Errorf("pool config did not persist:\n%s", body)
	}
}

// Only the fields the caller sent may change — the provider sends the pool block
// alone, and the rest of the group must survive.
func TestModifyDBProxyTargetGroup_PartialUpdate(t *testing.T) {
	s := newProxySvc(t)
	post(t, s, url.Values{
		"Action": {"ModifyDBProxyTargetGroup"}, "DBProxyName": {"app-proxy"},
		"TargetGroupName": {"default"},
		"ConnectionPoolConfig.MaxConnectionsPercent": {"90"},
	})
	body := post(t, s, url.Values{
		"Action": {"DescribeDBProxyTargetGroups"}, "DBProxyName": {"app-proxy"},
	})
	if !strings.Contains(body, "<MaxConnectionsPercent>90</MaxConnectionsPercent>") {
		t.Errorf("MaxConnectionsPercent not applied:\n%s", body)
	}
	if !strings.Contains(body, "<MaxIdleConnectionsPercent>50</MaxIdleConnectionsPercent>") {
		t.Errorf("MaxIdleConnectionsPercent should keep its default:\n%s", body)
	}
	if !strings.Contains(body, "<ConnectionBorrowTimeout>120</ConnectionBorrowTimeout>") {
		t.Errorf("ConnectionBorrowTimeout should keep its default:\n%s", body)
	}
}

func TestDescribeDBProxyTargetGroups_UnknownProxy(t *testing.T) {
	s := newProxySvc(t)
	body := post(t, s, url.Values{"Action": {"DescribeDBProxyTargetGroups"}, "DBProxyName": {"nope"}})
	if !strings.Contains(body, "DBProxyNotFoundFault") {
		t.Errorf("expected DBProxyNotFoundFault:\n%s", body)
	}
}

func TestModifyDBProxyTargetGroup_UnknownGroup(t *testing.T) {
	s := newProxySvc(t)
	body := post(t, s, url.Values{
		"Action": {"ModifyDBProxyTargetGroup"}, "DBProxyName": {"app-proxy"},
		"TargetGroupName": {"nope"},
	})
	if !strings.Contains(body, "DBProxyTargetGroupNotFoundFault") {
		t.Errorf("expected DBProxyTargetGroupNotFoundFault:\n%s", body)
	}
}

// --- Targets ---

func TestRegisterDBProxyTargets_Cluster(t *testing.T) {
	s := newProxySvc(t)
	body := post(t, s, url.Values{
		"Action":                        {"RegisterDBProxyTargets"},
		"DBProxyName":                   {"app-proxy"},
		"TargetGroupName":               {"default"},
		"DBClusterIdentifiers.member.1": {"app-db"},
	})
	for _, want := range []string{
		"<Type>TRACKED_CLUSTER</Type>",
		"<RdsResourceId>app-db</RdsResourceId>",
		"<TrackedClusterId>app-db</TrackedClusterId>",
		"<TargetArn>arn:aws:rds:us-east-1:000000000000:cluster:app-db</TargetArn>",
		"<State>AVAILABLE</State>",
		"<DBProxyTargets><member>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("RegisterDBProxyTargets missing %q:\n%s", want, body)
		}
	}

	body = post(t, s, url.Values{
		"Action": {"DescribeDBProxyTargets"}, "DBProxyName": {"app-proxy"},
	})
	if !strings.Contains(body, "<RdsResourceId>app-db</RdsResourceId>") {
		t.Errorf("registered cluster target not returned by describe:\n%s", body)
	}
	if !strings.Contains(body, "<Targets><member>") {
		t.Errorf("Targets list should wrap entries in <member>:\n%s", body)
	}
}

func TestRegisterDBProxyTargets_Instance(t *testing.T) {
	s := newProxySvc(t)
	body := post(t, s, url.Values{
		"Action":                         {"RegisterDBProxyTargets"},
		"DBProxyName":                    {"app-proxy"},
		"DBInstanceIdentifiers.member.1": {"app-db-1"},
	})
	if !strings.Contains(body, "<Type>RDS_INSTANCE</Type>") {
		t.Errorf("expected an RDS_INSTANCE target:\n%s", body)
	}
	if !strings.Contains(body, "<RdsResourceId>app-db-1</RdsResourceId>") {
		t.Errorf("expected the instance identifier as RdsResourceId:\n%s", body)
	}
	// An instance target carries no TrackedClusterId.
	if strings.Contains(body, "<TrackedClusterId>") {
		t.Errorf("instance target should not report TrackedClusterId:\n%s", body)
	}
}

// TargetGroupName is optional — RDS defaults it to the default group.
func TestRegisterDBProxyTargets_DefaultsTargetGroup(t *testing.T) {
	s := newProxySvc(t)
	post(t, s, url.Values{
		"Action": {"RegisterDBProxyTargets"}, "DBProxyName": {"app-proxy"},
		"DBClusterIdentifiers.member.1": {"app-db"},
	})
	body := post(t, s, url.Values{
		"Action": {"DescribeDBProxyTargets"}, "DBProxyName": {"app-proxy"},
		"TargetGroupName": {"default"},
	})
	if !strings.Contains(body, "<RdsResourceId>app-db</RdsResourceId>") {
		t.Errorf("target should have landed in the default group:\n%s", body)
	}
}

func TestRegisterDBProxyTargets_UnknownCluster(t *testing.T) {
	s := newProxySvc(t)
	body := post(t, s, url.Values{
		"Action": {"RegisterDBProxyTargets"}, "DBProxyName": {"app-proxy"},
		"DBClusterIdentifiers.member.1": {"nope"},
	})
	if !strings.Contains(body, "DBClusterNotFoundFault") {
		t.Errorf("expected DBClusterNotFoundFault:\n%s", body)
	}
}

func TestRegisterDBProxyTargets_UnknownInstance(t *testing.T) {
	s := newProxySvc(t)
	body := post(t, s, url.Values{
		"Action": {"RegisterDBProxyTargets"}, "DBProxyName": {"app-proxy"},
		"DBInstanceIdentifiers.member.1": {"nope"},
	})
	if !strings.Contains(body, "DBInstanceNotFoundFault") {
		t.Errorf("expected DBInstanceNotFoundFault:\n%s", body)
	}
}

// Re-registering the same target replaces it: a second apply must not double it.
func TestRegisterDBProxyTargets_Idempotent(t *testing.T) {
	s := newProxySvc(t)
	form := url.Values{
		"Action": {"RegisterDBProxyTargets"}, "DBProxyName": {"app-proxy"},
		"DBClusterIdentifiers.member.1": {"app-db"},
	}
	post(t, s, form)
	post(t, s, form)

	body := post(t, s, url.Values{
		"Action": {"DescribeDBProxyTargets"}, "DBProxyName": {"app-proxy"},
	})
	if n := strings.Count(body, "<RdsResourceId>app-db</RdsResourceId>"); n != 1 {
		t.Errorf("expected 1 target after re-registering, got %d:\n%s", n, body)
	}
}

func TestDeregisterDBProxyTargets(t *testing.T) {
	s := newProxySvc(t)
	post(t, s, url.Values{
		"Action": {"RegisterDBProxyTargets"}, "DBProxyName": {"app-proxy"},
		"DBClusterIdentifiers.member.1": {"app-db"},
	})
	post(t, s, url.Values{
		"Action": {"DeregisterDBProxyTargets"}, "DBProxyName": {"app-proxy"},
		"DBClusterIdentifiers.member.1": {"app-db"},
	})

	body := post(t, s, url.Values{
		"Action": {"DescribeDBProxyTargets"}, "DBProxyName": {"app-proxy"},
	})
	if strings.Contains(body, "app-db") {
		t.Errorf("target should be gone after deregister:\n%s", body)
	}
	if !strings.Contains(body, "<Targets></Targets>") {
		t.Errorf("expected an empty Targets list:\n%s", body)
	}
}

func TestDescribeDBProxyTargets_UnknownProxy(t *testing.T) {
	s := newProxySvc(t)
	body := post(t, s, url.Values{"Action": {"DescribeDBProxyTargets"}, "DBProxyName": {"nope"}})
	if !strings.Contains(body, "DBProxyNotFoundFault") {
		t.Errorf("expected DBProxyNotFoundFault:\n%s", body)
	}
}

// --- State ---

func TestResetClearsProxies(t *testing.T) {
	s := newProxySvc(t)
	if s.ProxyCount() != 1 {
		t.Fatalf("expected 1 proxy, got %d", s.ProxyCount())
	}
	s.Reset()
	if s.ProxyCount() != 0 {
		t.Errorf("Reset left %d proxies behind", s.ProxyCount())
	}
}

// between returns the text between two markers, or "" when absent.
func between(s, start, end string) string {
	i := strings.Index(s, start)
	if i < 0 {
		return ""
	}
	rest := s[i+len(start):]
	j := strings.Index(rest, end)
	if j < 0 {
		return ""
	}
	return rest[:j]
}
