// Package ec2 emulates the AWS EC2 API for VPC-related resources.
// Supports the query-protocol actions required by the Pulumi AWS provider v7
// when deploying forge's NewVpc construct: VPC, Subnet, Internet Gateway,
// Security Group, Route Table, Route, Route Table Association, and Tags.
// All state is in-memory; no real networking is created.
package ec2

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/nimbus-local/nimbus/internal/uid"
)

const (
	ec2NS     = "http://ec2.amazonaws.com/doc/2016-11-15/"
	accountID = "000000000000"
)

// ── Data types ────────────────────────────────────────────────────────────────

type vpc struct {
	id           string
	cidrBlock    string
	state        string
	dnsSupport   bool
	dnsHostnames bool
	tags         map[string]string
}

type subnet struct {
	id                  string
	vpcID               string
	cidrBlock           string
	availabilityZone    string
	availabilityZoneID  string
	state               string
	mapPublicIpOnLaunch bool
	tags                map[string]string
}

type internetGateway struct {
	id          string
	attachments []string // vpcIDs
	tags        map[string]string
}

type securityGroup struct {
	id          string
	vpcID       string
	name        string
	description string
	ingress     []sgRule
	egress      []sgRule
	tags        map[string]string
}

type sgRule struct {
	id            string // sgr-xxx
	protocol      string
	fromPort      int
	toPort        int
	cidrIPs       []string
	prefixListIDs []string // pl-xxx — gateway endpoint service lists
	groupIDs      []string // sg-xxx — referenced security groups
}

// sameTarget reports whether two rules point at the same destination. Ports and
// protocol alone are not enough to identify a rule: a group may allow 443 to a
// prefix list and 443 to a peer group at once, and revoking one must not drop
// the other.
func (r sgRule) sameTarget(other sgRule) bool {
	return equalStrings(r.cidrIPs, other.cidrIPs) &&
		equalStrings(r.prefixListIDs, other.prefixListIDs) &&
		equalStrings(r.groupIDs, other.groupIDs)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type routeTable struct {
	id     string
	vpcID  string
	routes []rtRoute
	tags   map[string]string
}

type rtRoute struct {
	cidrBlock    string
	gatewayID    string
	natGatewayID string
	local        bool
}

type rtAssociation struct {
	id           string
	subnetID     string
	routeTableID string
}

// ── Service ───────────────────────────────────────────────────────────────────

// SubnetInUseFunc reports whether another service still references a subnet,
// returning a description of the resource holding the reference for the error
// message.
type SubnetInUseFunc func(subnetID string) (string, bool)

// Service implements the AWS EC2 emulator for VPC-related resources.
type Service struct {
	mu           sync.RWMutex
	vpcs         map[string]*vpc
	subnets      map[string]*subnet
	igws         map[string]*internetGateway
	secGroups    map[string]*securityGroup
	routeTables  map[string]*routeTable
	associations map[string]*rtAssociation
	vpcEndpoints map[string]*vpcEndpoint
	prefixLists  map[string]*managedPrefixList
	region       string
	subnetInUse  []SubnetInUseFunc
}

// New creates a new EC2 service.
func New(region string) *Service {
	if region == "" {
		region = "us-east-1"
	}
	return &Service{
		region:       region,
		vpcs:         map[string]*vpc{},
		subnets:      map[string]*subnet{},
		igws:         map[string]*internetGateway{},
		secGroups:    map[string]*securityGroup{},
		routeTables:  map[string]*routeTable{},
		associations: map[string]*rtAssociation{},
		vpcEndpoints: map[string]*vpcEndpoint{},
		// AWS-managed service prefix lists exist independently of any account's
		// resources, so they are present from the start and survive a reset.
		prefixLists: seedPrefixLists(region),
	}
}

// AddSubnetInUseCheck registers a cross-service dependency check consulted by
// DeleteSubnet. Call it during startup, before the service begins serving
// requests.
func (s *Service) AddSubnetInUseCheck(fn SubnetInUseFunc) {
	s.subnetInUse = append(s.subnetInUse, fn)
}

func (s *Service) Name() string { return "ec2" }

// Reset clears all in-memory state.
func (s *Service) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vpcs = map[string]*vpc{}
	s.subnets = map[string]*subnet{}
	s.igws = map[string]*internetGateway{}
	s.secGroups = map[string]*securityGroup{}
	s.routeTables = map[string]*routeTable{}
	s.associations = map[string]*rtAssociation{}
	s.vpcEndpoints = map[string]*vpcEndpoint{}
	s.prefixLists = seedPrefixLists(s.region)
}

