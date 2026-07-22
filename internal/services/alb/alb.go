// Package alb emulates the AWS ELBv2 (ALB) control plane.
// All state is in-memory. Load balancers are never actually provisioned —
// DNSName is localhost-based and State is always active. Listeners start a
// real HTTP reverse proxy on the configured port that routes to registered targets.
package alb

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nimbus-local/nimbus/internal/uid"
)

const (
	accountID = "000000000000"
	elbNS     = "http://elasticloadbalancing.amazonaws.com/doc/2015-12-01/"
)

// Service implements the ELBv2 ALB control plane.
type Service struct {
	mu           sync.RWMutex
	lbs          map[string]*lb
	listeners    map[string]*listener
	rules        map[string]*rule        // arn -> rule
	targetGroups map[string]*targetGroup // arn -> tg
	proxies      map[string]*activeProxy // port -> proxy
	region       string
	// subnetAZ resolves a subnet ID to its Availability Zone (typically
	// ec2.Service.SubnetAZ). Nil in tests; unknown subnets fall back to
	// treating the subnet ID itself as a distinct zone.
	subnetAZ func(string) (string, bool)
}

type lb struct {
	arn       string
	name      string
	dnsName   string
	scheme    string
	lbType    string
	createdAt time.Time
	subnets   []lbSubnet
}

// lbSubnet is a subnet attached to a load balancer, with its resolved AZ.
type lbSubnet struct {
	subnetID string
	zoneName string
}

type listener struct {
	arn           string
	lbARN         string
	protocol      string
	port          string
	defaultAction defaultAction
	createdAt     time.Time
}

type rule struct {
	arn         string
	listenerARN string
	priority    string // "1"–"50000" or "default"
	isDefault   bool
	conditions  []condition
	action      defaultAction
}

type condition struct {
	field  string
	values []string
}

type defaultAction struct {
	actionType     string
	targetGroupARN string
}

type targetGroup struct {
	arn        string
	name       string
	protocol   string
	port       string
	vpcID      string
	targetType string
	createdAt  time.Time
	targets    map[string]*registeredTarget // "id:port" -> target
}

type registeredTarget struct {
	id   string
	port string // empty means use TG port
}

type activeProxy struct {
	srv *http.Server
}

// New creates an ALB service. subnetAZ resolves a subnet ID to its
// Availability Zone (pass ec2.Service.SubnetAZ); it may be nil.
func New(region string, subnetAZ func(string) (string, bool)) *Service {
	if region == "" {
		region = "us-east-1"
	}
	return &Service{
		region:       region,
		subnetAZ:     subnetAZ,
		lbs:          map[string]*lb{},
		listeners:    map[string]*listener{},
		rules:        map[string]*rule{},
		targetGroups: map[string]*targetGroup{},
		proxies:      map[string]*activeProxy{},
	}
}

func (s *Service) Name() string { return "alb" }

// Reset clears all in-memory state and shuts down active proxies.
func (s *Service) Reset() {
	s.mu.Lock()
	proxiesToStop := make(map[string]*activeProxy, len(s.proxies))
	for port, p := range s.proxies {
		proxiesToStop[port] = p
	}
	s.lbs = map[string]*lb{}
	s.listeners = map[string]*listener{}
	s.rules = map[string]*rule{}
	s.targetGroups = map[string]*targetGroup{}
	s.proxies = map[string]*activeProxy{}
	s.mu.Unlock()
	for port, p := range proxiesToStop {
		go func(port string, p *activeProxy) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = p.srv.Shutdown(ctx)
		}(port, p)
	}
}

// Detect claims ELBv2 requests — form-encoded body with Version=2015-12-01.
// Uses ParseForm (idempotent) so body is not double-consumed if a prior service
// already called ParseForm (e.g. SQS).
func (s *Service) Detect(r *http.Request) bool {
	if !strings.Contains(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		return false
	}
	_ = r.ParseForm()
	return r.FormValue("Version") == "2015-12-01"
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		elbError(w, http.StatusBadRequest, "ValidationError", "cannot parse request body")
		return
	}
	switch r.FormValue("Action") {
	// Load balancer
	case "CreateLoadBalancer":
		s.createLoadBalancer(w, r)
	case "DescribeLoadBalancers":
		s.describeLoadBalancers(w, r)
	case "DeleteLoadBalancer":
		s.deleteLoadBalancer(w, r)
	case "SetSubnets":
		s.setSubnets(w, r)
	case "DescribeLoadBalancerAttributes":
		s.describeLoadBalancerAttributes(w, r)
	case "ModifyLoadBalancerAttributes":
		s.modifyLoadBalancerAttributes(w, r)
	case "DescribeCapacityReservation":
		s.describeCapacityReservation(w, r)
	// Listener
	case "CreateListener":
		s.createListener(w, r)
	case "DescribeListeners":
		s.describeListeners(w, r)
	case "DeleteListener":
		s.deleteListener(w, r)
	case "ModifyListener":
		s.modifyListener(w, r)
	case "DescribeListenerAttributes":
		s.describeListenerAttributes(w, r)
	case "ModifyListenerAttributes":
		s.modifyListenerAttributes(w, r)
	// Rules
	case "CreateRule":
		s.createRule(w, r)
	case "DescribeRules":
		s.describeRules(w, r)
	case "DeleteRule":
		s.deleteRule(w, r)
	case "ModifyRule":
		s.modifyRule(w, r)
	case "SetRulePriorities":
		s.setRulePriorities(w, r)
	// Target group
	case "CreateTargetGroup":
		s.createTargetGroup(w, r)
	case "DescribeTargetGroups":
		s.describeTargetGroups(w, r)
	case "DeleteTargetGroup":
		s.deleteTargetGroup(w, r)
	case "ModifyTargetGroup":
		s.modifyTargetGroup(w, r)
	case "DescribeTargetGroupAttributes":
		s.describeTargetGroupAttributes(w, r)
	case "ModifyTargetGroupAttributes":
		s.modifyTargetGroupAttributes(w, r)
	// Target registration
	case "RegisterTargets":
		s.registerTargets(w, r)
	case "DeregisterTargets":
		s.deregisterTargets(w, r)
	case "DescribeTargetHealth":
		s.describeTargetHealth(w, r)
	// Tags
	case "AddTags":
		elbOK(w, "AddTagsResponse", "AddTagsResult", "")
	case "RemoveTags":
		elbOK(w, "RemoveTagsResponse", "RemoveTagsResult", "")
	case "DescribeTags":
		s.describeTags(w, r)
	default:
		elbError(w, http.StatusNotImplemented, "NotImplemented", r.FormValue("Action"))
	}
}

