package ec2

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ── Helpers ───────────────────────────────────────────────────────────────────

func newSvc() *Service { return New("us-east-1") }

func ec2Req(t *testing.T, svc *Service, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	return w
}

func must200(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	return w.Body.String()
}

func mustContain(t *testing.T, body, want string) {
	t.Helper()
	if !strings.Contains(body, want) {
		t.Errorf("expected response to contain %q\nbody: %s", want, body)
	}
}

// ── Detect ────────────────────────────────────────────────────────────────────

func TestDetect(t *testing.T) {
	svc := newSvc()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("Action=DescribeVpcs"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if !svc.Detect(req) {
		t.Fatal("Detect should return true for EC2 POST /")
	}
}

func TestDetectGetMethod(t *testing.T) {
	svc := newSvc()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if svc.Detect(req) {
		t.Fatal("Detect should return false for GET requests")
	}
}

func TestDetectJSONContentType(t *testing.T) {
	svc := newSvc()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Content-Type", "application/json")
	if svc.Detect(req) {
		t.Fatal("Detect should return false for JSON content type")
	}
}

func TestDetectNonRootPath(t *testing.T) {
	svc := newSvc()
	req := httptest.NewRequest(http.MethodPost, "/something",
		strings.NewReader("Action=DescribeVpcs"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if svc.Detect(req) {
		t.Fatal("Detect should return false for non-root path")
	}
}

// ── Availability Zones ────────────────────────────────────────────────────────

func TestDescribeAvailabilityZones(t *testing.T) {
	svc := newSvc()
	w := ec2Req(t, svc, "Action=DescribeAvailabilityZones&Filter.1.Name=state&Filter.1.Value.1=available")
	body := must200(t, w)
	mustContain(t, body, "us-east-1a")
	mustContain(t, body, "us-east-1b")
	mustContain(t, body, "us-east-1c")
	mustContain(t, body, "DescribeAvailabilityZonesResponse")
	mustContain(t, body, "available")
}

// ── VPC ───────────────────────────────────────────────────────────────────────

func TestCreateVpc(t *testing.T) {
	svc := newSvc()
	w := ec2Req(t, svc, "Action=CreateVpc&CidrBlock=10.0.0.0%2F16")
	body := must200(t, w)
	mustContain(t, body, "CreateVpcResponse")
	mustContain(t, body, "vpc-")
	mustContain(t, body, "10.0.0.0/16")
	mustContain(t, body, "available")
}

func TestCreateVpcAutoCreatesSGAndRT(t *testing.T) {
	svc := newSvc()
	ec2Req(t, svc, "Action=CreateVpc&CidrBlock=10.0.0.0%2F16")

	svc.mu.RLock()
	defer svc.mu.RUnlock()
	if len(svc.secGroups) != 1 {
		t.Errorf("expected 1 security group, got %d", len(svc.secGroups))
	}
	if len(svc.routeTables) != 1 {
		t.Errorf("expected 1 route table (main), got %d", len(svc.routeTables))
	}
}

func TestDescribeVpcs(t *testing.T) {
	svc := newSvc()
	w := ec2Req(t, svc, "Action=CreateVpc&CidrBlock=10.1.0.0%2F16")
	body := must200(t, w)
	vpcID := extractBetween(body, "<vpcId>", "</vpcId>")

	w2 := ec2Req(t, svc, "Action=DescribeVpcs&VpcId.1="+vpcID)
	body2 := must200(t, w2)
	mustContain(t, body2, vpcID)
	mustContain(t, body2, "10.1.0.0/16")
}

func TestDescribeVpcsFilter(t *testing.T) {
	svc := newSvc()
	w := ec2Req(t, svc, "Action=CreateVpc&CidrBlock=10.2.0.0%2F16")
	body := must200(t, w)
	vpcID := extractBetween(body, "<vpcId>", "</vpcId>")

	w2 := ec2Req(t, svc, "Action=DescribeVpcs&Filter.1.Name=vpc-id&Filter.1.Value.1="+vpcID)
	body2 := must200(t, w2)
	mustContain(t, body2, vpcID)
}

func TestDescribeVpcAttribute(t *testing.T) {
	svc := newSvc()
	w := ec2Req(t, svc, "Action=CreateVpc&CidrBlock=10.0.0.0%2F16")
	body := must200(t, w)
	vpcID := extractBetween(body, "<vpcId>", "</vpcId>")

	w2 := ec2Req(t, svc, "Action=DescribeVpcAttribute&VpcId="+vpcID+"&Attribute=enableDnsSupport")
	body2 := must200(t, w2)
	mustContain(t, body2, "enableDnsSupport")
	mustContain(t, body2, "<value>true</value>")
}

func TestModifyVpcAttribute(t *testing.T) {
	svc := newSvc()
	w := ec2Req(t, svc, "Action=CreateVpc&CidrBlock=10.0.0.0%2F16")
	body := must200(t, w)
	vpcID := extractBetween(body, "<vpcId>", "</vpcId>")

	w2 := ec2Req(t, svc, "Action=ModifyVpcAttribute&VpcId="+vpcID+"&EnableDnsHostnames.Value=true")
	body2 := must200(t, w2)
	mustContain(t, body2, "<return>true</return>")

	// Verify attribute was stored.
	svc.mu.RLock()
	v := svc.vpcs[vpcID]
	svc.mu.RUnlock()
	if v == nil || !v.dnsHostnames {
		t.Error("expected dnsHostnames to be true after ModifyVpcAttribute")
	}
}

func TestDeleteVpc(t *testing.T) {
	svc := newSvc()
	w := ec2Req(t, svc, "Action=CreateVpc&CidrBlock=10.0.0.0%2F16")
	body := must200(t, w)
	vpcID := extractBetween(body, "<vpcId>", "</vpcId>")

	w2 := ec2Req(t, svc, "Action=DeleteVpc&VpcId="+vpcID)
	body2 := must200(t, w2)
	mustContain(t, body2, "<return>true</return>")

	svc.mu.RLock()
	_, exists := svc.vpcs[vpcID]
	svc.mu.RUnlock()
	if exists {
		t.Error("expected VPC to be deleted")
	}
}

// ── Subnets ───────────────────────────────────────────────────────────────────

func TestCreateSubnet(t *testing.T) {
	svc := newSvc()
	w := ec2Req(t, svc, "Action=CreateSubnet&VpcId=vpc-test&CidrBlock=10.0.1.0%2F24&AvailabilityZone=us-east-1a")
	body := must200(t, w)
	mustContain(t, body, "CreateSubnetResponse")
	mustContain(t, body, "subnet-")
	mustContain(t, body, "vpc-test")
	mustContain(t, body, "10.0.1.0/24")
	mustContain(t, body, "us-east-1a")
}

func TestDescribeSubnetsStoredSubnet(t *testing.T) {
	svc := newSvc()
	w := ec2Req(t, svc, "Action=CreateSubnet&VpcId=vpc-test&CidrBlock=10.0.2.0%2F24&AvailabilityZone=us-east-1b")
	body := must200(t, w)
	subnetID := extractBetween(body, "<subnetId>", "</subnetId>")

	w2 := ec2Req(t, svc, "Action=DescribeSubnets&SubnetId.1="+subnetID)
	body2 := must200(t, w2)
	mustContain(t, body2, subnetID)
	mustContain(t, body2, "10.0.2.0/24")
}

func TestDescribeSubnetsFilterForm(t *testing.T) {
	svc := newSvc()
	body := "Action=DescribeSubnets&Version=2016-11-15" +
		"&Filter.1.Name=subnet-id&Filter.1.Value.1=subnet-00000001&Filter.1.Value.2=subnet-00000002"
	w := ec2Req(t, svc, body)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	resp := w.Body.String()
	mustContain(t, resp, "subnet-00000001")
	mustContain(t, resp, "subnet-00000002")
	mustContain(t, resp, "vpc-00000001")
	mustContain(t, resp, "DescribeSubnetsResponse")
}

func TestDescribeSubnetsSubnetIDForm(t *testing.T) {
	svc := newSvc()
	body := "Action=DescribeSubnets&SubnetId.1=subnet-aaa&SubnetId.2=subnet-bbb"
	w := ec2Req(t, svc, body)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	resp := w.Body.String()
	if !strings.Contains(resp, "subnet-aaa") || !strings.Contains(resp, "subnet-bbb") {
		t.Error("response missing requested subnet IDs")
	}
}

func TestModifySubnetAttribute(t *testing.T) {
	svc := newSvc()
	w := ec2Req(t, svc, "Action=CreateSubnet&VpcId=vpc-x&CidrBlock=10.0.3.0%2F24")
	body := must200(t, w)
	subnetID := extractBetween(body, "<subnetId>", "</subnetId>")

	w2 := ec2Req(t, svc, "Action=ModifySubnetAttribute&SubnetId="+subnetID+"&MapPublicIpOnLaunch.Value=true")
	body2 := must200(t, w2)
	mustContain(t, body2, "<return>true</return>")

	svc.mu.RLock()
	sn := svc.subnets[subnetID]
	svc.mu.RUnlock()
	if sn == nil || !sn.mapPublicIpOnLaunch {
		t.Error("expected mapPublicIpOnLaunch to be true")
	}
}

func TestDeleteSubnet(t *testing.T) {
	svc := newSvc()
	w := ec2Req(t, svc, "Action=CreateSubnet&VpcId=vpc-x&CidrBlock=10.0.4.0%2F24")
	body := must200(t, w)
	subnetID := extractBetween(body, "<subnetId>", "</subnetId>")

	w2 := ec2Req(t, svc, "Action=DeleteSubnet&SubnetId="+subnetID)
	body2 := must200(t, w2)
	mustContain(t, body2, "<return>true</return>")

	svc.mu.RLock()
	_, exists := svc.subnets[subnetID]
	svc.mu.RUnlock()
	if exists {
		t.Error("expected subnet to be deleted")
	}
}

// ── Internet Gateways ─────────────────────────────────────────────────────────

func TestCreateInternetGateway(t *testing.T) {
	svc := newSvc()
	w := ec2Req(t, svc, "Action=CreateInternetGateway")
	body := must200(t, w)
	mustContain(t, body, "CreateInternetGatewayResponse")
	mustContain(t, body, "igw-")
}

func TestDescribeInternetGateways(t *testing.T) {
	svc := newSvc()
	w := ec2Req(t, svc, "Action=CreateInternetGateway")
	body := must200(t, w)
	igwID := extractBetween(body, "<internetGatewayId>", "</internetGatewayId>")

	w2 := ec2Req(t, svc, "Action=DescribeInternetGateways&InternetGatewayId.1="+igwID)
	body2 := must200(t, w2)
	mustContain(t, body2, igwID)
}

func TestAttachDetachInternetGateway(t *testing.T) {
	svc := newSvc()
	w := ec2Req(t, svc, "Action=CreateInternetGateway")
	body := must200(t, w)
	igwID := extractBetween(body, "<internetGatewayId>", "</internetGatewayId>")

	w2 := ec2Req(t, svc, "Action=AttachInternetGateway&InternetGatewayId="+igwID+"&VpcId=vpc-attach-test")
	must200(t, w2)

	// Should appear in filter by attachment.vpc-id.
	w3 := ec2Req(t, svc, "Action=DescribeInternetGateways"+
		"&Filter.1.Name=attachment.vpc-id&Filter.1.Value.1=vpc-attach-test")
	body3 := must200(t, w3)
	mustContain(t, body3, igwID)
	mustContain(t, body3, "vpc-attach-test")

	// Detach.
	w4 := ec2Req(t, svc, "Action=DetachInternetGateway&InternetGatewayId="+igwID+"&VpcId=vpc-attach-test")
	must200(t, w4)

	svc.mu.RLock()
	igw := svc.igws[igwID]
	svc.mu.RUnlock()
	if len(igw.attachments) != 0 {
		t.Error("expected 0 attachments after detach")
	}
}

func TestDeleteInternetGateway(t *testing.T) {
	svc := newSvc()
	w := ec2Req(t, svc, "Action=CreateInternetGateway")
	body := must200(t, w)
	igwID := extractBetween(body, "<internetGatewayId>", "</internetGatewayId>")

	w2 := ec2Req(t, svc, "Action=DeleteInternetGateway&InternetGatewayId="+igwID)
	must200(t, w2)

	svc.mu.RLock()
	_, exists := svc.igws[igwID]
	svc.mu.RUnlock()
	if exists {
		t.Error("expected IGW to be deleted")
	}
}

// ── Security Groups ───────────────────────────────────────────────────────────

func TestCreateSecurityGroup(t *testing.T) {
	svc := newSvc()
	w := ec2Req(t, svc, "Action=CreateVpc&CidrBlock=10.0.0.0%2F16")
	vpcID := extractBetween(must200(t, w), "<vpcId>", "</vpcId>")

	w2 := ec2Req(t, svc,
		"Action=CreateSecurityGroup&GroupName=my-app&GroupDescription=my+app&VpcId="+vpcID)
	body := must200(t, w2)
	mustContain(t, body, "CreateSecurityGroupResponse")
	sgID := extractBetween(body, "<groupId>", "</groupId>")
	if sgID == "" || !strings.HasPrefix(sgID, "sg-") {
		t.Fatalf("expected a sg- groupId, got %q", sgID)
	}

	w3 := ec2Req(t, svc, "Action=DescribeSecurityGroups&GroupId.1="+sgID)
	body3 := must200(t, w3)
	mustContain(t, body3, sgID)
	mustContain(t, body3, "my-app")
	mustContain(t, body3, vpcID)
}

func TestDeleteSecurityGroup(t *testing.T) {
	svc := newSvc()
	w := ec2Req(t, svc, "Action=CreateVpc&CidrBlock=10.0.0.0%2F16")
	vpcID := extractBetween(must200(t, w), "<vpcId>", "</vpcId>")

	w2 := ec2Req(t, svc,
		"Action=CreateSecurityGroup&GroupName=my-app&GroupDescription=my+app&VpcId="+vpcID)
	sgID := extractBetween(must200(t, w2), "<groupId>", "</groupId>")

	w3 := ec2Req(t, svc, "Action=DeleteSecurityGroup&GroupId="+sgID)
	mustContain(t, must200(t, w3), "<return>true</return>")

	w4 := ec2Req(t, svc, "Action=DescribeSecurityGroups&GroupId.1="+sgID)
	body4 := must200(t, w4)
	if strings.Contains(body4, sgID) {
		t.Errorf("expected security group %s to be gone after delete", sgID)
	}
}

func TestDescribeSecurityGroups(t *testing.T) {
	svc := newSvc()
	// Create a VPC so the default SG exists.
	w := ec2Req(t, svc, "Action=CreateVpc&CidrBlock=10.0.0.0%2F16")
	body := must200(t, w)
	vpcID := extractBetween(body, "<vpcId>", "</vpcId>")

	w2 := ec2Req(t, svc, "Action=DescribeSecurityGroups&Filter.1.Name=vpc-id&Filter.1.Value.1="+vpcID)
	body2 := must200(t, w2)
	mustContain(t, body2, "sg-")
	mustContain(t, body2, "default")
	mustContain(t, body2, vpcID)
}

func TestAuthorizeRevokeSGIngress(t *testing.T) {
	svc := newSvc()
	ec2Req(t, svc, "Action=CreateVpc&CidrBlock=10.0.0.0%2F16")

	svc.mu.RLock()
	var sgID string
	for id := range svc.secGroups {
		sgID = id
	}
	svc.mu.RUnlock()

	// Authorize ingress.
	ec2Req(t, svc,
		"Action=AuthorizeSecurityGroupIngress&GroupId="+sgID+
			"&IpPermissions.1.IpProtocol=-1&IpPermissions.1.FromPort=0&IpPermissions.1.ToPort=0"+
			"&IpPermissions.1.IpRanges.1.CidrIp=10.0.0.0%2F16")

	svc.mu.RLock()
	sg := svc.secGroups[sgID]
	ingressLen := len(sg.ingress)
	svc.mu.RUnlock()
	if ingressLen != 1 {
		t.Errorf("expected 1 ingress rule, got %d", ingressLen)
	}

	// Revoke ingress.
	ec2Req(t, svc,
		"Action=RevokeSecurityGroupIngress&GroupId="+sgID+
			"&IpPermissions.1.IpProtocol=-1&IpPermissions.1.FromPort=0&IpPermissions.1.ToPort=0"+
			"&IpPermissions.1.IpRanges.1.CidrIp=10.0.0.0%2F16")

	svc.mu.RLock()
	sg = svc.secGroups[sgID]
	ingressLen = len(sg.ingress)
	svc.mu.RUnlock()
	if ingressLen != 0 {
		t.Errorf("expected 0 ingress rules after revoke, got %d", ingressLen)
	}
}

func TestDescribeSecurityGroupRules(t *testing.T) {
	svc := newSvc()
	ec2Req(t, svc, "Action=CreateVpc&CidrBlock=10.0.0.0%2F16")

	svc.mu.RLock()
	var sgID string
	for id := range svc.secGroups {
		sgID = id
	}
	svc.mu.RUnlock()

	// Add an ingress rule first.
	ec2Req(t, svc,
		"Action=AuthorizeSecurityGroupIngress&GroupId="+sgID+
			"&IpPermissions.1.IpProtocol=-1&IpPermissions.1.FromPort=0&IpPermissions.1.ToPort=0"+
			"&IpPermissions.1.IpRanges.1.CidrIp=10.0.0.0%2F16")

	w := ec2Req(t, svc, "Action=DescribeSecurityGroupRules"+
		"&Filter.1.Name=group-id&Filter.1.Value.1="+sgID)
	body := must200(t, w)
	mustContain(t, body, "DescribeSecurityGroupRulesResponse")
	mustContain(t, body, "sgr-")
	mustContain(t, body, sgID)
	mustContain(t, body, "<isEgress>false</isEgress>")
}

func TestDescribeSecurityGroupRulesEmpty(t *testing.T) {
	svc := newSvc()
	ec2Req(t, svc, "Action=CreateVpc&CidrBlock=10.0.0.0%2F16")

	svc.mu.RLock()
	var sgID string
	for id := range svc.secGroups {
		sgID = id
	}
	svc.mu.RUnlock()

	// Fresh SG has no rules — response should succeed with empty set.
	w := ec2Req(t, svc, "Action=DescribeSecurityGroupRules"+
		"&Filter.1.Name=group-id&Filter.1.Value.1="+sgID)
	body := must200(t, w)
	mustContain(t, body, "securityGroupRuleSet")
}

func TestAuthorizeRevokeSGEgress(t *testing.T) {
	svc := newSvc()
	ec2Req(t, svc, "Action=CreateVpc&CidrBlock=10.0.0.0%2F16")

	svc.mu.RLock()
	var sgID string
	for id := range svc.secGroups {
		sgID = id
	}
	svc.mu.RUnlock()

	ec2Req(t, svc,
		"Action=AuthorizeSecurityGroupEgress&GroupId="+sgID+
			"&IpPermissions.1.IpProtocol=-1&IpPermissions.1.FromPort=0&IpPermissions.1.ToPort=0"+
			"&IpPermissions.1.IpRanges.1.CidrIp=0.0.0.0%2F0")

	svc.mu.RLock()
	sg := svc.secGroups[sgID]
	egressLen := len(sg.egress)
	svc.mu.RUnlock()
	if egressLen != 1 {
		t.Errorf("expected 1 egress rule, got %d", egressLen)
	}

	ec2Req(t, svc,
		"Action=RevokeSecurityGroupEgress&GroupId="+sgID+
			"&IpPermissions.1.IpProtocol=-1&IpPermissions.1.FromPort=0&IpPermissions.1.ToPort=0"+
			"&IpPermissions.1.IpRanges.1.CidrIp=0.0.0.0%2F0")

	svc.mu.RLock()
	sg = svc.secGroups[sgID]
	egressLen = len(sg.egress)
	svc.mu.RUnlock()
	if egressLen != 0 {
		t.Errorf("expected 0 egress rules after revoke, got %d", egressLen)
	}
}

// ── Route Tables ──────────────────────────────────────────────────────────────

func TestCreateRouteTable(t *testing.T) {
	svc := newSvc()
	w := ec2Req(t, svc, "Action=CreateRouteTable&VpcId=vpc-rt-test")
	body := must200(t, w)
	mustContain(t, body, "CreateRouteTableResponse")
	mustContain(t, body, "rtb-")
	mustContain(t, body, "vpc-rt-test")
}

func TestDescribeRouteTablesById(t *testing.T) {
	svc := newSvc()
	w := ec2Req(t, svc, "Action=CreateRouteTable&VpcId=vpc-rt-test")
	body := must200(t, w)
	rtID := extractBetween(body, "<routeTableId>", "</routeTableId>")

	w2 := ec2Req(t, svc, "Action=DescribeRouteTables&RouteTableId.1="+rtID)
	body2 := must200(t, w2)
	mustContain(t, body2, rtID)
	mustContain(t, body2, "vpc-rt-test")
}

func TestCreateAndDeleteRoute(t *testing.T) {
	svc := newSvc()
	w := ec2Req(t, svc, "Action=CreateRouteTable&VpcId=vpc-x")
	body := must200(t, w)
	rtID := extractBetween(body, "<routeTableId>", "</routeTableId>")

	w2 := ec2Req(t, svc,
		"Action=CreateRoute&RouteTableId="+rtID+
			"&DestinationCidrBlock=0.0.0.0%2F0&GatewayId=igw-test")
	body2 := must200(t, w2)
	mustContain(t, body2, "<return>true</return>")

	svc.mu.RLock()
	rt := svc.routeTables[rtID]
	routeCount := len(rt.routes)
	svc.mu.RUnlock()
	if routeCount != 2 { // local + added route
		t.Errorf("expected 2 routes (local + added), got %d", routeCount)
	}

	// Delete the added route.
	ec2Req(t, svc, "Action=DeleteRoute&RouteTableId="+rtID+"&DestinationCidrBlock=0.0.0.0%2F0")

	svc.mu.RLock()
	rt = svc.routeTables[rtID]
	routeCount = len(rt.routes)
	svc.mu.RUnlock()
	if routeCount != 1 { // only local route remains
		t.Errorf("expected 1 route after delete, got %d", routeCount)
	}
}

func TestAssociateDisassociateRouteTable(t *testing.T) {
	svc := newSvc()
	w := ec2Req(t, svc, "Action=CreateRouteTable&VpcId=vpc-x")
	body := must200(t, w)
	rtID := extractBetween(body, "<routeTableId>", "</routeTableId>")

	w2 := ec2Req(t, svc,
		"Action=AssociateRouteTable&RouteTableId="+rtID+"&SubnetId=subnet-assoc-test")
	body2 := must200(t, w2)
	mustContain(t, body2, "rtbassoc-")
	assocID := extractBetween(body2, "<associationId>", "</associationId>")

	// Association should appear in DescribeRouteTables.
	w3 := ec2Req(t, svc, "Action=DescribeRouteTables&RouteTableId.1="+rtID)
	body3 := must200(t, w3)
	mustContain(t, body3, assocID)
	mustContain(t, body3, "subnet-assoc-test")

	// Disassociate.
	w4 := ec2Req(t, svc, "Action=DisassociateRouteTable&AssociationId="+assocID)
	must200(t, w4)

	svc.mu.RLock()
	_, exists := svc.associations[assocID]
	svc.mu.RUnlock()
	if exists {
		t.Error("expected association to be deleted")
	}
}

func TestDeleteRouteTable(t *testing.T) {
	svc := newSvc()
	w := ec2Req(t, svc, "Action=CreateRouteTable&VpcId=vpc-x")
	body := must200(t, w)
	rtID := extractBetween(body, "<routeTableId>", "</routeTableId>")

	// Associate so we can verify cleanup.
	w2 := ec2Req(t, svc, "Action=AssociateRouteTable&RouteTableId="+rtID+"&SubnetId=subnet-x")
	body2 := must200(t, w2)
	assocID := extractBetween(body2, "<associationId>", "</associationId>")

	ec2Req(t, svc, "Action=DeleteRouteTable&RouteTableId="+rtID)

	svc.mu.RLock()
	_, rtExists := svc.routeTables[rtID]
	_, assocExists := svc.associations[assocID]
	svc.mu.RUnlock()
	if rtExists {
		t.Error("expected route table to be deleted")
	}
	if assocExists {
		t.Error("expected association to be cleaned up when route table was deleted")
	}
}

// ── Tags ──────────────────────────────────────────────────────────────────────

func TestCreateTagsOnVpc(t *testing.T) {
	svc := newSvc()
	w := ec2Req(t, svc, "Action=CreateVpc&CidrBlock=10.0.0.0%2F16")
	body := must200(t, w)
	vpcID := extractBetween(body, "<vpcId>", "</vpcId>")

	w2 := ec2Req(t, svc,
		"Action=CreateTags&ResourceId.1="+vpcID+
			"&Tag.1.Key=forge%3Aapp&Tag.1.Value=my-app")
	must200(t, w2)

	svc.mu.RLock()
	v := svc.vpcs[vpcID]
	tagVal := v.tags["forge:app"]
	svc.mu.RUnlock()
	if tagVal != "my-app" {
		t.Errorf("expected tag forge:app=my-app, got %q", tagVal)
	}
}

func TestDeleteTagsOnVpc(t *testing.T) {
	svc := newSvc()
	w := ec2Req(t, svc, "Action=CreateVpc&CidrBlock=10.0.0.0%2F16")
	body := must200(t, w)
	vpcID := extractBetween(body, "<vpcId>", "</vpcId>")

	ec2Req(t, svc,
		"Action=CreateTags&ResourceId.1="+vpcID+"&Tag.1.Key=env&Tag.1.Value=test")
	ec2Req(t, svc,
		"Action=DeleteTags&ResourceId.1="+vpcID+"&Tag.1.Key=env")

	svc.mu.RLock()
	v := svc.vpcs[vpcID]
	_, exists := v.tags["env"]
	svc.mu.RUnlock()
	if exists {
		t.Error("expected tag 'env' to be deleted")
	}
}

// ── Network Interfaces ────────────────────────────────────────────────────────

func TestDescribeNetworkInterfacesEmpty(t *testing.T) {
	svc := newSvc()
	w := ec2Req(t, svc, "Action=DescribeNetworkInterfaces")
	body := must200(t, w)
	mustContain(t, body, "DescribeNetworkInterfacesResponse")
	mustContain(t, body, "networkInterfaceSet")
}

func TestDescribeNetworkInterfacesWithSubnetFilter(t *testing.T) {
	svc := newSvc()
	w := ec2Req(t, svc,
		"Action=DescribeNetworkInterfaces"+
			"&Filter.1.Name=subnet-id&Filter.1.Value.1=subnet-abc123")
	body := must200(t, w)
	mustContain(t, body, "DescribeNetworkInterfacesResponse")
	mustContain(t, body, "networkInterfaceSet")
}

func TestDescribeNetworkInterfacesWithVpcFilter(t *testing.T) {
	svc := newSvc()
	w := ec2Req(t, svc,
		"Action=DescribeNetworkInterfaces"+
			"&Filter.1.Name=vpc-id&Filter.1.Value.1=vpc-abc123")
	body := must200(t, w)
	mustContain(t, body, "DescribeNetworkInterfacesResponse")
	mustContain(t, body, "networkInterfaceSet")
}

// ── Unsupported action ────────────────────────────────────────────────────────

func TestUnsupportedAction(t *testing.T) {
	svc := newSvc()
	w := ec2Req(t, svc, "Action=DescribeInstances")
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "UnsupportedOperation") {
		t.Error("expected UnsupportedOperation in error response")
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// extractBetween returns the first string between start and end in s.
func extractBetween(s, start, end string) string {
	i := strings.Index(s, start)
	if i < 0 {
		return ""
	}
	s = s[i+len(start):]
	j := strings.Index(s, end)
	if j < 0 {
		return s
	}
	return s[:j]
}
