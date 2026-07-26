package ec2

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ── Data types ────────────────────────────────────────────────────────────────

// managedPrefixList is an AWS-managed prefix list. Only the service lists that
// back gateway endpoints are modelled; customer-managed lists are not created.
type managedPrefixList struct {
	id      string
	name    string // e.g. "com.amazonaws.us-east-1.s3"
	cidrs   []string
	ownerID string
}

type vpcEndpoint struct {
	id            string
	vpcID         string
	serviceName   string
	endpointType  string // "Gateway" | "Interface" | "GatewayLoadBalancer"
	state         string
	policy        string
	routeTableIDs []string
	subnetIDs     []string
	sgIDs         []string
	privateDNS    bool
	createdAt     time.Time
	tags          map[string]string
}

// gatewayServices are the services AWS offers as gateway endpoints, and so the
// only ones with a service prefix list.
var gatewayServices = []string{"s3", "dynamodb"}

// seedPrefixLists builds the AWS-managed service prefix lists for a region.
// IDs are derived from the list name so they survive a Nimbus restart — a
// changing ID would look like a replaced prefix list to a state file.
func seedPrefixLists(region string) map[string]*managedPrefixList {
	lists := map[string]*managedPrefixList{}
	for _, svc := range gatewayServices {
		name := fmt.Sprintf("com.amazonaws.%s.%s", region, svc)
		id := prefixListID(name)
		lists[id] = &managedPrefixList{
			id:   id,
			name: name,
			// Synthetic ranges: nothing routes through Nimbus, and reserved
			// documentation CIDRs make that obvious in `describe` output.
			cidrs:   []string{"198.51.100.0/24"},
			ownerID: "AWS",
		}
	}
	return lists
}

func prefixListID(name string) string {
	sum := sha256.Sum256([]byte(name))
	return "pl-" + hex.EncodeToString(sum[:])[:8]
}

// prefixListByName resolves a service prefix list by its name. Callers must
// hold at least a read lock.
func (s *Service) prefixListByName(name string) *managedPrefixList {
	for _, pl := range s.prefixLists {
		if pl.name == name {
			return pl
		}
	}
	return nil
}

// ── VPC endpoints ─────────────────────────────────────────────────────────────

func (s *Service) createVpcEndpoint(w http.ResponseWriter, r *http.Request) {
	vpcID := r.FormValue("VpcId")
	serviceName := r.FormValue("ServiceName")
	if vpcID == "" || serviceName == "" {
		ec2Error(w, "MissingParameter", "VpcId and ServiceName are required")
		return
	}

	epType := r.FormValue("VpcEndpointType")
	if epType == "" {
		epType = "Gateway"
	}

	ep := &vpcEndpoint{
		id:            "vpce-" + shortID(),
		vpcID:         vpcID,
		serviceName:   serviceName,
		endpointType:  epType,
		state:         "available",
		policy:        r.FormValue("PolicyDocument"),
		routeTableIDs: collectValues(r, "RouteTableId"),
		subnetIDs:     collectValues(r, "SubnetId"),
		sgIDs:         collectValues(r, "SecurityGroupId"),
		privateDNS:    r.FormValue("PrivateDnsEnabled") == "true",
		createdAt:     time.Now().UTC(),
		tags:          parseTagSpecTags(r),
	}

	s.mu.Lock()
	s.vpcEndpoints[ep.id] = ep
	s.mu.Unlock()

	writeXML(w, http.StatusOK, ec2Resp("CreateVpcEndpoint",
		fmt.Sprintf("<vpcEndpoint>%s</vpcEndpoint>", s.vpcEndpointXML(ep))))
}