// --- Inspection handlers ---

func (s *Service) LoadBalancersHandler(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	type row struct {
		ARN       string    `json:"arn"`
		Name      string    `json:"name"`
		DNSName   string    `json:"dnsName"`
		Scheme    string    `json:"scheme"`
		Type      string    `json:"type"`
		CreatedAt time.Time `json:"createdAt"`
	}
	rows := make([]row, 0, len(s.lbs))
	for _, l := range s.lbs {
		rows = append(rows, row{ARN: l.arn, Name: l.name, DNSName: l.dnsName,
			Scheme: l.scheme, Type: l.lbType, CreatedAt: l.createdAt})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rows)
}

func (s *Service) TargetGroupsHandler(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	type targetRow struct {
		ID   string `json:"id"`
		Port string `json:"port,omitempty"`
	}
	type row struct {
		ARN        string      `json:"arn"`
		Name       string      `json:"name"`
		Protocol   string      `json:"protocol"`
		Port       string      `json:"port"`
		TargetType string      `json:"targetType"`
		Targets    []targetRow `json:"targets"`
		CreatedAt  time.Time   `json:"createdAt"`
	}
	rows := make([]row, 0, len(s.targetGroups))
	for _, tg := range s.targetGroups {
		var targets []targetRow
		for _, t := range tg.targets {
			targets = append(targets, targetRow{ID: t.id, Port: t.port})
		}
		rows = append(rows, row{ARN: tg.arn, Name: tg.name, Protocol: tg.protocol,
			Port: tg.port, TargetType: tg.targetType, Targets: targets, CreatedAt: tg.createdAt})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rows)
}

func (s *Service) ListenersHandler(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	type row struct {
		ARN           string    `json:"arn"`
		LBARN         string    `json:"lbArn"`
		Protocol      string    `json:"protocol"`
		Port          string    `json:"port"`
		DefaultAction string    `json:"defaultAction"`
		CreatedAt     time.Time `json:"createdAt"`
	}
	rows := make([]row, 0, len(s.listeners))
	for _, l := range s.listeners {
		action := l.defaultAction.actionType
		if l.defaultAction.targetGroupARN != "" {
			action += " -> " + l.defaultAction.targetGroupARN
		}
		rows = append(rows, row{ARN: l.arn, LBARN: l.lbARN, Protocol: l.protocol,
			Port: l.port, DefaultAction: action, CreatedAt: l.createdAt})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rows)
}

// --- Load Balancer operations ---

func (s *Service) createLoadBalancer(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("Name")
	if name == "" {
		elbError(w, http.StatusBadRequest, "ValidationError", "Name is required")
		return
	}
	lbType := r.FormValue("Type")
	if lbType == "" {
		lbType = "application"
	}
	scheme := r.FormValue("Scheme")
	if scheme == "" {
		scheme = "internet-facing"
	}

	subnets := s.resolveSubnets(r)

	// Application load balancers must span at least two subnets in two
	// different Availability Zones — real AWS rejects otherwise.
	if lbType == "application" {
		azSet := map[string]bool{}
		for _, sn := range subnets {
			azSet[sn.zoneName] = true
		}
		if len(azSet) < 2 {
			elbError(w, http.StatusBadRequest, "ValidationError",
				"At least two subnets in two different Availability Zones must be specified")
			return
		}
	}

	id := shortID()
	arn := fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:loadbalancer/app/%s/%s",
		s.region, accountID, name, id)
	dnsName := fmt.Sprintf("%s-%s.%s.elb.localhost", name, id, s.region)

	l := &lb{arn: arn, name: name, dnsName: dnsName, scheme: scheme,
		lbType: lbType, createdAt: time.Now().UTC(), subnets: subnets}

	s.mu.Lock()
	s.lbs[arn] = l
	s.mu.Unlock()

	elbOK(w, "CreateLoadBalancerResponse", "CreateLoadBalancerResult",
		"<LoadBalancers>"+lbMember(l)+"</LoadBalancers>")
}

// resolveSubnets collects the subnet IDs from the request (both Subnets.member.N
// and SubnetMappings.member.N.SubnetId) and resolves each to its Availability
// Zone. Subnets unknown to EC2 fall back to using the subnet ID as the zone, so
// distinct IDs still count as distinct AZs.
func (s *Service) resolveSubnets(r *http.Request) []lbSubnet {
	ids := formList(r, "Subnets.member.")
	for i := 1; ; i++ {
		id := r.FormValue(fmt.Sprintf("SubnetMappings.member.%d.SubnetId", i))
		if id == "" {
			break
		}
		ids = append(ids, id)
	}
	out := make([]lbSubnet, 0, len(ids))
	for _, id := range ids {
		out = append(out, lbSubnet{subnetID: id, zoneName: s.lookupAZ(id)})
	}
	return out
}

// lookupAZ resolves a subnet ID to its AZ, falling back to the subnet ID itself
// when the subnet is unknown (no EC2 store or synthetic ID).
func (s *Service) lookupAZ(subnetID string) string {
	if s.subnetAZ != nil {
		if az, ok := s.subnetAZ(subnetID); ok {
			return az
		}
	}
	return subnetID
}

func (s *Service) describeLoadBalancers(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	filterARNs := formList(r, "LoadBalancerArns.member.")
	filterNames := formList(r, "Names.member.")

	var members []string
	for _, l := range s.lbs {
		if len(filterARNs) > 0 && !contains(filterARNs, l.arn) {
			continue
		}
		if len(filterNames) > 0 && !contains(filterNames, l.name) {
			continue
		}
		members = append(members, lbMember(l))
	}

	if len(filterARNs) > 0 && len(members) == 0 {
		elbError(w, http.StatusBadRequest, "LoadBalancerNotFound",
			"One or more of the specified load balancers do not exist.")
		return
	}

	elbOK(w, "DescribeLoadBalancersResponse", "DescribeLoadBalancersResult",
		"<LoadBalancers>"+strings.Join(members, "")+"</LoadBalancers><NextMarker/>")
}

