package ec2

import (
	"regexp"
	"strings"
	"testing"
)

// extract pulls the first capture of pattern out of an XML body.
func extract(t *testing.T, body, pattern string) string {
	t.Helper()
	m := regexp.MustCompile(pattern).FindStringSubmatch(body)
	if len(m) < 2 {
		t.Fatalf("pattern %q did not match\nbody: %s", pattern, body)
	}
	return m[1]
}

func createEndpoint(t *testing.T, svc *Service, vpcID, service string) string {
	t.Helper()
	body := must200(t, ec2Req(t, svc,
		"Action=CreateVpcEndpoint&VpcId="+vpcID+"&ServiceName="+service))
	return extract(t, body, `<vpcEndpointId>(vpce-[^<]+)</vpcEndpointId>`)
}

// ── Prefix lists ──────────────────────────────────────────────────────────────

func TestDescribePrefixLists_SeededForGatewayServices(t *testing.T) {
	svc := newSvc()
	body := must200(t, ec2Req(t, svc, "Action=DescribePrefixLists"))
	mustContain(t, body, "com.amazonaws.us-east-1.s3")
	mustContain(t, body, "com.amazonaws.us-east-1.dynamodb")
}

// The provider resolves a gateway endpoint's prefix list by filtering on the
// service name, so that filter has to work for prefix_list_id to populate.
func TestDescribePrefixLists_FilterByName(t *testing.T) {
	svc := newSvc()
	body := must200(t, ec2Req(t, svc,
		"Action=DescribePrefixLists&Filter.1.Name=prefix-list-name"+
			"&Filter.1.Value.1=com.amazonaws.us-east-1.s3"))

	mustContain(t, body, "com.amazonaws.us-east-1.s3")
	if strings.Contains(body, "dynamodb") {
		t.Errorf("filter leaked a non-matching prefix list\nbody: %s", body)
	}
}

// s3PrefixListID resolves the S3 service list by name. Describe output is
// unordered, so tests that need one specific list must filter for it.
func s3PrefixListID(t *testing.T, svc *Service) string {
	t.Helper()
	body := must200(t, ec2Req(t, svc,
		"Action=DescribePrefixLists&Filter.1.Name=prefix-list-name"+
			"&Filter.1.Value.1=com.amazonaws.us-east-1.s3"))
	return extract(t, body, `<prefixListId>(pl-[^<]+)</prefixListId>`)
}

// A prefix list ID that changed between restarts would look like a replaced
// list to a state file that recorded it.
func TestPrefixListIDs_AreStableAcrossInstances(t *testing.T) {
	if s3PrefixListID(t, newSvc()) != s3PrefixListID(t, newSvc()) {
		t.Error("prefix list IDs differ between service instances")
	}
}

func TestPrefixLists_SurviveReset(t *testing.T) {
	svc := newSvc()
	before := s3PrefixListID(t, svc)

	svc.Reset()

	if after := s3PrefixListID(t, svc); before != after {
		t.Errorf("AWS-managed prefix lists should survive reset: %q → %q", before, after)
	}
}

func TestGetManagedPrefixListEntries(t *testing.T) {
	svc := newSvc()
	id := s3PrefixListID(t, svc)

	body := must200(t, ec2Req(t, svc, "Action=GetManagedPrefixListEntries&PrefixListId="+id))
	mustContain(t, body, "<cidr>")
}

func TestGetManagedPrefixListEntries_NotFound(t *testing.T) {
	svc := newSvc()
	w := ec2Req(t, svc, "Action=GetManagedPrefixListEntries&PrefixListId=pl-deadbeef")
	if w.Code != 400 {
		t.Fatalf("expected 400 for unknown prefix list, got %d", w.Code)
	}
	mustContain(t, w.Body.String(), "InvalidPrefixListID.NotFound")
}

func TestDescribeManagedPrefixLists(t *testing.T) {
	svc := newSvc()
	body := must200(t, ec2Req(t, svc, "Action=DescribeManagedPrefixLists"))
	mustContain(t, body, "<ownerId>AWS</ownerId>")
	mustContain(t, body, "<state>create-complete</state>")
	mustContain(t, body, "arn:aws:ec2:us-east-1:aws:prefix-list/pl-")
}

// ── VPC endpoints ─────────────────────────────────────────────────────────────