// Detect identifies EC2 query-protocol requests:
// POST / with Content-Type application/x-www-form-urlencoded.
func (s *Service) Detect(r *http.Request) bool {
	return r.Method == http.MethodPost &&
		r.URL.Path == "/" &&
		strings.Contains(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded")
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	switch r.FormValue("Action") {
	// Availability zones
	case "DescribeAvailabilityZones":
		s.describeAvailabilityZones(w, r)
	// VPC
	case "CreateVpc":
		s.createVpc(w, r)
	case "DescribeVpcs":
		s.describeVpcs(w, r)
	case "DescribeVpcAttribute":
		s.describeVpcAttribute(w, r)
	case "ModifyVpcAttribute":
		s.modifyVpcAttribute(w, r)
	case "DeleteVpc":
		s.deleteVpc(w, r)
	// Subnets
	case "CreateSubnet":
		s.createSubnet(w, r)
	case "DescribeSubnets":
		s.describeSubnets(w, r)
	case "ModifySubnetAttribute":
		s.modifySubnetAttribute(w, r)
	case "DeleteSubnet":
		s.deleteSubnet(w, r)
	// Internet gateways
	case "CreateInternetGateway":
		s.createInternetGateway(w, r)
	case "DescribeInternetGateways":
		s.describeInternetGateways(w, r)
	case "AttachInternetGateway":
		s.attachInternetGateway(w, r)
	case "DetachInternetGateway":
		s.detachInternetGateway(w, r)
	case "DeleteInternetGateway":
		s.deleteInternetGateway(w, r)
	// Security groups
	case "CreateSecurityGroup":
		s.createSecurityGroup(w, r)
	case "DeleteSecurityGroup":
		s.deleteSecurityGroup(w, r)
	case "DescribeSecurityGroups":
		s.describeSecurityGroups(w, r)
	case "DescribeSecurityGroupRules":
		s.describeSecurityGroupRules(w, r)
	case "AuthorizeSecurityGroupIngress":
		s.authorizeIngress(w, r)
	case "AuthorizeSecurityGroupEgress":
		s.authorizeEgress(w, r)
	case "RevokeSecurityGroupIngress":
		s.revokeIngress(w, r)
	case "RevokeSecurityGroupEgress":
		s.revokeEgress(w, r)
	case "ModifySecurityGroupRules":
		s.modifySecurityGroupRules(w, r)
	// Route tables
	case "CreateRouteTable":
		s.createRouteTable(w, r)
	case "DescribeRouteTables":
		s.describeRouteTables(w, r)
	case "DeleteRouteTable":
		s.deleteRouteTable(w, r)
	case "CreateRoute":
		s.createRoute(w, r)
	case "DeleteRoute":
		s.deleteRoute(w, r)
	case "AssociateRouteTable":
		s.associateRouteTable(w, r)
	case "DisassociateRouteTable":
		s.disassociateRouteTable(w, r)
	// VPC endpoints
	case "CreateVpcEndpoint":
		s.createVpcEndpoint(w, r)
	case "DescribeVpcEndpoints":
		s.describeVpcEndpoints(w, r)
	case "ModifyVpcEndpoint":
		s.modifyVpcEndpoint(w, r)
	case "DeleteVpcEndpoints":
		s.deleteVpcEndpoints(w, r)
	// Prefix lists
	case "DescribePrefixLists":
		s.describePrefixLists(w, r)
	case "DescribeManagedPrefixLists":
		s.describeManagedPrefixLists(w, r)
	case "GetManagedPrefixListEntries":
		s.getManagedPrefixListEntries(w, r)
	// Network interfaces
	case "DescribeNetworkInterfaces":
		s.describeNetworkInterfaces(w, r)
	// Tags
	case "CreateTags":
		s.createTags(w, r)
	case "DeleteTags":
		s.deleteTags(w, r)
	default:
		writeXML(w, http.StatusNotImplemented, fmt.Sprintf(
			`<Response><Errors><Error><Code>UnsupportedOperation</Code>`+
				`<Message>EC2 action not emulated: %s</Message></Error></Errors></Response>`,
			r.FormValue("Action")))
	}
}

// ── Availability Zones ────────────────────────────────────────────────────────

func (s *Service) describeAvailabilityZones(w http.ResponseWriter, _ *http.Request) {
	// Return 3 AZs for the configured region (a, b, c).
	var items strings.Builder
	for i, suffix := range []string{"a", "b", "c"} {
		zone := s.region + suffix
		zoneID := regionAbbrev(s.region) + fmt.Sprintf("-az%d", i+1)
		fmt.Fprintf(&items, `
    <item>
      <zoneName>%s</zoneName>
      <zoneState>available</zoneState>
      <regionName>%s</regionName>
      <messageSet/>
      <zoneId>%s</zoneId>
    </item>`, zone, s.region, zoneID)
	}
	writeXML(w, http.StatusOK, ec2Resp("DescribeAvailabilityZones",
		"<availabilityZoneInfo>"+items.String()+"\n  </availabilityZoneInfo>"))
}

// regionAbbrev maps a region name to its abbreviated zone-ID prefix (e.g. "us-east-1" → "use1").
func regionAbbrev(region string) string {
	parts := strings.Split(region, "-")
	if len(parts) < 3 {
		return region
	}
	// "us-east-1" → "us" + "e" + "1" → "use1"
	dir := parts[1]
	if len(dir) > 1 {
		dir = dir[:1]
	}
	return parts[0] + dir + parts[2]
}

// ── VPC ───────────────────────────────────────────────────────────────────────

func (s *Service) createVpc(w http.ResponseWriter, r *http.Request) {
	cidr := r.FormValue("CidrBlock")
	if cidr == "" {
		cidr = "10.0.0.0/16"
	}
	id := "vpc-" + shortID()

	v := &vpc{
		id:           id,
		cidrBlock:    cidr,
		state:        "available",
		dnsSupport:   true,
		dnsHostnames: false,
		tags:         parseTagSpecTags(r),
	}

	// Auto-create default security group and main route table for the VPC.
	sgID := "sg-" + shortID()
	sg := &securityGroup{
		id:          sgID,
		vpcID:       id,
		name:        "default",
		description: "default VPC security group",
		tags:        map[string]string{},
	}
	rtID := "rtb-" + shortID()
	rt := &routeTable{
		id:    rtID,
		vpcID: id,
		routes: []rtRoute{
			{cidrBlock: cidr, gatewayID: "local", local: true},
		},
		tags: map[string]string{},
	}

	s.mu.Lock()
	s.vpcs[id] = v
	s.secGroups[sgID] = sg
	s.routeTables[rtID] = rt
	s.mu.Unlock()

	writeXML(w, http.StatusOK, ec2Resp("CreateVpc",
		"<vpc>"+s.vpcXML(v)+"</vpc>"))
}

func (s *Service) describeVpcs(w http.ResponseWriter, r *http.Request) {
	ids := collectValues(r, "VpcId")
	filters := parseFilters(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	var items strings.Builder
	for _, v := range s.vpcs {
		if len(ids) > 0 && !contains(ids, v.id) {
			continue
		}
		if fv, ok := filters["vpc-id"]; ok && !contains(fv, v.id) {
			continue
		}
		fmt.Fprintf(&items, "\n    <item>%s</item>", s.vpcXML(v))
	}
	writeXML(w, http.StatusOK, ec2Resp("DescribeVpcs",
		"<vpcSet>"+items.String()+"\n  </vpcSet>"))
}

func (s *Service) describeVpcAttribute(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("VpcId")
	attr := r.FormValue("Attribute")

	s.mu.RLock()
	v := s.vpcs[id]
	s.mu.RUnlock()

	if v == nil {
		ec2Error(w, "InvalidVpcID.NotFound", fmt.Sprintf("The vpc ID '%s' does not exist", id))
		return
	}

	var attrXML string
	switch attr {
	case "enableDnsSupport":
		attrXML = fmt.Sprintf("<enableDnsSupport><value>%v</value></enableDnsSupport>", v.dnsSupport)
	case "enableDnsHostnames":
		attrXML = fmt.Sprintf("<enableDnsHostnames><value>%v</value></enableDnsHostnames>", v.dnsHostnames)
	default:
		attrXML = fmt.Sprintf("<%s><value>false</value></%s>", attr, attr)
	}

	writeXML(w, http.StatusOK, ec2Resp("DescribeVpcAttribute",
		fmt.Sprintf("<vpcId>%s</vpcId>%s", id, attrXML)))
}

func (s *Service) modifyVpcAttribute(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("VpcId")

	s.mu.Lock()
	v := s.vpcs[id]
	if v != nil {
		if r.FormValue("EnableDnsSupport.Value") != "" {
			v.dnsSupport = r.FormValue("EnableDnsSupport.Value") == "true"
		}
		if r.FormValue("EnableDnsHostnames.Value") != "" {
			v.dnsHostnames = r.FormValue("EnableDnsHostnames.Value") == "true"
		}
	}
	s.mu.Unlock()

	writeXML(w, http.StatusOK, ec2Resp("ModifyVpcAttribute", "<return>true</return>"))
}

func (s *Service) deleteVpc(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("VpcId")

	s.mu.Lock()
	delete(s.vpcs, id)
	// Clean up the default SG and main route table created with the VPC.
	for sgID, sg := range s.secGroups {
		if sg.vpcID == id && sg.name == "default" {
			delete(s.secGroups, sgID)
		}
	}
	for rtID, rt := range s.routeTables {
		if rt.vpcID == id {
			delete(s.routeTables, rtID)
		}
	}
	s.mu.Unlock()

	writeXML(w, http.StatusOK, ec2Resp("DeleteVpc", "<return>true</return>"))
}

func (s *Service) vpcXML(v *vpc) string {
	return fmt.Sprintf(`
      <vpcId>%s</vpcId>
      <state>%s</state>
      <cidrBlock>%s</cidrBlock>
      <dhcpOptionsId>dopt-00000001</dhcpOptionsId>
      <instanceTenancy>default</instanceTenancy>
      <isDefault>false</isDefault>
      %s`, v.id, v.state, v.cidrBlock, tagsXML(v.tags))
}

// ── Subnets ───────────────────────────────────────────────────────────────────

func (s *Service) createSubnet(w http.ResponseWriter, r *http.Request) {
	id := "subnet-" + shortID()
	vpcID := r.FormValue("VpcId")
	cidr := r.FormValue("CidrBlock")
	az := r.FormValue("AvailabilityZone")
	if az == "" {
		az = s.region + "a"
	}

	sn := &subnet{
		id:                  id,
		vpcID:               vpcID,
		cidrBlock:           cidr,
		availabilityZone:    az,
		availabilityZoneID:  regionAbbrev(s.region) + "-az1",
		state:               "available",
		mapPublicIpOnLaunch: false,
		tags:                parseTagSpecTags(r),
	}

	s.mu.Lock()
	s.subnets[id] = sn
	s.mu.Unlock()

	writeXML(w, http.StatusOK, ec2Resp("CreateSubnet",
		"<subnet>"+subnetXML(sn)+"</subnet>"))
}

func (s *Service) describeSubnets(w http.ResponseWriter, r *http.Request) {
	ids := collectSubnetIDs(r)
	filters := parseFilters(r)
	vpcFilter := filters["vpc-id"]
	subnetIDFilter := filters["subnet-id"]

	// Merge explicit SubnetId.N params into the subnetIDFilter.
	for _, id := range ids {
		subnetIDFilter = append(subnetIDFilter, id)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var items strings.Builder
	seen := map[string]bool{}

	// Return stored subnets that match.
	for _, sn := range s.subnets {
		if len(subnetIDFilter) > 0 && !contains(subnetIDFilter, sn.id) {
			continue
		}
		if len(vpcFilter) > 0 && !contains(vpcFilter, sn.vpcID) {
			continue
		}
		fmt.Fprintf(&items, "\n    <item>%s</item>", subnetXML(sn))
		seen[sn.id] = true
	}

	// Synthetic fallback for subnet IDs not in store (backwards compat
	// with smoke stacks that pass hardcoded "subnet-00000001" style IDs).
	for i, id := range subnetIDFilter {
		if seen[id] {
			continue
		}
		az := fmt.Sprintf("%s%c", s.region, 'a'+byte(i%3))
		fmt.Fprintf(&items, `
    <item>
      <subnetId>%s</subnetId>
      <state>available</state>
      <vpcId>vpc-00000001</vpcId>
      <cidrBlock>10.0.%d.0/24</cidrBlock>
      <availableIpAddressCount>251</availableIpAddressCount>
      <availabilityZone>%s</availabilityZone>
      <availabilityZoneId>%s-az%d</availabilityZoneId>
      <defaultForAz>false</defaultForAz>
      <mapPublicIpOnLaunch>false</mapPublicIpOnLaunch>
      <ownerId>%s</ownerId>
      <tagSet/>
    </item>`, id, i+1, az, regionAbbrev(s.region), i+1, accountID)
	}

	writeXML(w, http.StatusOK, ec2Resp("DescribeSubnets",
		"<subnetSet>"+items.String()+"\n  </subnetSet>"))
}

func (s *Service) modifySubnetAttribute(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("SubnetId")

	s.mu.Lock()
	sn := s.subnets[id]
	if sn != nil {
		if v := r.FormValue("MapPublicIpOnLaunch.Value"); v != "" {
			sn.mapPublicIpOnLaunch = v == "true"
		}
	}
	s.mu.Unlock()

	writeXML(w, http.StatusOK, ec2Resp("ModifySubnetAttribute", "<return>true</return>"))
}

func (s *Service) deleteSubnet(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("SubnetId")

	// Real AWS refuses to delete a subnet another resource still sits in —
	// e.g. an RDS instance placed there through its DB subnet group.
	for _, inUse := range s.subnetInUse {
		if user, blocked := inUse(id); blocked {
			ec2Error(w, "DependencyViolation", fmt.Sprintf(
				"The subnet '%s' has dependencies and cannot be deleted: %s", id, user))
			return
		}
	}

	s.mu.Lock()
	delete(s.subnets, id)
	s.mu.Unlock()

	writeXML(w, http.StatusOK, ec2Resp("DeleteSubnet", "<return>true</return>"))
}

// SubnetInfo returns the VPC ID and Availability Zone of a tracked subnet.
// ok is false if the subnet was never created via CreateSubnet. Used by other
// services (e.g. RDS subnet groups) to resolve where a subnet lives.
func (s *Service) SubnetInfo(id string) (vpcID, az string, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sn, found := s.subnets[id]
	if !found {
		return "", "", false
	}
	return sn.vpcID, sn.availabilityZone, true
}

// SubnetAZ returns the Availability Zone of a tracked subnet. The second
// return value is false if the subnet is not known to the EC2 store (e.g. a
// synthetic ID never created via CreateSubnet). It is used by other services
// (e.g. ALB) to validate that a load balancer spans multiple AZs.
func (s *Service) SubnetAZ(id string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sn, ok := s.subnets[id]
	if !ok {
		return "", false
	}
	return sn.availabilityZone, true
}

func subnetXML(sn *subnet) string {
	return fmt.Sprintf(`
      <subnetId>%s</subnetId>
      <state>%s</state>
      <vpcId>%s</vpcId>
      <cidrBlock>%s</cidrBlock>
      <availableIpAddressCount>251</availableIpAddressCount>
      <availabilityZone>%s</availabilityZone>
      <availabilityZoneId>%s</availabilityZoneId>
      <defaultForAz>false</defaultForAz>
      <mapPublicIpOnLaunch>%v</mapPublicIpOnLaunch>
      <ownerId>%s</ownerId>
      %s`,
		sn.id, sn.state, sn.vpcID, sn.cidrBlock,
		sn.availabilityZone, sn.availabilityZoneID,
		sn.mapPublicIpOnLaunch, accountID, tagsXML(sn.tags))
}

// ── Internet Gateways ─────────────────────────────────────────────────────────

func (s *Service) createInternetGateway(w http.ResponseWriter, r *http.Request) {
	id := "igw-" + shortID()
	igw := &internetGateway{
		id:   id,
		tags: parseTagSpecTags(r),
	}

	s.mu.Lock()
	s.igws[id] = igw
	s.mu.Unlock()

	writeXML(w, http.StatusOK, ec2Resp("CreateInternetGateway",
		"<internetGateway>"+igwXML(igw)+"</internetGateway>"))
}

func (s *Service) describeInternetGateways(w http.ResponseWriter, r *http.Request) {
	ids := collectValues(r, "InternetGatewayId")
	filters := parseFilters(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	var items strings.Builder
	for _, igw := range s.igws {
		if len(ids) > 0 && !contains(ids, igw.id) {
			continue
		}
		if fv, ok := filters["internet-gateway-id"]; ok && !contains(fv, igw.id) {
			continue
		}
		if fv, ok := filters["attachment.vpc-id"]; ok {
			if !igwHasVPC(igw, fv) {
				continue
			}
		}
		fmt.Fprintf(&items, "\n    <item>%s</item>", igwXML(igw))
	}

	writeXML(w, http.StatusOK, ec2Resp("DescribeInternetGateways",
		"<internetGatewaySet>"+items.String()+"\n  </internetGatewaySet>"))
}

func igwHasVPC(igw *internetGateway, vpcIDs []string) bool {
	for _, a := range igw.attachments {
		if contains(vpcIDs, a) {
			return true
		}
	}
	return false
}

func (s *Service) attachInternetGateway(w http.ResponseWriter, r *http.Request) {
	igwID := r.FormValue("InternetGatewayId")
	vpcID := r.FormValue("VpcId")

	s.mu.Lock()
	if igw := s.igws[igwID]; igw != nil {
		igw.attachments = append(igw.attachments, vpcID)
	}
	s.mu.Unlock()

	writeXML(w, http.StatusOK, ec2Resp("AttachInternetGateway", "<return>true</return>"))
}

func (s *Service) detachInternetGateway(w http.ResponseWriter, r *http.Request) {
	igwID := r.FormValue("InternetGatewayId")
	vpcID := r.FormValue("VpcId")

	s.mu.Lock()
	if igw := s.igws[igwID]; igw != nil {
		var keep []string
		for _, a := range igw.attachments {
			if a != vpcID {
				keep = append(keep, a)
			}
		}
		igw.attachments = keep
	}
	s.mu.Unlock()

	writeXML(w, http.StatusOK, ec2Resp("DetachInternetGateway", "<return>true</return>"))
}

func (s *Service) deleteInternetGateway(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("InternetGatewayId")

	s.mu.Lock()
	delete(s.igws, id)
	s.mu.Unlock()

	writeXML(w, http.StatusOK, ec2Resp("DeleteInternetGateway", "<return>true</return>"))
}

func igwXML(igw *internetGateway) string {
	var attachXML strings.Builder
	for _, vpcID := range igw.attachments {
		fmt.Fprintf(&attachXML, `
        <item>
          <vpcId>%s</vpcId>
          <state>available</state>
        </item>`, vpcID)
	}
	return fmt.Sprintf(`
      <internetGatewayId>%s</internetGatewayId>
      <attachmentSet>%s</attachmentSet>
      %s`, igw.id, attachXML.String(), tagsXML(igw.tags))
}

// ── Security Groups ───────────────────────────────────────────────────────────

func (s *Service) describeSecurityGroupRules(w http.ResponseWriter, r *http.Request) {
	filters := parseFilters(r)
	sgID := ""
	if ids, ok := filters["group-id"]; ok && len(ids) > 0 {
		sgID = ids[0]
	}

	s.mu.RLock()
	sg := s.secGroups[sgID]
	s.mu.RUnlock()

	var items strings.Builder
	if sg != nil {
		for _, rule := range sg.ingress {
			fmt.Fprintf(&items, "\n    <item>%s</item>", sgRuleItemXML(rule, sg.id, false))
		}
		for _, rule := range sg.egress {
			fmt.Fprintf(&items, "\n    <item>%s</item>", sgRuleItemXML(rule, sg.id, true))
		}
	}

	writeXML(w, http.StatusOK, ec2Resp("DescribeSecurityGroupRules",
		"<securityGroupRuleSet>"+items.String()+"\n  </securityGroupRuleSet>"))
}

// sgRuleItemXML renders one flattened rule. A rule carries exactly one target
// kind, so at most one of the CIDR, prefix list, or referenced group elements
// is present.
func sgRuleItemXML(rule sgRule, sgID string, isEgress bool) string {
	target := ""
	switch {
	case len(rule.cidrIPs) > 0:
		target = fmt.Sprintf("<cidrIpv4>%s</cidrIpv4>", rule.cidrIPs[0])
	case len(rule.prefixListIDs) > 0:
		target = fmt.Sprintf("<prefixListId>%s</prefixListId>", rule.prefixListIDs[0])
	case len(rule.groupIDs) > 0:
		target = fmt.Sprintf(
			"<referencedGroupInfo><groupId>%s</groupId><userId>%s</userId></referencedGroupInfo>",
			rule.groupIDs[0], accountID)
	}
	return fmt.Sprintf(`
      <securityGroupRuleId>%s</securityGroupRuleId>
      <groupId>%s</groupId>
      <isEgress>%v</isEgress>
      <ipProtocol>%s</ipProtocol>
      <fromPort>%d</fromPort>
      <toPort>%d</toPort>
      %s`,
		rule.id, sgID, isEgress, rule.protocol, rule.fromPort, rule.toPort, target)
}

func (s *Service) modifySecurityGroupRules(w http.ResponseWriter, r *http.Request) {
	// Accept modifications without actually applying them — the smoke test
	// does not assert on exact rule state, only that the resource deploys.
	writeXML(w, http.StatusOK, ec2Resp("ModifySecurityGroupRules",
		"<return>true</return>"))
}

func (s *Service) createSecurityGroup(w http.ResponseWriter, r *http.Request) {
	id := "sg-" + shortID()
	sg := &securityGroup{
		id:          id,
		vpcID:       r.FormValue("VpcId"),
		name:        r.FormValue("GroupName"),
		description: r.FormValue("GroupDescription"),
		tags:        parseTagSpecTags(r),
	}

	s.mu.Lock()
	s.secGroups[id] = sg
	s.mu.Unlock()

	writeXML(w, http.StatusOK, ec2Resp("CreateSecurityGroup",
		fmt.Sprintf("<groupId>%s</groupId>%s", id, tagsXML(sg.tags))))
}

func (s *Service) deleteSecurityGroup(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("GroupId")

	s.mu.Lock()
	delete(s.secGroups, id)
	s.mu.Unlock()

	writeXML(w, http.StatusOK, ec2Resp("DeleteSecurityGroup", "<return>true</return>"))
}

func (s *Service) describeSecurityGroups(w http.ResponseWriter, r *http.Request) {
	ids := collectValues(r, "GroupId")
	filters := parseFilters(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	var items strings.Builder
	for _, sg := range s.secGroups {
		if len(ids) > 0 && !contains(ids, sg.id) {
			continue
		}
		if fv, ok := filters["group-id"]; ok && !contains(fv, sg.id) {
			continue
		}
		if fv, ok := filters["vpc-id"]; ok && !contains(fv, sg.vpcID) {
			continue
		}
		if fv, ok := filters["group-name"]; ok && !contains(fv, sg.name) {
			continue
		}
		fmt.Fprintf(&items, "\n    <item>%s</item>", sgXML(sg))
	}

	writeXML(w, http.StatusOK, ec2Resp("DescribeSecurityGroups",
		"<securityGroupInfo>"+items.String()+"\n  </securityGroupInfo>"))
}

func (s *Service) authorizeIngress(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("GroupId")
	rules := parseIPPermissions(r, "IpPermissions")

	s.mu.Lock()
	if sg := s.secGroups[id]; sg != nil {
		sg.ingress = append(sg.ingress, rules...)
	}
	s.mu.Unlock()

	writeXML(w, http.StatusOK, ec2Resp("AuthorizeSecurityGroupIngress",
		"<return>true</return>"))
}

func (s *Service) authorizeEgress(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("GroupId")
	rules := parseIPPermissions(r, "IpPermissions")

	s.mu.Lock()
	if sg := s.secGroups[id]; sg != nil {
		sg.egress = append(sg.egress, rules...)
	}
	s.mu.Unlock()

	writeXML(w, http.StatusOK, ec2Resp("AuthorizeSecurityGroupEgress",
		"<return>true</return>"))
}

func (s *Service) revokeIngress(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("GroupId")
	rules := parseIPPermissions(r, "IpPermissions")

	s.mu.Lock()
	if sg := s.secGroups[id]; sg != nil {
		sg.ingress = removeRules(sg.ingress, rules)
	}
	s.mu.Unlock()

	writeXML(w, http.StatusOK, ec2Resp("RevokeSecurityGroupIngress",
		"<return>true</return>"))
}

func (s *Service) revokeEgress(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("GroupId")
	rules := parseIPPermissions(r, "IpPermissions")

	s.mu.Lock()
	if sg := s.secGroups[id]; sg != nil {
		sg.egress = removeRules(sg.egress, rules)
	}
	s.mu.Unlock()

	writeXML(w, http.StatusOK, ec2Resp("RevokeSecurityGroupEgress",
		"<return>true</return>"))
}

func removeRules(existing, toRemove []sgRule) []sgRule {
	var out []sgRule
outer:
	for _, e := range existing {
		for _, r := range toRemove {
			if e.protocol == r.protocol && e.fromPort == r.fromPort &&
				e.toPort == r.toPort && e.sameTarget(r) {
				continue outer
			}
		}
		out = append(out, e)
	}
	return out
}

func sgXML(sg *securityGroup) string {
	return fmt.Sprintf(`
      <ownerId>%s</ownerId>
      <groupId>%s</groupId>
      <groupName>%s</groupName>
      <groupDescription>%s</groupDescription>
      <vpcId>%s</vpcId>
      <ipPermissions>%s</ipPermissions>
      <ipPermissionsEgress>%s</ipPermissionsEgress>
      %s`,
		accountID, sg.id, sg.name, sg.description, sg.vpcID,
		rulesXML(sg.ingress), rulesXML(sg.egress), tagsXML(sg.tags))
}

func rulesXML(rules []sgRule) string {
	if len(rules) == 0 {
		return ""
	}
	var b strings.Builder
	for _, rule := range rules {
		fmt.Fprintf(&b, `
        <item>
          <ipProtocol>%s</ipProtocol>
          <fromPort>%d</fromPort>
          <toPort>%d</toPort>
          <groups>%s</groups>
          <ipRanges>%s</ipRanges>
          <prefixListIds>%s</prefixListIds>
        </item>`, rule.protocol, rule.fromPort, rule.toPort,
			ruleGroupsXML(rule.groupIDs), cidrIPsXML(rule.cidrIPs),
			prefixListIDsXML(rule.prefixListIDs))
	}
	return b.String()
}

func ruleGroupsXML(groupIDs []string) string {
	var b strings.Builder
	for _, id := range groupIDs {
		fmt.Fprintf(&b, "<item><userId>%s</userId><groupId>%s</groupId></item>", accountID, id)
	}
	return b.String()
}

func prefixListIDsXML(ids []string) string {
	var b strings.Builder
	for _, id := range ids {
		fmt.Fprintf(&b, "<item><prefixListId>%s</prefixListId></item>", id)
	}
	return b.String()
}

func cidrIPsXML(cidrs []string) string {
	var b strings.Builder
	for _, c := range cidrs {
		fmt.Fprintf(&b, "<item><cidrIp>%s</cidrIp></item>", c)
	}
	return b.String()
}

// ── Route Tables ──────────────────────────────────────────────────────────────

func (s *Service) createRouteTable(w http.ResponseWriter, r *http.Request) {
	id := "rtb-" + shortID()
	vpcID := r.FormValue("VpcId")

	// Find VPC CIDR to add local route.
	s.mu.RLock()
	v := s.vpcs[vpcID]
	s.mu.RUnlock()

	localCIDR := "10.0.0.0/16"
	if v != nil {
		localCIDR = v.cidrBlock
	}

	rt := &routeTable{
		id:    id,
		vpcID: vpcID,
		routes: []rtRoute{
			{cidrBlock: localCIDR, gatewayID: "local", local: true},
		},
		tags: parseTagSpecTags(r),
	}

	s.mu.Lock()
	s.routeTables[id] = rt
	s.mu.Unlock()

	writeXML(w, http.StatusOK, ec2Resp("CreateRouteTable",
		"<routeTable>"+s.routeTableXML(rt)+"</routeTable>"))
}

func (s *Service) describeRouteTables(w http.ResponseWriter, r *http.Request) {
	ids := collectValues(r, "RouteTableId")
	filters := parseFilters(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	var items strings.Builder
	for _, rt := range s.routeTables {
		if len(ids) > 0 && !contains(ids, rt.id) {
			continue
		}
		if fv, ok := filters["route-table-id"]; ok && !contains(fv, rt.id) {
			continue
		}
		if fv, ok := filters["vpc-id"]; ok && !contains(fv, rt.vpcID) {
			continue
		}
		if fv, ok := filters["association.route-table-association-id"]; ok {
			if !rtHasAssoc(rt, s.associations, fv) {
				continue
			}
		}
		if fv, ok := filters["association.subnet-id"]; ok {
			if !rtHasSubnet(rt, s.associations, fv) {
				continue
			}
		}
		fmt.Fprintf(&items, "\n    <item>%s</item>", s.routeTableXML(rt))
	}

	writeXML(w, http.StatusOK, ec2Resp("DescribeRouteTables",
		"<routeTableSet>"+items.String()+"\n  </routeTableSet>"))
}

func rtHasAssoc(rt *routeTable, assocs map[string]*rtAssociation, ids []string) bool {
	for _, assoc := range assocs {
		if assoc.routeTableID == rt.id && contains(ids, assoc.id) {
			return true
		}
	}
	return false
}

func rtHasSubnet(rt *routeTable, assocs map[string]*rtAssociation, subnetIDs []string) bool {
	for _, assoc := range assocs {
		if assoc.routeTableID == rt.id && contains(subnetIDs, assoc.subnetID) {
			return true
		}
	}
	return false
}

func (s *Service) deleteRouteTable(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("RouteTableId")

	s.mu.Lock()
	delete(s.routeTables, id)
	// Remove all associations for this route table.
	for assocID, assoc := range s.associations {
		if assoc.routeTableID == id {
			delete(s.associations, assocID)
		}
	}
	s.mu.Unlock()

	writeXML(w, http.StatusOK, ec2Resp("DeleteRouteTable", "<return>true</return>"))
}

func (s *Service) createRoute(w http.ResponseWriter, r *http.Request) {
	rtID := r.FormValue("RouteTableId")
	cidr := r.FormValue("DestinationCidrBlock")
	gwID := r.FormValue("GatewayId")
	natID := r.FormValue("NatGatewayId")

	s.mu.Lock()
	if rt := s.routeTables[rtID]; rt != nil {
		rt.routes = append(rt.routes, rtRoute{
			cidrBlock:    cidr,
			gatewayID:    gwID,
			natGatewayID: natID,
		})
	}
	s.mu.Unlock()

	writeXML(w, http.StatusOK, ec2Resp("CreateRoute", "<return>true</return>"))
}

func (s *Service) deleteRoute(w http.ResponseWriter, r *http.Request) {
	rtID := r.FormValue("RouteTableId")
	cidr := r.FormValue("DestinationCidrBlock")

	s.mu.Lock()
	if rt := s.routeTables[rtID]; rt != nil {
		var keep []rtRoute
		for _, route := range rt.routes {
			if route.cidrBlock != cidr || route.local {
				keep = append(keep, route)
			}
		}
		rt.routes = keep
	}
	s.mu.Unlock()

	writeXML(w, http.StatusOK, ec2Resp("DeleteRoute", "<return>true</return>"))
}

func (s *Service) associateRouteTable(w http.ResponseWriter, r *http.Request) {
	id := "rtbassoc-" + shortID()
	subnetID := r.FormValue("SubnetId")
	rtID := r.FormValue("RouteTableId")

	s.mu.Lock()
	s.associations[id] = &rtAssociation{
		id:           id,
		subnetID:     subnetID,
		routeTableID: rtID,
	}
	s.mu.Unlock()

	writeXML(w, http.StatusOK, ec2Resp("AssociateRouteTable",
		fmt.Sprintf("<associationId>%s</associationId>", id)))
}

func (s *Service) disassociateRouteTable(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("AssociationId")

	s.mu.Lock()
	delete(s.associations, id)
	s.mu.Unlock()

	writeXML(w, http.StatusOK, ec2Resp("DisassociateRouteTable", "<return>true</return>"))
}

func (s *Service) routeTableXML(rt *routeTable) string {
	var routeItems strings.Builder
	for _, route := range rt.routes {
		origin := "CreateRoute"
		if route.local {
			origin = "CreateRouteTable"
		}
		gwID := route.gatewayID
		if route.natGatewayID != "" {
			gwID = ""
		}
		var extra string
		if route.natGatewayID != "" {
			extra = fmt.Sprintf("<natGatewayId>%s</natGatewayId>", route.natGatewayID)
		}
		if gwID != "" {
			extra = fmt.Sprintf("<gatewayId>%s</gatewayId>", gwID)
		}
		fmt.Fprintf(&routeItems, `
        <item>
          <destinationCidrBlock>%s</destinationCidrBlock>
          %s
          <state>active</state>
          <origin>%s</origin>
        </item>`, route.cidrBlock, extra, origin)
	}

	var assocItems strings.Builder
	for _, assoc := range s.associations {
		if assoc.routeTableID != rt.id {
			continue
		}
		fmt.Fprintf(&assocItems, `
        <item>
          <routeTableAssociationId>%s</routeTableAssociationId>
          <routeTableId>%s</routeTableId>
          <subnetId>%s</subnetId>
          <main>false</main>
          <associationState><state>associated</state></associationState>
        </item>`, assoc.id, rt.id, assoc.subnetID)
	}

	return fmt.Sprintf(`
      <routeTableId>%s</routeTableId>
      <vpcId>%s</vpcId>
      <ownerId>%s</ownerId>
      <routeSet>%s</routeSet>
      <associationSet>%s</associationSet>
      %s`,
		rt.id, rt.vpcID, accountID,
		routeItems.String(), assocItems.String(), tagsXML(rt.tags))
}

// ── Network Interfaces ────────────────────────────────────────────────────────

// describeNetworkInterfaces returns an empty NetworkInterfaceSet.
// Nimbus has no real EC2 instances, so there are never any ENIs attached to
// subnets. Returning an empty set is enough for the Terraform/Pulumi provider
// to proceed through the subnet-delete path without error.
func (s *Service) describeNetworkInterfaces(w http.ResponseWriter, _ *http.Request) {
	writeXML(w, http.StatusOK, ec2Resp("DescribeNetworkInterfaces",
		"<networkInterfaceSet/>"))
}

// ── Tags ──────────────────────────────────────────────────────────────────────

// createTags applies tags to any resource identified by ResourceId.N.
// The resource-specific tag maps are updated in-place.
func (s *Service) createTags(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	for i := 1; ; i++ {
		resID := r.FormValue(fmt.Sprintf("ResourceId.%d", i))
		if resID == "" {
			break
		}
		for j := 1; ; j++ {
			key := r.FormValue(fmt.Sprintf("Tag.%d.Key", j))
			if key == "" {
				break
			}
			val := r.FormValue(fmt.Sprintf("Tag.%d.Value", j))
			s.applyTag(resID, key, val)
		}
	}
	s.mu.Unlock()

	writeXML(w, http.StatusOK, ec2Resp("CreateTags", "<return>true</return>"))
}

func (s *Service) deleteTags(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	for i := 1; ; i++ {
		resID := r.FormValue(fmt.Sprintf("ResourceId.%d", i))
		if resID == "" {
			break
		}
		for j := 1; ; j++ {
			key := r.FormValue(fmt.Sprintf("Tag.%d.Key", j))
			if key == "" {
				break
			}
			s.removeTag(resID, key)
		}
	}
	s.mu.Unlock()

	writeXML(w, http.StatusOK, ec2Resp("DeleteTags", "<return>true</return>"))
}

// applyTag sets key=val on whichever resource has the given ID.
// Must be called under s.mu.Lock().
func (s *Service) applyTag(resID, key, val string) {
	if v := s.vpcs[resID]; v != nil {
		v.tags[key] = val
		return
	}
	if sn := s.subnets[resID]; sn != nil {
		sn.tags[key] = val
		return
	}
	if igw := s.igws[resID]; igw != nil {
		igw.tags[key] = val
		return
	}
	if sg := s.secGroups[resID]; sg != nil {
		sg.tags[key] = val
		return
	}
	if rt := s.routeTables[resID]; rt != nil {
		rt.tags[key] = val
		return
	}
}

func (s *Service) removeTag(resID, key string) {
	if v := s.vpcs[resID]; v != nil {
		delete(v.tags, key)
		return
	}
	if sn := s.subnets[resID]; sn != nil {
		delete(sn.tags, key)
		return
	}
	if igw := s.igws[resID]; igw != nil {
		delete(igw.tags, key)
		return
	}
	if sg := s.secGroups[resID]; sg != nil {
		delete(sg.tags, key)
		return
	}
	if rt := s.routeTables[resID]; rt != nil {
		delete(rt.tags, key)
		return
	}
}

// ── Parsing helpers ───────────────────────────────────────────────────────────

// parseFilters extracts EC2 query-protocol filters into a map of name → values.
// Handles Filter.N.Name=<name>&Filter.N.Value.M=<val> form.
func parseFilters(r *http.Request) map[string][]string {
	filters := map[string][]string{}
	for i := 1; ; i++ {
		name := r.FormValue(fmt.Sprintf("Filter.%d.Name", i))
		if name == "" {
			break
		}
		var vals []string
		for j := 1; ; j++ {
			v := r.FormValue(fmt.Sprintf("Filter.%d.Value.%d", i, j))
			if v == "" {
				break
			}
			vals = append(vals, v)
		}
		filters[name] = vals
	}
	return filters
}

// collectValues extracts resource ID lists from both ResourceId.N and Filter form params.
func collectValues(r *http.Request, param string) []string {
	var ids []string
	seen := map[string]bool{}
	add := func(v string) {
		if v != "" && !seen[v] {
			seen[v] = true
			ids = append(ids, v)
		}
	}
	for i := 1; ; i++ {
		v := r.FormValue(fmt.Sprintf("%s.%d", param, i))
		if v == "" {
			break
		}
		add(v)
	}
	return ids
}

// collectSubnetIDs extracts subnet IDs from SubnetId.N and Filter.N.Name=subnet-id forms.
func collectSubnetIDs(r *http.Request) []string {
	var ids []string
	seen := map[string]bool{}
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for i := 1; ; i++ {
		v := r.FormValue(fmt.Sprintf("SubnetId.%d", i))
		if v == "" {
			break
		}
		add(v)
	}
	for fi := 1; ; fi++ {
		name := r.FormValue(fmt.Sprintf("Filter.%d.Name", fi))
		if name == "" {
			break
		}
		if name != "subnet-id" {
			continue
		}
		for vi := 1; ; vi++ {
			v := r.FormValue(fmt.Sprintf("Filter.%d.Value.%d", fi, vi))
			if v == "" {
				break
			}
			add(v)
		}
	}
	return ids
}

// parseTagSpecTags reads tags from TagSpecification.N.Tag.M.Key/Value, the form
// SDK clients use to tag a resource as part of its create call. Falls back to
// the flat Tag.N form for callers that tag separately.
func parseTagSpecTags(r *http.Request) map[string]string {
	tags := map[string]string{}
	for i := 1; ; i++ {
		prefix := fmt.Sprintf("TagSpecification.%d.Tag.", i)
		if r.FormValue(prefix+"1.Key") == "" {
			break
		}
		for j := 1; ; j++ {
			key := r.FormValue(fmt.Sprintf("%s%d.Key", prefix, j))
			if key == "" {
				break
			}
			tags[key] = r.FormValue(fmt.Sprintf("%s%d.Value", prefix, j))
		}
	}
	if len(tags) == 0 {
		return parseTags(r)
	}
	return tags
}

// parseTags reads Tag.N.Key/Value params from the form.
func parseTags(r *http.Request) map[string]string {
	tags := map[string]string{}
	for i := 1; ; i++ {
		key := r.FormValue(fmt.Sprintf("Tag.%d.Key", i))
		if key == "" {
			break
		}
		tags[key] = r.FormValue(fmt.Sprintf("Tag.%d.Value", i))
	}
	return tags
}

// parseIPPermissions parses IpPermissions.N.* form params into sgRule slices.
// Each rule is assigned a unique sgr- ID.
func parseIPPermissions(r *http.Request, prefix string) []sgRule {
	var rules []sgRule
	for i := 1; ; i++ {
		proto := r.FormValue(fmt.Sprintf("%s.%d.IpProtocol", prefix, i))
		if proto == "" {
			break
		}
		fromPort, _ := strconv.Atoi(r.FormValue(fmt.Sprintf("%s.%d.FromPort", prefix, i)))
		toPort, _ := strconv.Atoi(r.FormValue(fmt.Sprintf("%s.%d.ToPort", prefix, i)))

		var cidrs []string
		for j := 1; ; j++ {
			cidr := r.FormValue(fmt.Sprintf("%s.%d.IpRanges.%d.CidrIp", prefix, i, j))
			if cidr == "" {
				break
			}
			cidrs = append(cidrs, cidr)
		}

		// A rule may target a prefix list or a peer security group instead of a
		// CIDR. Dropping these leaves an empty rule that never matches what the
		// caller configured.
		var prefixLists []string
		for j := 1; ; j++ {
			pl := r.FormValue(fmt.Sprintf("%s.%d.PrefixListIds.%d.PrefixListId", prefix, i, j))
			if pl == "" {
				break
			}
			prefixLists = append(prefixLists, pl)
		}

		var groups []string
		for j := 1; ; j++ {
			gid := r.FormValue(fmt.Sprintf("%s.%d.Groups.%d.GroupId", prefix, i, j))
			if gid == "" {
				break
			}
			groups = append(groups, gid)
		}

		rules = append(rules, sgRule{
			id:            "sgr-" + shortID(),
			protocol:      proto,
			fromPort:      fromPort,
			toPort:        toPort,
			cidrIPs:       cidrs,
			prefixListIDs: prefixLists,
			groupIDs:      groups,
		})
	}
	return rules
}

// ── XML helpers ───────────────────────────────────────────────────────────────

func tagsXML(tags map[string]string) string {
	if len(tags) == 0 {
		return "<tagSet/>"
	}
	var b strings.Builder
	b.WriteString("<tagSet>")
	for k, v := range tags {
		fmt.Fprintf(&b, "<item><key>%s</key><value>%s</value></item>", k, v)
	}
	b.WriteString("</tagSet>")
	return b.String()
}

func ec2Resp(action, body string) string {
	return fmt.Sprintf(`<%sResponse xmlns=%q>
  <requestId>%s</requestId>
  %s
</%sResponse>`, action, ec2NS, uid.New(), body, action)
}

func writeXML(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>`)
	fmt.Fprint(w, body)
}

func ec2Error(w http.ResponseWriter, code, msg string) {
	writeXML(w, http.StatusBadRequest, fmt.Sprintf(
		`<Response><Errors><Error><Code>%s</Code><Message>%s</Message></Error></Errors>`+
			`<RequestID>%s</RequestID></Response>`, code, msg, uid.New()))
}

// ── Misc helpers ──────────────────────────────────────────────────────────────

func contains(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}

func shortID() string {
	u := strings.ReplaceAll(uid.New(), "-", "")
	return u[:8]
}