func (s *Service) deleteLoadBalancer(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("LoadBalancerArn")
	s.mu.Lock()
	delete(s.lbs, arn)
	// Cascade: delete listeners and their rules, stop proxies
	for lArn, l := range s.listeners {
		if l.lbARN == arn {
			s.removeListenerRules(lArn)
			port := l.port
			delete(s.listeners, lArn)
			if !s.portInUse(port) {
				s.stopProxyLocked(port)
			}
		}
	}
	s.mu.Unlock()
	elbOK(w, "DeleteLoadBalancerResponse", "DeleteLoadBalancerResult", "")
}

func (s *Service) setSubnets(w http.ResponseWriter, r *http.Request) {
	lbARN := r.FormValue("LoadBalancerArn")
	s.mu.RLock()
	l := s.lbs[lbARN]
	s.mu.RUnlock()
	if l == nil {
		elbError(w, http.StatusBadRequest, "LoadBalancerNotFound", "The specified load balancer does not exist.")
		return
	}
	elbOK(w, "SetSubnetsResponse", "SetSubnetsResult",
		`<AvailabilityZones><member><ZoneName>us-east-1a</ZoneName><SubnetId>subnet-00000000000000001</SubnetId><LoadBalancerAddresses/></member></AvailabilityZones>`)
}

func (s *Service) describeLoadBalancerAttributes(w http.ResponseWriter, _ *http.Request) {
	attrs := `<member><Key>deletion_protection.enabled</Key><Value>false</Value></member>` +
		`<member><Key>idle_timeout.timeout_seconds</Key><Value>60</Value></member>` +
		`<member><Key>access_logs.s3.enabled</Key><Value>false</Value></member>` +
		`<member><Key>access_logs.s3.bucket</Key><Value></Value></member>` +
		`<member><Key>access_logs.s3.prefix</Key><Value></Value></member>` +
		`<member><Key>enable_http2</Key><Value>true</Value></member>` +
		`<member><Key>routing.http.desync_mitigation_mode</Key><Value>defensive</Value></member>` +
		`<member><Key>routing.http.drop_invalid_header_fields.enabled</Key><Value>false</Value></member>` +
		`<member><Key>routing.http.preserve_host_header.enabled</Key><Value>false</Value></member>` +
		`<member><Key>routing.http.x_amzn_tls_version_and_cipher_suite.enabled</Key><Value>false</Value></member>` +
		`<member><Key>routing.http.xff_client_port.enabled</Key><Value>false</Value></member>` +
		`<member><Key>routing.http.xff_header_processing.mode</Key><Value>append</Value></member>` +
		`<member><Key>waf.fail_open.enabled</Key><Value>false</Value></member>`
	elbOK(w, "DescribeLoadBalancerAttributesResponse", "DescribeLoadBalancerAttributesResult",
		"<Attributes>"+attrs+"</Attributes>")
}

func (s *Service) modifyLoadBalancerAttributes(w http.ResponseWriter, _ *http.Request) {
	elbOK(w, "ModifyLoadBalancerAttributesResponse", "ModifyLoadBalancerAttributesResult",
		"<Attributes/>")
}

func (s *Service) describeCapacityReservation(w http.ResponseWriter, _ *http.Request) {
	body := `<DecreaseRequestsRemaining>10</DecreaseRequestsRemaining>` +
		`<LastModifiedTime>` + time.Now().UTC().Format(time.RFC3339) + `</LastModifiedTime>` +
		`<CapacityReservationState/>`
	elbOK(w, "DescribeCapacityReservationResponse", "DescribeCapacityReservationResult", body)
}

// --- Listener operations ---

func (s *Service) createListener(w http.ResponseWriter, r *http.Request) {
	lbARN := r.FormValue("LoadBalancerArn")
	if lbARN == "" {
		elbError(w, http.StatusBadRequest, "ValidationError", "LoadBalancerArn is required")
		return
	}
	protocol := r.FormValue("Protocol")
	if protocol == "" {
		protocol = "HTTP"
	}
	port := r.FormValue("Port")
	if port == "" {
		port = "80"
	}
	da := defaultAction{
		actionType:     r.FormValue("DefaultActions.member.1.Type"),
		targetGroupARN: r.FormValue("DefaultActions.member.1.TargetGroupArn"),
	}
	if da.actionType == "" {
		da.actionType = "forward"
	}

	lbSuffix := lbARNSuffix(lbARN)
	listenerID := shortID()
	lArn := fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:listener/%s/%s",
		s.region, accountID, lbSuffix, listenerID)

	l := &listener{arn: lArn, lbARN: lbARN, protocol: protocol,
		port: port, defaultAction: da, createdAt: time.Now().UTC()}

	// Create the implicit default rule for this listener.
	defaultRuleARN := lArn + "/default"
	defaultRule := &rule{
		arn:         defaultRuleARN,
		listenerARN: lArn,
		priority:    "default",
		isDefault:   true,
		action:      da,
	}

	s.mu.Lock()
	s.listeners[lArn] = l
	s.rules[defaultRuleARN] = defaultRule
	alreadyListening := s.portInUse(port)
	s.mu.Unlock()

	if !alreadyListening {
		s.startProxy(port, lArn)
	}

	elbOK(w, "CreateListenerResponse", "CreateListenerResult",
		"<Listeners>"+listenerMember(l)+"</Listeners>")
}

func (s *Service) describeListeners(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	filterARNs := formList(r, "ListenerArns.member.")
	filterLBARN := r.FormValue("LoadBalancerArn")

	var members []string
	for _, l := range s.listeners {
		if len(filterARNs) > 0 && !contains(filterARNs, l.arn) {
			continue
		}
		if filterLBARN != "" && l.lbARN != filterLBARN {
			continue
		}
		members = append(members, listenerMember(l))
	}

	if len(filterARNs) > 0 && len(members) == 0 {
		elbError(w, http.StatusBadRequest, "ListenerNotFound",
			"One or more of the specified listeners do not exist.")
		return
	}

	elbOK(w, "DescribeListenersResponse", "DescribeListenersResult",
		"<Listeners>"+strings.Join(members, "")+"</Listeners><NextMarker/>")
}