func TestCreateAndDescribeVpcEndpoint(t *testing.T) {
	svc := newSvc()
	id := createEndpoint(t, svc, "vpc-123", "com.amazonaws.us-east-1.s3")

	body := must200(t, ec2Req(t, svc, "Action=DescribeVpcEndpoints"))
	mustContain(t, body, id)
	mustContain(t, body, "<vpcEndpointType>Gateway</vpcEndpointType>")
	mustContain(t, body, "<state>available</state>")
}

func TestDescribeVpcEndpoints_FilterByVpcAndService(t *testing.T) {
	svc := newSvc()
	wanted := createEndpoint(t, svc, "vpc-aaa", "com.amazonaws.us-east-1.s3")
	otherVPC := createEndpoint(t, svc, "vpc-bbb", "com.amazonaws.us-east-1.s3")
	otherSvc := createEndpoint(t, svc, "vpc-aaa", "com.amazonaws.us-east-1.dynamodb")

	body := must200(t, ec2Req(t, svc,
		"Action=DescribeVpcEndpoints"+
			"&Filter.1.Name=vpc-id&Filter.1.Value.1=vpc-aaa"+
			"&Filter.2.Name=service-name&Filter.2.Value.1=com.amazonaws.us-east-1.s3"))

	mustContain(t, body, wanted)
	for _, unwanted := range []string{otherVPC, otherSvc} {
		if strings.Contains(body, unwanted) {
			t.Errorf("filters matched an endpoint they should have excluded: %s", unwanted)
		}
	}
}

func TestCreateVpcEndpoint_RequiresVpcAndService(t *testing.T) {
	svc := newSvc()
	w := ec2Req(t, svc, "Action=CreateVpcEndpoint&VpcId=vpc-123")
	if w.Code != 400 {
		t.Fatalf("expected 400 without ServiceName, got %d", w.Code)
	}
	mustContain(t, w.Body.String(), "MissingParameter")
}

func TestCreateVpcEndpoint_StoresRouteTablesAndTags(t *testing.T) {
	svc := newSvc()
	must200(t, ec2Req(t, svc,
		"Action=CreateVpcEndpoint&VpcId=vpc-123&ServiceName=com.amazonaws.us-east-1.s3"+
			"&RouteTableId.1=rtb-aaa&RouteTableId.2=rtb-bbb"+
			"&TagSpecification.1.ResourceType=vpc-endpoint"+
			"&TagSpecification.1.Tag.1.Key=Name&TagSpecification.1.Tag.1.Value=gw"))

	body := must200(t, ec2Req(t, svc, "Action=DescribeVpcEndpoints"))
	mustContain(t, body, "<item>rtb-aaa</item>")
	mustContain(t, body, "<item>rtb-bbb</item>")
	mustContain(t, body, "<key>Name</key><value>gw</value>")
}

// An endpoint policy is caller-supplied JSON; unescaped quotes would produce
// XML the client cannot parse.
func TestCreateVpcEndpoint_EscapesPolicyDocument(t *testing.T) {
	svc := newSvc()
	must200(t, ec2Req(t, svc,
		"Action=CreateVpcEndpoint&VpcId=vpc-123&ServiceName=com.amazonaws.us-east-1.s3"+
			"&PolicyDocument="+`%7B%22Version%22%3A%222012-10-17%22%7D`))

	body := must200(t, ec2Req(t, svc, "Action=DescribeVpcEndpoints"))
	mustContain(t, body, "&quot;Version&quot;")
	if strings.Contains(body, `<policyDocument>{"`) {
		t.Errorf("policy document was not escaped\nbody: %s", body)
	}
}

func TestDeleteVpcEndpoints(t *testing.T) {
	svc := newSvc()
	id := createEndpoint(t, svc, "vpc-123", "com.amazonaws.us-east-1.s3")

	must200(t, ec2Req(t, svc, "Action=DeleteVpcEndpoints&VpcEndpointId.1="+id))

	body := must200(t, ec2Req(t, svc, "Action=DescribeVpcEndpoints"))
	if strings.Contains(body, id) {
		t.Errorf("endpoint %s still present after delete\nbody: %s", id, body)
	}
}