func (s *Service) describeVpcEndpoints(w http.ResponseWriter, r *http.Request) {
	ids := collectValues(r, "VpcEndpointId")
	filters := parseFilters(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	var items strings.Builder
	for _, ep := range s.vpcEndpoints {
		if len(ids) > 0 && !contains(ids, ep.id) {
			continue
		}
		if fv, ok := filters["vpc-id"]; ok && !contains(fv, ep.vpcID) {
			continue
		}
		if fv, ok := filters["service-name"]; ok && !contains(fv, ep.serviceName) {
			continue
		}
		if fv, ok := filters["vpc-endpoint-id"]; ok && !contains(fv, ep.id) {
			continue
		}
		if fv, ok := filters["vpc-endpoint-type"]; ok && !contains(fv, ep.endpointType) {
			continue
		}
		if fv, ok := filters["vpc-endpoint-state"]; ok && !contains(fv, ep.state) {
			continue
		}
		if !tagFiltersMatch(filters, ep.tags) {
			continue
		}
		fmt.Fprintf(&items, "\n    <item>%s</item>", s.vpcEndpointXML(ep))
	}

	writeXML(w, http.StatusOK, ec2Resp("DescribeVpcEndpoints",
		"<vpcEndpointSet>"+items.String()+"\n  </vpcEndpointSet>"))
}

func (s *Service) modifyVpcEndpoint(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("VpcEndpointId")

	s.mu.Lock()
	if ep := s.vpcEndpoints[id]; ep != nil {
		if pd := r.FormValue("PolicyDocument"); pd != "" {
			ep.policy = pd
		}
		if add := collectValues(r, "AddRouteTableId"); len(add) > 0 {
			ep.routeTableIDs = append(ep.routeTableIDs, add...)
		}
		if rm := collectValues(r, "RemoveRouteTableId"); len(rm) > 0 {
			ep.routeTableIDs = removeAll(ep.routeTableIDs, rm)
		}
		if add := collectValues(r, "AddSubnetId"); len(add) > 0 {
			ep.subnetIDs = append(ep.subnetIDs, add...)
		}
		if rm := collectValues(r, "RemoveSubnetId"); len(rm) > 0 {
			ep.subnetIDs = removeAll(ep.subnetIDs, rm)
		}
	}
	s.mu.Unlock()

	writeXML(w, http.StatusOK, ec2Resp("ModifyVpcEndpoint", "<return>true</return>"))
}

// DeleteVpcEndpoints takes a list; unwind entries report per-endpoint failures,
// which Nimbus never produces since a missing endpoint is already the goal state.
func (s *Service) deleteVpcEndpoints(w http.ResponseWriter, r *http.Request) {
	ids := collectValues(r, "VpcEndpointId")

	s.mu.Lock()
	for _, id := range ids {
		delete(s.vpcEndpoints, id)
	}
	s.mu.Unlock()

	writeXML(w, http.StatusOK, ec2Resp("DeleteVpcEndpoints", "<unsuccessful/>"))
}

func (s *Service) vpcEndpointXML(ep *vpcEndpoint) string {
	var b strings.Builder
	fmt.Fprintf(&b, `
      <vpcEndpointId>%s</vpcEndpointId>
      <vpcEndpointType>%s</vpcEndpointType>
      <vpcId>%s</vpcId>
      <serviceName>%s</serviceName>
      <state>%s</state>
      <policyDocument>%s</policyDocument>
      <privateDnsEnabled>%v</privateDnsEnabled>
      <requesterManaged>false</requesterManaged>
      <ownerId>%s</ownerId>
      <creationTimestamp>%s</creationTimestamp>`,
		ep.id, ep.endpointType, ep.vpcID, ep.serviceName, ep.state,
		xmlEscape(ep.policy), ep.privateDNS, accountID,
		ep.createdAt.Format(time.RFC3339))

	b.WriteString(idSetXML("routeTableIdSet", ep.routeTableIDs))
	b.WriteString(idSetXML("subnetIdSet", ep.subnetIDs))
	b.WriteString(groupSetXML(ep.sgIDs))
	b.WriteString("<networkInterfaceIdSet/><dnsEntrySet/>")
	b.WriteString(tagsXML(ep.tags))
	return b.String()
}

func idSetXML(tag string, ids []string) string {
	if len(ids) == 0 {
		return "<" + tag + "/>"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<%s>", tag)
	for _, id := range ids {
		fmt.Fprintf(&b, "<item>%s</item>", id)
	}
	fmt.Fprintf(&b, "</%s>", tag)
	return b.String()
}

// groupSetXML renders the security groups attached to an interface endpoint.
// Unlike a plain ID set each entry carries the group name alongside the ID.
func groupSetXML(ids []string) string {
	if len(ids) == 0 {
		return "<groupSet/>"
	}
	var b strings.Builder
	b.WriteString("<groupSet>")
	for _, id := range ids {
		fmt.Fprintf(&b, "<item><groupId>%s</groupId></item>", id)
	}
	b.WriteString("</groupSet>")
	return b.String()
}

// ── Prefix lists ──────────────────────────────────────────────────────────────

// DescribePrefixLists is the legacy call that resolves a gateway endpoint's
// service name to the prefix list ID used in security group rules.
func (s *Service) describePrefixLists(w http.ResponseWriter, r *http.Request) {
	ids := collectValues(r, "PrefixListId")
	filters := parseFilters(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	var items strings.Builder
	for _, pl := range s.prefixLists {
		if len(ids) > 0 && !contains(ids, pl.id) {
			continue
		}
		if fv, ok := filters["prefix-list-id"]; ok && !contains(fv, pl.id) {
			continue
		}
		if fv, ok := filters["prefix-list-name"]; ok && !contains(fv, pl.name) {
			continue
		}
		fmt.Fprintf(&items, `
    <item>
      <prefixListId>%s</prefixListId>
      <prefixListName>%s</prefixListName>
      %s
    </item>`, pl.id, pl.name, cidrSetXML(pl.cidrs))
	}

	writeXML(w, http.StatusOK, ec2Resp("DescribePrefixLists",
		"<prefixListSet>"+items.String()+"\n  </prefixListSet>"))
}

func (s *Service) describeManagedPrefixLists(w http.ResponseWriter, r *http.Request) {
	ids := collectValues(r, "PrefixListId")
	filters := parseFilters(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	var items strings.Builder
	for _, pl := range s.prefixLists {
		if len(ids) > 0 && !contains(ids, pl.id) {
			continue
		}
		if fv, ok := filters["prefix-list-id"]; ok && !contains(fv, pl.id) {
			continue
		}
		if fv, ok := filters["prefix-list-name"]; ok && !contains(fv, pl.name) {
			continue
		}
		if fv, ok := filters["owner-id"]; ok && !contains(fv, pl.ownerID) {
			continue
		}
		fmt.Fprintf(&items, `
    <item>
      <prefixListId>%s</prefixListId>
      <prefixListName>%s</prefixListName>
      <prefixListArn>arn:aws:ec2:%s:aws:prefix-list/%s</prefixListArn>
      <addressFamily>IPv4</addressFamily>
      <state>create-complete</state>
      <maxEntries>%d</maxEntries>
      <version>1</version>
      <ownerId>%s</ownerId>
      <tagSet/>
    </item>`, pl.id, pl.name, s.region, pl.id, len(pl.cidrs), pl.ownerID)
	}

	writeXML(w, http.StatusOK, ec2Resp("DescribeManagedPrefixLists",
		"<prefixListSet>"+items.String()+"\n  </prefixListSet>"))
}

// GetManagedPrefixListEntries returns the CIDRs behind a managed prefix list.
func (s *Service) getManagedPrefixListEntries(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("PrefixListId")

	s.mu.RLock()
	pl := s.prefixLists[id]
	s.mu.RUnlock()

	if pl == nil {
		ec2Error(w, "InvalidPrefixListID.NotFound",
			fmt.Sprintf("The prefix list ID '%s' does not exist", id))
		return
	}

	var items strings.Builder
	for _, c := range pl.cidrs {
		fmt.Fprintf(&items, "<item><cidr>%s</cidr></item>", c)
	}
	writeXML(w, http.StatusOK, ec2Resp("GetManagedPrefixListEntries",
		"<entrySet>"+items.String()+"</entrySet>"))
}

func cidrSetXML(cidrs []string) string {
	if len(cidrs) == 0 {
		return "<cidrSet/>"
	}
	var b strings.Builder
	b.WriteString("<cidrSet>")
	for _, c := range cidrs {
		fmt.Fprintf(&b, "<item>%s</item>", c)
	}
	b.WriteString("</cidrSet>")
	return b.String()
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// tagFiltersMatch applies `tag:<key>` filters against a resource's tags.
func tagFiltersMatch(filters map[string][]string, tags map[string]string) bool {
	for name, want := range filters {
		key, ok := strings.CutPrefix(name, "tag:")
		if !ok {
			continue
		}
		if !contains(want, tags[key]) {
			return false
		}
	}
	return true
}

func removeAll(list, remove []string) []string {
	var out []string
	for _, v := range list {
		if !contains(remove, v) {
			out = append(out, v)
		}
	}
	return out
}

// xmlEscape escapes the characters that would break out of an element body.
// Endpoint policies are caller-supplied JSON and routinely contain quotes.
func xmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return r.Replace(s)
}