func (s *Service) deleteListener(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("ListenerArn")
	s.mu.Lock()
	l, ok := s.listeners[arn]
	if ok {
		s.removeListenerRules(arn)
		port := l.port
		delete(s.listeners, arn)
		if !s.portInUse(port) {
			s.stopProxyLocked(port)
		}
	}
	s.mu.Unlock()
	elbOK(w, "DeleteListenerResponse", "DeleteListenerResult", "")
}

func (s *Service) modifyListener(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("ListenerArn")
	s.mu.Lock()
	l, ok := s.listeners[arn]
	if ok {
		if v := r.FormValue("Protocol"); v != "" {
			l.protocol = v
		}
		if v := r.FormValue("Port"); v != "" {
			l.port = v
		}
		if v := r.FormValue("DefaultActions.member.1.Type"); v != "" {
			l.defaultAction.actionType = v
		}
		if v := r.FormValue("DefaultActions.member.1.TargetGroupArn"); v != "" {
			l.defaultAction.targetGroupARN = v
		}
		// Keep default rule in sync
		if dr, ok2 := s.rules[arn+"/default"]; ok2 {
			dr.action = l.defaultAction
		}
	}
	s.mu.Unlock()
	if !ok {
		elbError(w, http.StatusBadRequest, "ListenerNotFound", "The specified listener does not exist.")
		return
	}
	s.mu.RLock()
	mem := listenerMember(l)
	s.mu.RUnlock()
	elbOK(w, "ModifyListenerResponse", "ModifyListenerResult",
		"<Listeners>"+mem+"</Listeners>")
}

func (s *Service) describeListenerAttributes(w http.ResponseWriter, _ *http.Request) {
	attrs := `<member><Key>routing.http.request.x_forwarded_for.enabled</Key><Value>true</Value></member>` +
		`<member><Key>routing.http.response.server.enabled</Key><Value>true</Value></member>`
	elbOK(w, "DescribeListenerAttributesResponse", "DescribeListenerAttributesResult",
		"<Attributes>"+attrs+"</Attributes>")
}

func (s *Service) modifyListenerAttributes(w http.ResponseWriter, _ *http.Request) {
	elbOK(w, "ModifyListenerAttributesResponse", "ModifyListenerAttributesResult",
		"<Attributes/>")
}

// --- Rule operations ---

func (s *Service) createRule(w http.ResponseWriter, r *http.Request) {
	listenerARN := r.FormValue("ListenerArn")
	if listenerARN == "" {
		elbError(w, http.StatusBadRequest, "ValidationError", "ListenerArn is required")
		return
	}
	priority := r.FormValue("Priority")
	if priority == "" {
		priority = "100"
	}

	ruleID := shortID()
	rArn := ruleARN(listenerARN, ruleID)

	ru := &rule{
		arn:         rArn,
		listenerARN: listenerARN,
		priority:    priority,
		isDefault:   false,
		conditions:  parseConditions(r, "Conditions.member."),
		action: defaultAction{
			actionType:     r.FormValue("Actions.member.1.Type"),
			targetGroupARN: r.FormValue("Actions.member.1.TargetGroupArn"),
		},
	}
	if ru.action.actionType == "" {
		ru.action.actionType = "forward"
	}

	s.mu.Lock()
	s.rules[rArn] = ru
	s.mu.Unlock()

	elbOK(w, "CreateRuleResponse", "CreateRuleResult",
		"<Rules>"+ruleMember(ru)+"</Rules>")
}

func (s *Service) describeRules(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	listenerARN := r.FormValue("ListenerArn")
	filterARNs := formList(r, "RuleArns.member.")

	var matched []*rule
	for _, ru := range s.rules {
		if listenerARN != "" && ru.listenerARN != listenerARN {
			continue
		}
		if len(filterARNs) > 0 && !contains(filterARNs, ru.arn) {
			continue
		}
		matched = append(matched, ru)
	}

	// Sort: numeric priorities ascending, default last
	sort.Slice(matched, func(i, j int) bool {
		return rulePriorityCmp(matched[i], matched[j])
	})

	var members []string
	for _, ru := range matched {
		members = append(members, ruleMember(ru))
	}

	elbOK(w, "DescribeRulesResponse", "DescribeRulesResult",
		"<Rules>"+strings.Join(members, "")+"</Rules><NextMarker/>")
}

func (s *Service) deleteRule(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("RuleArn")
	s.mu.Lock()
	delete(s.rules, arn)
	s.mu.Unlock()
	elbOK(w, "DeleteRuleResponse", "DeleteRuleResult", "")
}

func (s *Service) modifyRule(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("RuleArn")
	s.mu.Lock()
	ru, ok := s.rules[arn]
	if ok {
		conds := parseConditions(r, "Conditions.member.")
		if len(conds) > 0 {
			ru.conditions = conds
		}
		if v := r.FormValue("Actions.member.1.Type"); v != "" {
			ru.action.actionType = v
		}
		if v := r.FormValue("Actions.member.1.TargetGroupArn"); v != "" {
			ru.action.targetGroupARN = v
		}
	}
	s.mu.Unlock()
	if !ok {
		elbError(w, http.StatusBadRequest, "RuleNotFound", "The specified rule does not exist.")
		return
	}
	s.mu.RLock()
	mem := ruleMember(ru)
	s.mu.RUnlock()
	elbOK(w, "ModifyRuleResponse", "ModifyRuleResult",
		"<Rules>"+mem+"</Rules>")
}

func (s *Service) setRulePriorities(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	for i := 1; ; i++ {
		rArn := r.FormValue(fmt.Sprintf("RulePriorities.member.%d.RuleArn", i))
		if rArn == "" {
			break
		}
		pri := r.FormValue(fmt.Sprintf("RulePriorities.member.%d.Priority", i))
		if ru, ok := s.rules[rArn]; ok {
			ru.priority = pri
		}
	}
	s.mu.Unlock()
	elbOK(w, "SetRulePrioritiesResponse", "SetRulePrioritiesResult", "<Rules/>")
}