func TestModifyVpcEndpoint_AddsAndRemovesRouteTables(t *testing.T) {
	svc := newSvc()
	id := createEndpoint(t, svc, "vpc-123", "com.amazonaws.us-east-1.s3")

	must200(t, ec2Req(t, svc,
		"Action=ModifyVpcEndpoint&VpcEndpointId="+id+"&AddRouteTableId.1=rtb-new"))
	mustContain(t, must200(t, ec2Req(t, svc, "Action=DescribeVpcEndpoints")), "rtb-new")

	must200(t, ec2Req(t, svc,
		"Action=ModifyVpcEndpoint&VpcEndpointId="+id+"&RemoveRouteTableId.1=rtb-new"))
	if strings.Contains(must200(t, ec2Req(t, svc, "Action=DescribeVpcEndpoints")), "rtb-new") {
		t.Error("route table still attached after removal")
	}
}

// ── Security group rule targets ───────────────────────────────────────────────

func authorizeEgressRule(t *testing.T, svc *Service, sgID, target string) {
	t.Helper()
	must200(t, ec2Req(t, svc,
		"Action=AuthorizeSecurityGroupEgress&GroupId="+sgID+
			"&IpPermissions.1.IpProtocol=tcp"+
			"&IpPermissions.1.FromPort=443&IpPermissions.1.ToPort=443&"+target))
}

func newSGWithVPC(t *testing.T, svc *Service) string {
	t.Helper()
	body := must200(t, ec2Req(t, svc,
		"Action=CreateSecurityGroup&GroupName=test&GroupDescription=test&VpcId=vpc-123"))
	return extract(t, body, `<groupId>(sg-[^<]+)</groupId>`)
}

func TestAuthorizeEgress_PrefixListTargetRoundTrips(t *testing.T) {
	svc := newSvc()
	sgID := newSGWithVPC(t, svc)
	authorizeEgressRule(t, svc, sgID, "IpPermissions.1.PrefixListIds.1.PrefixListId=pl-abc123")

	body := must200(t, ec2Req(t, svc, "Action=DescribeSecurityGroups"))
	mustContain(t, body, "<prefixListId>pl-abc123</prefixListId>")
}

func TestAuthorizeEgress_ReferencedGroupTargetRoundTrips(t *testing.T) {
	svc := newSvc()
	sgID := newSGWithVPC(t, svc)
	authorizeEgressRule(t, svc, sgID, "IpPermissions.1.Groups.1.GroupId=sg-peer99")

	body := must200(t, ec2Req(t, svc, "Action=DescribeSecurityGroups"))
	mustContain(t, body, "<groupId>sg-peer99</groupId>")
}

func TestDescribeSecurityGroupRules_ReportsNonCidrTargets(t *testing.T) {
	svc := newSvc()
	sgID := newSGWithVPC(t, svc)
	authorizeEgressRule(t, svc, sgID, "IpPermissions.1.PrefixListIds.1.PrefixListId=pl-abc123")

	body := must200(t, ec2Req(t, svc,
		"Action=DescribeSecurityGroupRules&Filter.1.Name=group-id&Filter.1.Value.1="+sgID))
	mustContain(t, body, "<prefixListId>pl-abc123</prefixListId>")
	mustContain(t, body, "<isEgress>true</isEgress>")
}

// Rules that share a protocol and port range but point at different targets are
// distinct; revoking one must leave the other in place.
func TestRevokeEgress_OnlyRemovesTheMatchingTarget(t *testing.T) {
	svc := newSvc()
	sgID := newSGWithVPC(t, svc)
	authorizeEgressRule(t, svc, sgID, "IpPermissions.1.PrefixListIds.1.PrefixListId=pl-abc123")
	authorizeEgressRule(t, svc, sgID, "IpPermissions.1.Groups.1.GroupId=sg-peer99")

	must200(t, ec2Req(t, svc,
		"Action=RevokeSecurityGroupEgress&GroupId="+sgID+
			"&IpPermissions.1.IpProtocol=tcp"+
			"&IpPermissions.1.FromPort=443&IpPermissions.1.ToPort=443"+
			"&IpPermissions.1.PrefixListIds.1.PrefixListId=pl-abc123"))

	body := must200(t, ec2Req(t, svc, "Action=DescribeSecurityGroups"))
	if strings.Contains(body, "pl-abc123") {
		t.Error("revoked prefix list rule is still present")
	}
	mustContain(t, body, "sg-peer99")
}

func TestServiceHasNoVpcEndpointsInitially(t *testing.T) {
	svc := newSvc()
	body := must200(t, ec2Req(t, svc, "Action=DescribeVpcEndpoints"))
	if strings.Contains(body, "vpce-") {
		t.Errorf("expected no endpoints before one is created\nbody: %s", body)
	}
}