// --- Target Group operations ---

func (s *Service) createTargetGroup(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("Name")
	if name == "" {
		elbError(w, http.StatusBadRequest, "ValidationError", "Name is required")
		return
	}
	protocol := r.FormValue("Protocol")
	if protocol == "" {
		protocol = "HTTP"
	}
	port := r.FormValue("Port")
	if port == "" {
		port = "80"
	}
	vpcID := r.FormValue("VpcId")
	if vpcID == "" {
		vpcID = "vpc-00000000000000001"
	}
	targetType := r.FormValue("TargetType")
	if targetType == "" {
		targetType = "instance"
	}

	id := shortID()
	arn := fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:targetgroup/%s/%s",
		s.region, accountID, name, id)

	tg := &targetGroup{
		arn: arn, name: name, protocol: protocol, port: port,
		vpcID: vpcID, targetType: targetType, createdAt: time.Now().UTC(),
		targets: map[string]*registeredTarget{},
	}

	s.mu.Lock()
	s.targetGroups[arn] = tg
	s.mu.Unlock()

	elbOK(w, "CreateTargetGroupResponse", "CreateTargetGroupResult",
		"<TargetGroups>"+tgMember(tg, nil)+"</TargetGroups>")
}

func (s *Service) describeTargetGroups(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	filterARNs := formList(r, "TargetGroupArns.member.")
	filterNames := formList(r, "Names.member.")

	var members []string
	for _, tg := range s.targetGroups {
		if len(filterARNs) > 0 && !contains(filterARNs, tg.arn) {
			continue
		}
		if len(filterNames) > 0 && !contains(filterNames, tg.name) {
			continue
		}
		members = append(members, tgMember(tg, s.lbARNsForTG(tg.arn)))
	}

	if len(filterARNs) > 0 && len(members) == 0 {
		elbError(w, http.StatusBadRequest, "TargetGroupNotFound",
			"One or more of the specified target groups do not exist.")
		return
	}

	elbOK(w, "DescribeTargetGroupsResponse", "DescribeTargetGroupsResult",
		"<TargetGroups>"+strings.Join(members, "")+"</TargetGroups><NextMarker/>")
}

func (s *Service) deleteTargetGroup(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("TargetGroupArn")
	s.mu.Lock()
	delete(s.targetGroups, arn)
	s.mu.Unlock()
	elbOK(w, "DeleteTargetGroupResponse", "DeleteTargetGroupResult", "")
}

func (s *Service) modifyTargetGroup(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("TargetGroupArn")
	s.mu.RLock()
	tg := s.targetGroups[arn]
	var lbARNs []string
	if tg != nil {
		lbARNs = s.lbARNsForTG(arn)
	}
	s.mu.RUnlock()
	if tg == nil {
		elbError(w, http.StatusBadRequest, "TargetGroupNotFound", "The specified target group does not exist.")
		return
	}
	elbOK(w, "ModifyTargetGroupResponse", "ModifyTargetGroupResult",
		"<TargetGroups>"+tgMember(tg, lbARNs)+"</TargetGroups>")
}

func (s *Service) describeTargetGroupAttributes(w http.ResponseWriter, _ *http.Request) {
	attrs := `<member><Key>deregistration_delay.timeout_seconds</Key><Value>300</Value></member>` +
		`<member><Key>stickiness.enabled</Key><Value>false</Value></member>` +
		`<member><Key>stickiness.type</Key><Value>lb_cookie</Value></member>` +
		`<member><Key>stickiness.lb_cookie.duration_seconds</Key><Value>86400</Value></member>` +
		`<member><Key>load_balancing.algorithm.type</Key><Value>round_robin</Value></member>` +
		`<member><Key>slow_start.duration_seconds</Key><Value>0</Value></member>` +
		`<member><Key>lambda.multi_value_headers.enabled</Key><Value>false</Value></member>` +
		`<member><Key>preserve_client_ip.enabled</Key><Value>false</Value></member>` +
		`<member><Key>proxy_protocol_v2.enabled</Key><Value>false</Value></member>`
	elbOK(w, "DescribeTargetGroupAttributesResponse", "DescribeTargetGroupAttributesResult",
		"<Attributes>"+attrs+"</Attributes>")
}

func (s *Service) modifyTargetGroupAttributes(w http.ResponseWriter, _ *http.Request) {
	elbOK(w, "ModifyTargetGroupAttributesResponse", "ModifyTargetGroupAttributesResult",
		"<Attributes/>")
}

// --- Target registration ---

func (s *Service) registerTargets(w http.ResponseWriter, r *http.Request) {
	tgARN := r.FormValue("TargetGroupArn")
	s.mu.Lock()
	tg, ok := s.targetGroups[tgARN]
	if ok {
		for i := 1; ; i++ {
			id := r.FormValue(fmt.Sprintf("Targets.member.%d.Id", i))
			if id == "" {
				break
			}
			port := r.FormValue(fmt.Sprintf("Targets.member.%d.Port", i))
			tg.targets[id+":"+port] = &registeredTarget{id: id, port: port}
		}
	}
	s.mu.Unlock()
	if !ok {
		elbError(w, http.StatusBadRequest, "TargetGroupNotFound",
			"The specified target group does not exist.")
		return
	}
	elbOK(w, "RegisterTargetsResponse", "RegisterTargetsResult", "")
}

func (s *Service) deregisterTargets(w http.ResponseWriter, r *http.Request) {
	tgARN := r.FormValue("TargetGroupArn")
	s.mu.Lock()
	tg, ok := s.targetGroups[tgARN]
	if ok {
		for i := 1; ; i++ {
			id := r.FormValue(fmt.Sprintf("Targets.member.%d.Id", i))
			if id == "" {
				break
			}
			port := r.FormValue(fmt.Sprintf("Targets.member.%d.Port", i))
			delete(tg.targets, id+":"+port)
		}
	}
	s.mu.Unlock()
	elbOK(w, "DeregisterTargetsResponse", "DeregisterTargetsResult", "")
}

func (s *Service) describeTargetHealth(w http.ResponseWriter, r *http.Request) {
	tgARN := r.FormValue("TargetGroupArn")
	s.mu.RLock()
	tg, ok := s.targetGroups[tgARN]
	var members []string
	if ok {
		filterIDs := formList(r, "Targets.member.")
		for _, t := range tg.targets {
			if len(filterIDs) > 0 && !contains(filterIDs, t.id) {
				continue
			}
			port := t.port
			if port == "" {
				port = tg.port
			}
			members = append(members, targetHealthMember(t.id, port))
		}
	}
	s.mu.RUnlock()
	elbOK(w, "DescribeTargetHealthResponse", "DescribeTargetHealthResult",
		"<TargetHealthDescriptions>"+strings.Join(members, "")+"</TargetHealthDescriptions>")
}

func (s *Service) describeTags(w http.ResponseWriter, r *http.Request) {
	arns := formList(r, "ResourceArns.member.")
	var descs strings.Builder
	for _, arn := range arns {
		descs.WriteString(fmt.Sprintf("<member><ResourceArn>%s</ResourceArn><Tags/></member>", xmlEsc(arn)))
	}
	elbOK(w, "DescribeTagsResponse", "DescribeTagsResult",
		"<TagDescriptions>"+descs.String()+"</TagDescriptions>")
}

// --- Reverse proxy ---

func (s *Service) startProxy(port, listenerARN string) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.handleProxyRequest(w, r, listenerARN)
	})
	srv := &http.Server{Addr: ":" + port, Handler: handler}

	s.mu.Lock()
	s.proxies[port] = &activeProxy{srv: srv}
	s.mu.Unlock()

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Warn("alb proxy stopped", "port", port, "err", err)
		}
	}()
	slog.Info("alb proxy started", "port", port)
}

// stopProxyLocked stops the proxy for port. Caller must NOT hold s.mu.
func (s *Service) stopProxyLocked(port string) {
	p, ok := s.proxies[port]
	if !ok {
		return
	}
	delete(s.proxies, port)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = p.srv.Shutdown(ctx)
		slog.Info("alb proxy stopped", "port", port)
	}()
}

// portInUse returns true if any remaining listener uses port. Caller must hold s.mu.
func (s *Service) portInUse(port string) bool {
	for _, l := range s.listeners {
		if l.port == port {
			return true
		}
	}
	return false
}

// removeListenerRules deletes all rules belonging to listenerARN. Caller must hold s.mu.
func (s *Service) removeListenerRules(listenerARN string) {
	for rArn, ru := range s.rules {
		if ru.listenerARN == listenerARN {
			delete(s.rules, rArn)
		}
	}
}

func (s *Service) handleProxyRequest(w http.ResponseWriter, r *http.Request, listenerARN string) {
	// Gather rules under read lock, then release before making outbound requests.
	s.mu.RLock()
	rules := s.sortedRulesForListener(listenerARN)
	s.mu.RUnlock()

	var tgARN string
	for _, ru := range rules {
		if matchConditions(ru.conditions, r) {
			tgARN = ru.action.targetGroupARN
			break
		}
	}
	if tgARN == "" {
		http.Error(w, "no matching rule", http.StatusServiceUnavailable)
		return
	}

	target := s.pickTarget(tgARN)
	if target == nil {
		http.Error(w, "no available targets", http.StatusServiceUnavailable)
		return
	}

	port := target.port
	if port == "" {
		s.mu.RLock()
		if tg, ok := s.targetGroups[tgARN]; ok {
			port = tg.port
		}
		s.mu.RUnlock()
	}

	targetURL, err := url.Parse(fmt.Sprintf("http://%s:%s", target.id, port))
	if err != nil {
		http.Error(w, "invalid target address", http.StatusServiceUnavailable)
		return
	}

	rp := httputil.NewSingleHostReverseProxy(targetURL)
	rp.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		slog.Warn("alb proxy upstream error", "target", targetURL.Host, "err", err)
		http.Error(w, "upstream error: "+err.Error(), http.StatusBadGateway)
	}
	rp.ServeHTTP(w, r)
}

// sortedRulesForListener returns rules for the listener sorted by priority.
// Caller must hold s.mu (read or write).
func (s *Service) sortedRulesForListener(listenerARN string) []*rule {
	var out []*rule
	for _, ru := range s.rules {
		if ru.listenerARN == listenerARN {
			out = append(out, ru)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return rulePriorityCmp(out[i], out[j])
	})
	return out
}

// pickTarget picks a random registered target from the given target group.
func (s *Service) pickTarget(tgARN string) *registeredTarget {
	s.mu.RLock()
	tg, ok := s.targetGroups[tgARN]
	if !ok || len(tg.targets) == 0 {
		s.mu.RUnlock()
		return nil
	}
	targets := make([]*registeredTarget, 0, len(tg.targets))
	for _, t := range tg.targets {
		targets = append(targets, t)
	}
	s.mu.RUnlock()
	return targets[rand.Intn(len(targets))] //nolint:gosec
}

// --- Rule matching ---

func matchConditions(conditions []condition, r *http.Request) bool {
	for _, c := range conditions {
		if !matchCondition(c, r) {
			return false
		}
	}
	return true // empty conditions = default rule
}

func matchCondition(c condition, r *http.Request) bool {
	switch c.field {
	case "path-pattern":
		for _, pattern := range c.values {
			if globMatch(pattern, r.URL.Path) {
				return true
			}
		}
		return false
	case "host-header":
		host := r.Host
		if h, _, err := splitHostPort(host); err == nil {
			host = h
		}
		for _, pattern := range c.values {
			if globMatch(pattern, host) {
				return true
			}
		}
		return false
	default:
		return true
	}
}

// globMatch matches s against a simple glob pattern where * matches any sequence.
func globMatch(pattern, s string) bool {
	if pattern == "*" {
		return true
	}
	return globMatchRec(pattern, s)
}

func globMatchRec(pattern, s string) bool {
	for len(pattern) > 0 {
		if pattern[0] == '*' {
			for i := 0; i <= len(s); i++ {
				if globMatchRec(pattern[1:], s[i:]) {
					return true
				}
			}
			return false
		}
		if len(s) == 0 || pattern[0] != s[0] {
			return false
		}
		pattern, s = pattern[1:], s[1:]
	}
	return len(s) == 0
}

func splitHostPort(host string) (string, string, error) {
	idx := strings.LastIndex(host, ":")
	if idx < 0 {
		return host, "", fmt.Errorf("no port")
	}
	return host[:idx], host[idx+1:], nil
}

// --- XML builders ---

func lbMember(l *lb) string {
	return fmt.Sprintf(
		`<member>`+
			`<LoadBalancerArn>%s</LoadBalancerArn>`+
			`<LoadBalancerName>%s</LoadBalancerName>`+
			`<DNSName>%s</DNSName>`+
			`<CanonicalHostedZoneId>Z00000000000000</CanonicalHostedZoneId>`+
			`<CreatedTime>%s</CreatedTime>`+
			`<Scheme>%s</Scheme>`+
			`<VpcId>vpc-00000000000000001</VpcId>`+
			`<State><Code>active</Code><Reason/></State>`+
			`<Type>%s</Type>`+
			`%s`+
			`<SecurityGroups/>`+
			`<IpAddressType>ipv4</IpAddressType>`+
			`</member>`,
		xmlEsc(l.arn), xmlEsc(l.name), xmlEsc(l.dnsName),
		l.createdAt.Format(time.RFC3339),
		xmlEsc(l.scheme), xmlEsc(l.lbType), availabilityZonesXML(l.subnets),
	)
}

// availabilityZonesXML renders the <AvailabilityZones> element for a load
// balancer's subnets. Falls back to a single synthetic zone when no subnets
// were recorded (backwards compat with callers that omit them).
func availabilityZonesXML(subnets []lbSubnet) string {
	if len(subnets) == 0 {
		return `<AvailabilityZones><member><ZoneName>us-east-1a</ZoneName>` +
			`<SubnetId>subnet-00000000000000001</SubnetId><LoadBalancerAddresses/></member></AvailabilityZones>`
	}
	var b strings.Builder
	b.WriteString("<AvailabilityZones>")
	for _, sn := range subnets {
		fmt.Fprintf(&b, `<member><ZoneName>%s</ZoneName><SubnetId>%s</SubnetId><LoadBalancerAddresses/></member>`,
			xmlEsc(sn.zoneName), xmlEsc(sn.subnetID))
	}
	b.WriteString("</AvailabilityZones>")
	return b.String()
}

func listenerMember(l *listener) string {
	da := fmt.Sprintf(
		`<member>`+
			`<Type>%s</Type>`+
			`<TargetGroupArn>%s</TargetGroupArn>`+
			`<Order>1</Order>`+
			`<ForwardConfig>`+
			`<TargetGroups><member><TargetGroupArn>%s</TargetGroupArn><Weight>1</Weight></member></TargetGroups>`+
			`<TargetGroupStickinessConfig><Enabled>false</Enabled></TargetGroupStickinessConfig>`+
			`</ForwardConfig>`+
			`</member>`,
		xmlEsc(l.defaultAction.actionType),
		xmlEsc(l.defaultAction.targetGroupARN),
		xmlEsc(l.defaultAction.targetGroupARN),
	)
	return fmt.Sprintf(
		`<member>`+
			`<ListenerArn>%s</ListenerArn>`+
			`<LoadBalancerArn>%s</LoadBalancerArn>`+
			`<Protocol>%s</Protocol>`+
			`<Port>%s</Port>`+
			`<DefaultActions>%s</DefaultActions>`+
			`<SslPolicy/><Certificates/><AlpnPolicy/>`+
			`<MutualAuthentication><Mode>off</Mode></MutualAuthentication>`+
			`</member>`,
		xmlEsc(l.arn), xmlEsc(l.lbARN),
		xmlEsc(l.protocol), xmlEsc(l.port),
		da,
	)
}

func ruleMember(ru *rule) string {
	var conds strings.Builder
	for _, c := range ru.conditions {
		var vals strings.Builder
		for _, v := range c.values {
			vals.WriteString(fmt.Sprintf("<member>%s</member>", xmlEsc(v)))
		}
		conds.WriteString(fmt.Sprintf(
			`<member><Field>%s</Field><Values>%s</Values></member>`,
			xmlEsc(c.field), vals.String(),
		))
	}
	action := fmt.Sprintf(
		`<member><Type>%s</Type><TargetGroupArn>%s</TargetGroupArn><Order>1</Order></member>`,
		xmlEsc(ru.action.actionType), xmlEsc(ru.action.targetGroupARN),
	)
	isDefault := "false"
	if ru.isDefault {
		isDefault = "true"
	}
	return fmt.Sprintf(
		`<member>`+
			`<RuleArn>%s</RuleArn>`+
			`<Priority>%s</Priority>`+
			`<Conditions>%s</Conditions>`+
			`<Actions>%s</Actions>`+
			`<IsDefault>%s</IsDefault>`+
			`</member>`,
		xmlEsc(ru.arn), xmlEsc(ru.priority),
		conds.String(), action, isDefault,
	)
}

// lbARNsForTG returns the ARNs of every load balancer attached to the target
// group — via a listener default action or a listener rule that forwards to
// it — de-duplicated and sorted. Must be called with s.mu held.
func (s *Service) lbARNsForTG(tgARN string) []string {
	set := map[string]bool{}
	for _, l := range s.listeners {
		if l.defaultAction.targetGroupARN == tgARN {
			set[l.lbARN] = true
		}
	}
	for _, ru := range s.rules {
		if ru.action.targetGroupARN != tgARN {
			continue
		}
		if l, ok := s.listeners[ru.listenerARN]; ok {
			set[l.lbARN] = true
		}
	}
	arns := make([]string, 0, len(set))
	for arn := range set {
		arns = append(arns, arn)
	}
	sort.Strings(arns)
	return arns
}

func tgMember(tg *targetGroup, lbARNs []string) string {
	hcProtocol := tg.protocol
	if hcProtocol == "" {
		hcProtocol = "HTTP"
	}
	lbList := "<LoadBalancerArns/>"
	if len(lbARNs) > 0 {
		var b strings.Builder
		b.WriteString("<LoadBalancerArns>")
		for _, arn := range lbARNs {
			b.WriteString("<member>" + xmlEsc(arn) + "</member>")
		}
		b.WriteString("</LoadBalancerArns>")
		lbList = b.String()
	}
	return fmt.Sprintf(
		`<member>`+
			`<TargetGroupArn>%s</TargetGroupArn>`+
			`<TargetGroupName>%s</TargetGroupName>`+
			`<Protocol>%s</Protocol>`+
			`<Port>%s</Port>`+
			`<VpcId>%s</VpcId>`+
			`<TargetType>%s</TargetType>`+
			`<HealthCheckEnabled>true</HealthCheckEnabled>`+
			`<HealthCheckIntervalSeconds>30</HealthCheckIntervalSeconds>`+
			`<HealthCheckPath>/</HealthCheckPath>`+
			`<HealthCheckPort>traffic-port</HealthCheckPort>`+
			`<HealthCheckProtocol>%s</HealthCheckProtocol>`+
			`<HealthCheckTimeoutSeconds>5</HealthCheckTimeoutSeconds>`+
			`<HealthyThresholdCount>5</HealthyThresholdCount>`+
			`<UnhealthyThresholdCount>2</UnhealthyThresholdCount>`+
			`<Matcher><HttpCode>200</HttpCode></Matcher>`+
			`%s`+
			`<ProtocolVersion>HTTP1</ProtocolVersion>`+
			`</member>`,
		xmlEsc(tg.arn), xmlEsc(tg.name),
		xmlEsc(tg.protocol), xmlEsc(tg.port), xmlEsc(tg.vpcID), xmlEsc(tg.targetType),
		xmlEsc(hcProtocol), lbList,
	)
}

func targetHealthMember(id, port string) string {
	return fmt.Sprintf(
		`<member>`+
			`<Target><Id>%s</Id><Port>%s</Port></Target>`+
			`<HealthCheckPort>%s</HealthCheckPort>`+
			`<TargetHealth><State>healthy</State></TargetHealth>`+
			`</member>`,
		xmlEsc(id), xmlEsc(port), xmlEsc(port),
	)
}

func elbOK(w http.ResponseWriter, resp, result, body string) {
	w.Header().Set("Content-Type", "text/xml")
	reqID := uid.New()
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>`)
	fmt.Fprintf(w, `<%s xmlns="%s">`, resp, elbNS)
	fmt.Fprintf(w, `<%s>%s</%s>`, result, body, result)
	fmt.Fprintf(w, `<ResponseMetadata><RequestId>%s</RequestId></ResponseMetadata>`, reqID)
	fmt.Fprintf(w, `</%s>`, resp)
}

func elbError(w http.ResponseWriter, code int, errCode, msg string) {
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(code)
	reqID := uid.New()
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>`)
	fmt.Fprintf(w, `<ErrorResponse xmlns="%s">`, elbNS)
	fmt.Fprintf(w, `<Error><Code>%s</Code><Message>%s</Message></Error>`, xmlEsc(errCode), xmlEsc(msg))
	fmt.Fprintf(w, `<RequestId>%s</RequestId>`, reqID)
	fmt.Fprintf(w, `</ErrorResponse>`)
}

// --- Helpers ---

func xmlEsc(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}

func formList(r *http.Request, prefix string) []string {
	var out []string
	for i := 1; ; i++ {
		v := r.FormValue(fmt.Sprintf("%s%d", prefix, i))
		if v == "" {
			break
		}
		out = append(out, v)
	}
	return out
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// parseConditions reads Conditions.member.N.Field / .Values.member.M from the form.
// Also checks the typed config key (PathPatternConfig, HostHeaderConfig, etc.).
func parseConditions(r *http.Request, prefix string) []condition {
	var out []condition
	for i := 1; ; i++ {
		field := r.FormValue(fmt.Sprintf("%s%d.Field", prefix, i))
		if field == "" {
			break
		}
		// Try flat Values first, then typed config.
		configKey := fieldConfigKey(field)
		var values []string
		for j := 1; ; j++ {
			v := r.FormValue(fmt.Sprintf("%s%d.Values.member.%d", prefix, i, j))
			if v == "" && configKey != "" {
				v = r.FormValue(fmt.Sprintf("%s%d.%s.Values.member.%d", prefix, i, configKey, j))
			}
			if v == "" {
				break
			}
			values = append(values, v)
		}
		out = append(out, condition{field: field, values: values})
	}
	return out
}

func fieldConfigKey(field string) string {
	switch field {
	case "path-pattern":
		return "PathPatternConfig"
	case "host-header":
		return "HostHeaderConfig"
	case "http-header":
		return "HttpHeaderConfig"
	case "query-string":
		return "QueryStringConfig"
	case "source-ip":
		return "SourceIpConfig"
	default:
		return ""
	}
}

// ruleARN builds a rule ARN by replacing ":listener/" with ":listener-rule/" and appending the rule ID.
func ruleARN(listenerARN, ruleID string) string {
	return strings.Replace(listenerARN, ":listener/", ":listener-rule/", 1) + "/" + ruleID
}

// lbARNSuffix extracts "app/{name}/{id}" from a full LB ARN.
func lbARNSuffix(arn string) string {
	idx := strings.Index(arn, ":loadbalancer/")
	if idx < 0 {
		return "app/unknown/00000000"
	}
	return arn[idx+len(":loadbalancer/"):]
}

// rulePriorityCmp returns true if rule i should sort before rule j.
// Non-default rules are sorted by numeric priority ascending; default rule sorts last.
func rulePriorityCmp(i, j *rule) bool {
	if i.isDefault {
		return false
	}
	if j.isDefault {
		return true
	}
	pi, _ := strconv.Atoi(i.priority)
	pj, _ := strconv.Atoi(j.priority)
	return pi < pj
}

func shortID() string {
	raw := strings.ReplaceAll(uid.New(), "-", "")
	if len(raw) > 16 {
		return raw[:16]
	}
	return raw
}
