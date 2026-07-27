package alb

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func newSvc() *Service { return New("us-east-1", nil) }

// elbReq sends a form-encoded ELBv2 request.
func elbReq(t *testing.T, s *Service, action string, params map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{}
	form.Set("Action", action)
	form.Set("Version", "2015-12-01")
	for k, v := range params {
		form.Set(k, v)
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	return w
}

// createLB is a test helper that creates a load balancer and returns its ARN.
func createLB(t *testing.T, s *Service, name string) string {
	t.Helper()
	w := elbReq(t, s, "CreateLoadBalancer", map[string]string{
		"Name":             name,
		"Subnets.member.1": "subnet-aaa",
		"Subnets.member.2": "subnet-bbb",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("CreateLoadBalancer: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	return extractXMLValue(t, w.Body.String(), "LoadBalancerArn")
}

// createTG creates a target group and returns its ARN.
func createTG(t *testing.T, s *Service, name string) string {
	t.Helper()
	w := elbReq(t, s, "CreateTargetGroup", map[string]string{
		"Name":       name,
		"Protocol":   "HTTP",
		"Port":       "80",
		"VpcId":      "vpc-123",
		"TargetType": "ip",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("CreateTargetGroup: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	return extractXMLValue(t, w.Body.String(), "TargetGroupArn")
}

// createListener creates a listener and returns its ARN.
// Uses a port in the ephemeral range; proxy startup failure is non-fatal.
func createListener(t *testing.T, s *Service, lbARN, tgARN, port string) string {
	t.Helper()
	w := elbReq(t, s, "CreateListener", map[string]string{
		"LoadBalancerArn":                        lbARN,
		"Protocol":                               "HTTP",
		"Port":                                   port,
		"DefaultActions.member.1.Type":           "forward",
		"DefaultActions.member.1.TargetGroupArn": tgARN,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("CreateListener: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	return extractXMLValue(t, w.Body.String(), "ListenerArn")
}

// extractXMLValue pulls the first occurrence of an element value from XML text.
func extractXMLValue(t *testing.T, body, tag string) string {
	t.Helper()
	type any struct {
		XMLName xml.Name
		Inner   string `xml:",innerxml"`
	}
	dec := xml.NewDecoder(strings.NewReader(body))
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == tag {
			var val string
			dec.DecodeElement(&val, &se) //nolint:errcheck
			return val
		}
	}
	t.Fatalf("extractXMLValue: tag <%s> not found in:\n%s", tag, body)
	return ""
}

// --- Detect ---

func TestDetect(t *testing.T) {
	s := newSvc()
	form := url.Values{}
	form.Set("Version", "2015-12-01")
	form.Set("Action", "DescribeLoadBalancers")
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if !s.Detect(req) {
		t.Fatal("expected Detect=true")
	}
}

func TestDetect_Miss(t *testing.T) {
	s := newSvc()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Content-Type", "application/json")
	if s.Detect(req) {
		t.Fatal("expected Detect=false for non-form-encoded request")
	}
}

// --- CreateLoadBalancer ---

func TestCreateLoadBalancer(t *testing.T) {
	s := newSvc()
	w := elbReq(t, s, "CreateLoadBalancer", map[string]string{
		"Name":             "my-lb",
		"Subnets.member.1": "subnet-aaa",
		"Subnets.member.2": "subnet-bbb",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "my-lb") {
		t.Error("expected LB name in response")
	}
	if !strings.Contains(w.Body.String(), "cloudfront.localhost") &&
		!strings.Contains(w.Body.String(), "localhost") {
		t.Error("expected localhost-based DNSName")
	}
	// The provided subnets should be reflected in the AvailabilityZones.
	if !strings.Contains(w.Body.String(), "subnet-aaa") ||
		!strings.Contains(w.Body.String(), "subnet-bbb") {
		t.Error("expected provided subnets in AvailabilityZones")
	}
}

// TestCreateLoadBalancer_SingleSubnet: an ALB with a single subnet (one AZ) is
// rejected, matching real AWS.
func TestCreateLoadBalancer_SingleSubnet(t *testing.T) {
	s := newSvc()
	w := elbReq(t, s, "CreateLoadBalancer", map[string]string{
		"Name":             "single-az",
		"Subnets.member.1": "subnet-aaa",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d\n%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "ValidationError") {
		t.Errorf("expected ValidationError, got:\n%s", w.Body.String())
	}
}

// TestCreateLoadBalancer_SameAZ: two subnets that resolve to the same AZ are
// rejected.
func TestCreateLoadBalancer_SameAZ(t *testing.T) {
	s := New("us-east-1", func(string) (string, bool) { return "us-east-1a", true })
	w := elbReq(t, s, "CreateLoadBalancer", map[string]string{
		"Name":             "same-az",
		"Subnets.member.1": "subnet-aaa",
		"Subnets.member.2": "subnet-bbb",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d\n%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "ValidationError") {
		t.Errorf("expected ValidationError, got:\n%s", w.Body.String())
	}
}

// TestCreateLoadBalancer_DifferentAZ: two subnets in different AZs are accepted.
func TestCreateLoadBalancer_DifferentAZ(t *testing.T) {
	azByID := map[string]string{"subnet-aaa": "us-east-1a", "subnet-bbb": "us-east-1b"}
	s := New("us-east-1", func(id string) (string, bool) {
		az, ok := azByID[id]
		return az, ok
	})
	w := elbReq(t, s, "CreateLoadBalancer", map[string]string{
		"Name":             "multi-az",
		"Subnets.member.1": "subnet-aaa",
		"Subnets.member.2": "subnet-bbb",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "us-east-1a") || !strings.Contains(body, "us-east-1b") {
		t.Errorf("expected both AZs in response, got:\n%s", body)
	}
}

func TestCreateLoadBalancer_MissingName(t *testing.T) {
	s := newSvc()
	w := elbReq(t, s, "CreateLoadBalancer", map[string]string{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- DescribeLoadBalancers ---

func TestDescribeLoadBalancers(t *testing.T) {
	s := newSvc()
	arn := createLB(t, s, "lb1")

	w := elbReq(t, s, "DescribeLoadBalancers", map[string]string{
		"LoadBalancerArns.member.1": arn,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "lb1") {
		t.Error("expected lb name in describe response")
	}
}

func TestDescribeLoadBalancers_NotFound(t *testing.T) {
	s := newSvc()
	w := elbReq(t, s, "DescribeLoadBalancers", map[string]string{
		"LoadBalancerArns.member.1": "arn:aws:elasticloadbalancing:us-east-1:000000000000:loadbalancer/app/nope/abc",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "LoadBalancerNotFound") {
		t.Error("expected LoadBalancerNotFound error")
	}
}

func TestDescribeLoadBalancers_All(t *testing.T) {
	s := newSvc()
	createLB(t, s, "lb-a")
	createLB(t, s, "lb-b")

	w := elbReq(t, s, "DescribeLoadBalancers", map[string]string{})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if strings.Count(w.Body.String(), "<LoadBalancerArn>") < 2 {
		t.Error("expected at least 2 load balancers")
	}
}

// --- DeleteLoadBalancer ---

func TestDeleteLoadBalancer(t *testing.T) {
	s := newSvc()
	arn := createLB(t, s, "to-delete")

	w := elbReq(t, s, "DeleteLoadBalancer", map[string]string{"LoadBalancerArn": arn})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	// Confirm gone
	w2 := elbReq(t, s, "DescribeLoadBalancers", map[string]string{
		"LoadBalancerArns.member.1": arn,
	})
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 after delete, got %d", w2.Code)
	}
}

// --- SetSubnets ---

func TestSetSubnets(t *testing.T) {
	s := newSvc()
	arn := createLB(t, s, "lb-subnets")

	w := elbReq(t, s, "SetSubnets", map[string]string{
		"LoadBalancerArn":  arn,
		"Subnets.member.1": "subnet-new",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "AvailabilityZones") {
		t.Error("expected AvailabilityZones in SetSubnets response")
	}
}

func TestSetSubnets_NotFound(t *testing.T) {
	s := newSvc()
	w := elbReq(t, s, "SetSubnets", map[string]string{
		"LoadBalancerArn": "arn:nope",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- LB Attributes ---

func TestDescribeLoadBalancerAttributes(t *testing.T) {
	s := newSvc()
	w := elbReq(t, s, "DescribeLoadBalancerAttributes", map[string]string{})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "idle_timeout") {
		t.Error("expected idle_timeout attribute")
	}
}

func TestModifyLoadBalancerAttributes(t *testing.T) {
	s := newSvc()
	w := elbReq(t, s, "ModifyLoadBalancerAttributes", map[string]string{
		"Attributes.member.1.Key":   "idle_timeout.timeout_seconds",
		"Attributes.member.1.Value": "120",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// --- DescribeCapacityReservation ---

func TestDescribeCapacityReservation(t *testing.T) {
	s := newSvc()
	w := elbReq(t, s, "DescribeCapacityReservation", map[string]string{})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "CapacityReservationState") {
		t.Error("expected CapacityReservationState in response")
	}
}

// --- CreateTargetGroup ---

func TestCreateTargetGroup(t *testing.T) {
	s := newSvc()
	w := elbReq(t, s, "CreateTargetGroup", map[string]string{
		"Name":       "my-tg",
		"Protocol":   "HTTP",
		"Port":       "8080",
		"VpcId":      "vpc-123",
		"TargetType": "instance",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "my-tg") {
		t.Error("expected TG name in response")
	}
}

func TestCreateTargetGroup_MissingName(t *testing.T) {
	s := newSvc()
	w := elbReq(t, s, "CreateTargetGroup", map[string]string{"Protocol": "HTTP"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- DescribeTargetGroups ---

func TestDescribeTargetGroups(t *testing.T) {
	s := newSvc()
	arn := createTG(t, s, "tg1")

	w := elbReq(t, s, "DescribeTargetGroups", map[string]string{
		"TargetGroupArns.member.1": arn,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "tg1") {
		t.Error("expected TG name in response")
	}
}

// --- DeleteTargetGroup ---

func TestDeleteTargetGroup(t *testing.T) {
	s := newSvc()
	arn := createTG(t, s, "tg-del")

	w := elbReq(t, s, "DeleteTargetGroup", map[string]string{"TargetGroupArn": arn})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// --- ModifyTargetGroup ---

func TestModifyTargetGroup(t *testing.T) {
	s := newSvc()
	arn := createTG(t, s, "tg-mod")

	w := elbReq(t, s, "ModifyTargetGroup", map[string]string{"TargetGroupArn": arn})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "tg-mod") {
		t.Error("expected TG name in ModifyTargetGroup response")
	}
}

func TestModifyTargetGroup_NotFound(t *testing.T) {
	s := newSvc()
	w := elbReq(t, s, "ModifyTargetGroup", map[string]string{"TargetGroupArn": "arn:nope"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- Target Group Attributes ---

func TestDescribeTargetGroupAttributes(t *testing.T) {
	s := newSvc()
	w := elbReq(t, s, "DescribeTargetGroupAttributes", map[string]string{})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "deregistration_delay") {
		t.Error("expected deregistration_delay attribute")
	}
}

func TestModifyTargetGroupAttributes(t *testing.T) {
	s := newSvc()
	w := elbReq(t, s, "ModifyTargetGroupAttributes", map[string]string{})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// --- Targets ---

func TestRegisterAndDescribeTargets(t *testing.T) {
	s := newSvc()
	tgARN := createTG(t, s, "tg-targets")

	w := elbReq(t, s, "RegisterTargets", map[string]string{
		"TargetGroupArn":        tgARN,
		"Targets.member.1.Id":   "10.0.0.1",
		"Targets.member.1.Port": "8080",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("RegisterTargets: expected 200, got %d", w.Code)
	}

	w2 := elbReq(t, s, "DescribeTargetHealth", map[string]string{"TargetGroupArn": tgARN})
	if w2.Code != http.StatusOK {
		t.Fatalf("DescribeTargetHealth: expected 200, got %d", w2.Code)
	}
	if !strings.Contains(w2.Body.String(), "healthy") {
		t.Error("expected target health state 'healthy'")
	}
}

func TestDeregisterTargets(t *testing.T) {
	s := newSvc()
	tgARN := createTG(t, s, "tg-dereg")

	elbReq(t, s, "RegisterTargets", map[string]string{
		"TargetGroupArn":        tgARN,
		"Targets.member.1.Id":   "10.0.0.2",
		"Targets.member.1.Port": "80",
	})
	w := elbReq(t, s, "DeregisterTargets", map[string]string{
		"TargetGroupArn":        tgARN,
		"Targets.member.1.Id":   "10.0.0.2",
		"Targets.member.1.Port": "80",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("DeregisterTargets: expected 200, got %d", w.Code)
	}

	w2 := elbReq(t, s, "DescribeTargetHealth", map[string]string{"TargetGroupArn": tgARN})
	if strings.Contains(w2.Body.String(), "10.0.0.2") {
		t.Error("expected deregistered target to be absent")
	}
}

// --- Listener CRUD ---

func TestCreateAndDescribeListener(t *testing.T) {
	s := newSvc()
	lbARN := createLB(t, s, "lb-with-listener")
	tgARN := createTG(t, s, "tg-for-listener")
	lARN := createListener(t, s, lbARN, tgARN, "49001")

	w := elbReq(t, s, "DescribeListeners", map[string]string{
		"ListenerArns.member.1": lARN,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("DescribeListeners: expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "49001") {
		t.Error("expected port 49001 in listener response")
	}
}

func TestCreateListener_MissingLBARN(t *testing.T) {
	s := newSvc()
	w := elbReq(t, s, "CreateListener", map[string]string{"Port": "80"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestDeleteListener(t *testing.T) {
	s := newSvc()
	lbARN := createLB(t, s, "lb-del-listener")
	tgARN := createTG(t, s, "tg-del-listener")
	lARN := createListener(t, s, lbARN, tgARN, "49002")

	w := elbReq(t, s, "DeleteListener", map[string]string{"ListenerArn": lARN})
	if w.Code != http.StatusOK {
		t.Fatalf("DeleteListener: expected 200, got %d", w.Code)
	}
	w2 := elbReq(t, s, "DescribeListeners", map[string]string{
		"ListenerArns.member.1": lARN,
	})
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 after delete, got %d", w2.Code)
	}
}

func TestModifyListener(t *testing.T) {
	s := newSvc()
	lbARN := createLB(t, s, "lb-mod-listener")
	tgARN := createTG(t, s, "tg-mod-listener")
	lARN := createListener(t, s, lbARN, tgARN, "49003")

	w := elbReq(t, s, "ModifyListener", map[string]string{
		"ListenerArn": lARN,
		"Port":        "49003",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("ModifyListener: expected 200, got %d", w.Code)
	}
}

func TestModifyListener_NotFound(t *testing.T) {
	s := newSvc()
	w := elbReq(t, s, "ModifyListener", map[string]string{"ListenerArn": "arn:nope"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- Listener Attributes ---

func TestDescribeListenerAttributes(t *testing.T) {
	s := newSvc()
	w := elbReq(t, s, "DescribeListenerAttributes", map[string]string{})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestModifyListenerAttributes(t *testing.T) {
	s := newSvc()
	w := elbReq(t, s, "ModifyListenerAttributes", map[string]string{})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// --- Rules ---

func TestCreateDescribeDeleteRule(t *testing.T) {
	s := newSvc()
	lbARN := createLB(t, s, "lb-rules")
	tgARN := createTG(t, s, "tg-rules")
	lARN := createListener(t, s, lbARN, tgARN, "49004")

	w := elbReq(t, s, "CreateRule", map[string]string{
		"ListenerArn":                         lARN,
		"Priority":                            "10",
		"Conditions.member.1.Field":           "path-pattern",
		"Conditions.member.1.Values.member.1": "/api/*",
		"Actions.member.1.Type":               "forward",
		"Actions.member.1.TargetGroupArn":     tgARN,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("CreateRule: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	ruleARN := extractXMLValue(t, w.Body.String(), "RuleArn")

	w2 := elbReq(t, s, "DescribeRules", map[string]string{
		"RuleArns.member.1": ruleARN,
	})
	if w2.Code != http.StatusOK {
		t.Fatalf("DescribeRules: expected 200, got %d", w2.Code)
	}
	if !strings.Contains(w2.Body.String(), "/api/*") {
		t.Error("expected path-pattern condition in DescribeRules response")
	}

	w3 := elbReq(t, s, "DeleteRule", map[string]string{"RuleArn": ruleARN})
	if w3.Code != http.StatusOK {
		t.Fatalf("DeleteRule: expected 200, got %d", w3.Code)
	}
}

func TestCreateRule_MissingListenerARN(t *testing.T) {
	s := newSvc()
	w := elbReq(t, s, "CreateRule", map[string]string{"Priority": "1"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestSetRulePriorities(t *testing.T) {
	s := newSvc()
	lbARN := createLB(t, s, "lb-prio")
	tgARN := createTG(t, s, "tg-prio")
	lARN := createListener(t, s, lbARN, tgARN, "49005")

	w1 := elbReq(t, s, "CreateRule", map[string]string{
		"ListenerArn": lARN, "Priority": "10",
		"Conditions.member.1.Field":           "path-pattern",
		"Conditions.member.1.Values.member.1": "/a",
		"Actions.member.1.Type":               "forward",
		"Actions.member.1.TargetGroupArn":     tgARN,
	})
	ruleARN := extractXMLValue(t, w1.Body.String(), "RuleArn")

	w := elbReq(t, s, "SetRulePriorities", map[string]string{
		"RulePriorities.member.1.RuleArn":  ruleARN,
		"RulePriorities.member.1.Priority": "20",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("SetRulePriorities: expected 200, got %d", w.Code)
	}
}

func TestModifyRule(t *testing.T) {
	s := newSvc()
	lbARN := createLB(t, s, "lb-mod-rule")
	tgARN := createTG(t, s, "tg-mod-rule")
	lARN := createListener(t, s, lbARN, tgARN, "49006")

	w1 := elbReq(t, s, "CreateRule", map[string]string{
		"ListenerArn": lARN, "Priority": "5",
		"Conditions.member.1.Field":           "path-pattern",
		"Conditions.member.1.Values.member.1": "/old",
		"Actions.member.1.Type":               "forward",
		"Actions.member.1.TargetGroupArn":     tgARN,
	})
	ruleARN := extractXMLValue(t, w1.Body.String(), "RuleArn")

	w := elbReq(t, s, "ModifyRule", map[string]string{
		"RuleArn":                             ruleARN,
		"Conditions.member.1.Field":           "path-pattern",
		"Conditions.member.1.Values.member.1": "/new",
		"Actions.member.1.Type":               "forward",
		"Actions.member.1.TargetGroupArn":     tgARN,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("ModifyRule: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
}

// --- Tags ---

func TestAddRemoveDescribeTags(t *testing.T) {
	s := newSvc()
	arn := createLB(t, s, "lb-tags")

	w1 := elbReq(t, s, "AddTags", map[string]string{
		"ResourceArns.member.1": arn,
		"Tags.member.1.Key":     "env",
		"Tags.member.1.Value":   "dev",
	})
	if w1.Code != http.StatusOK {
		t.Fatalf("AddTags: expected 200, got %d", w1.Code)
	}

	w2 := elbReq(t, s, "RemoveTags", map[string]string{
		"ResourceArns.member.1": arn,
		"TagKeys.member.1":      "env",
	})
	if w2.Code != http.StatusOK {
		t.Fatalf("RemoveTags: expected 200, got %d", w2.Code)
	}

	w3 := elbReq(t, s, "DescribeTags", map[string]string{
		"ResourceArns.member.1": arn,
	})
	if w3.Code != http.StatusOK {
		t.Fatalf("DescribeTags: expected 200, got %d", w3.Code)
	}
}

// --- Unknown action ---

func TestUnknownAction(t *testing.T) {
	s := newSvc()
	w := elbReq(t, s, "NonExistentAction", map[string]string{})
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", w.Code)
	}
}

// --- Inspection endpoints ---

func TestLoadBalancersHandler(t *testing.T) {
	s := newSvc()
	createLB(t, s, "inspect-lb")

	req := httptest.NewRequest(http.MethodGet, "/_nimbus/alb/loadbalancers", nil)
	w := httptest.NewRecorder()
	s.LoadBalancersHandler(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "inspect-lb") {
		t.Error("expected LB name in inspection response")
	}
}

func TestTargetGroupsHandler(t *testing.T) {
	s := newSvc()
	createTG(t, s, "inspect-tg")

	req := httptest.NewRequest(http.MethodGet, "/_nimbus/alb/targetgroups", nil)
	w := httptest.NewRecorder()
	s.TargetGroupsHandler(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "inspect-tg") {
		t.Error("expected TG name in inspection response")
	}
}

func TestListenersHandler(t *testing.T) {
	s := newSvc()
	lbARN := createLB(t, s, "lb-inspect-listener")
	tgARN := createTG(t, s, "tg-inspect-listener")
	createListener(t, s, lbARN, tgARN, "49007")

	req := httptest.NewRequest(http.MethodGet, "/_nimbus/alb/listeners", nil)
	w := httptest.NewRecorder()
	s.ListenersHandler(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "49007") {
		t.Error("expected port in listeners inspection response")
	}
}

// --- Target group LoadBalancerArns ---

func TestDescribeTargetGroups_LoadBalancerArns(t *testing.T) {
	s := newSvc()
	lbARN := createLB(t, s, "lb-attach")
	tgDefault := createTG(t, s, "tg-default-action")
	tgRule := createTG(t, s, "tg-rule-action")
	tgOrphan := createTG(t, s, "tg-orphan")

	// tgDefault is attached via the listener's default action.
	lARN := createListener(t, s, lbARN, tgDefault, "49040")

	// tgRule is attached only via a listener rule.
	w := elbReq(t, s, "CreateRule", map[string]string{
		"ListenerArn":                         lARN,
		"Priority":                            "10",
		"Conditions.member.1.Field":           "path-pattern",
		"Conditions.member.1.Values.member.1": "/api/*",
		"Actions.member.1.Type":               "forward",
		"Actions.member.1.TargetGroupArn":     tgRule,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("CreateRule: expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	assertLBArns := func(tgARN string, want string) {
		t.Helper()
		w := elbReq(t, s, "DescribeTargetGroups", map[string]string{
			"TargetGroupArns.member.1": tgARN,
		})
		if w.Code != http.StatusOK {
			t.Fatalf("DescribeTargetGroups: expected 200, got %d\n%s", w.Code, w.Body.String())
		}
		body := w.Body.String()
		if want == "" {
			if !strings.Contains(body, "<LoadBalancerArns/>") {
				t.Errorf("expected empty <LoadBalancerArns/> for %s:\n%s", tgARN, body)
			}
			return
		}
		if !strings.Contains(body, "<LoadBalancerArns><member>"+want+"</member></LoadBalancerArns>") {
			t.Errorf("expected LoadBalancerArns [%s] for %s:\n%s", want, tgARN, body)
		}
	}

	assertLBArns(tgDefault, lbARN)
	assertLBArns(tgRule, lbARN)
	assertLBArns(tgOrphan, "")

	// Deleting the listener detaches both (rules belong to the listener, but
	// the rule store is independent — the rule's listener no longer resolves).
	w = elbReq(t, s, "DeleteListener", map[string]string{"ListenerArn": lARN})
	if w.Code != http.StatusOK {
		t.Fatalf("DeleteListener: expected 200, got %d", w.Code)
	}
	assertLBArns(tgDefault, "")
}

// --- Health check settings ---

// Health-check settings used to be hard-coded in the response, so any non-default
// check the caller configured drifted on every Terraform plan.
func TestCreateTargetGroup_HealthCheckRoundTrips(t *testing.T) {
	s := newSvc()
	w := elbReq(t, s, "CreateTargetGroup", map[string]string{
		"Name":                       "tg",
		"Protocol":                   "HTTP",
		"Port":                       "80",
		"HealthCheckPath":            "/health",
		"HealthyThresholdCount":      "2",
		"UnhealthyThresholdCount":    "4",
		"HealthCheckIntervalSeconds": "10",
		"HealthCheckTimeoutSeconds":  "3",
		"Matcher.HttpCode":           "200-299",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	for _, want := range []string{
		"<HealthCheckPath>/health</HealthCheckPath>",
		"<HealthyThresholdCount>2</HealthyThresholdCount>",
		"<UnhealthyThresholdCount>4</UnhealthyThresholdCount>",
		"<HealthCheckIntervalSeconds>10</HealthCheckIntervalSeconds>",
		"<HealthCheckTimeoutSeconds>3</HealthCheckTimeoutSeconds>",
		"<HttpCode>200-299</HttpCode>",
	} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("create response missing %q:\n%s", want, w.Body.String())
		}
	}

	// DescribeTargetGroups is what the provider reads on every plan.
	w = elbReq(t, s, "DescribeTargetGroups", map[string]string{"Names.member.1": "tg"})
	if !strings.Contains(w.Body.String(), "<HealthCheckPath>/health</HealthCheckPath>") {
		t.Errorf("describe lost the health check path:\n%s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "<HealthyThresholdCount>2</HealthyThresholdCount>") {
		t.Errorf("describe lost the healthy threshold:\n%s", w.Body.String())
	}
}

// Unset settings still report the AWS defaults.
func TestCreateTargetGroup_HealthCheckDefaults(t *testing.T) {
	s := newSvc()
	w := elbReq(t, s, "CreateTargetGroup", map[string]string{
		"Name": "tg", "Protocol": "HTTP", "Port": "80",
	})
	for _, want := range []string{
		"<HealthCheckEnabled>true</HealthCheckEnabled>",
		"<HealthCheckPath>/</HealthCheckPath>",
		"<HealthCheckIntervalSeconds>30</HealthCheckIntervalSeconds>",
		"<HealthyThresholdCount>5</HealthyThresholdCount>",
		"<UnhealthyThresholdCount>2</UnhealthyThresholdCount>",
		"<HttpCode>200</HttpCode>",
	} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("missing default %q:\n%s", want, w.Body.String())
		}
	}
}

// --- Listener rule conditions ---

// The provider reads condition { path_pattern { values } } from the typed
// PathPatternConfig block, not from the flat Values list, so a rule whose
// response carried only Values read back with an empty path_pattern and drifted.
func TestCreateRule_ReportsTypedConditionConfig(t *testing.T) {
	s := newSvc()
	lbARN := createLB(t, s, "lb")
	tgw := elbReq(t, s, "CreateTargetGroup", map[string]string{
		"Name": "tg", "Protocol": "HTTP", "Port": "80",
	})
	tgARN := between(tgw.Body.String(), "<TargetGroupArn>", "</TargetGroupArn>")
	lw := elbReq(t, s, "CreateListener", map[string]string{
		"LoadBalancerArn":                        lbARN,
		"Protocol":                               "HTTP",
		"Port":                                   "80",
		"DefaultActions.member.1.Type":           "forward",
		"DefaultActions.member.1.TargetGroupArn": tgARN,
	})
	listenerARN := between(lw.Body.String(), "<ListenerArn>", "</ListenerArn>")

	w := elbReq(t, s, "CreateRule", map[string]string{
		"ListenerArn":               listenerARN,
		"Priority":                  "10",
		"Conditions.member.1.Field": "path-pattern",
		"Conditions.member.1.PathPatternConfig.Values.member.1": "/api/*",
		"Actions.member.1.Type":                                 "forward",
		"Actions.member.1.TargetGroupArn":                       tgARN,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "<PathPatternConfig><Values><member>/api/*</member></Values></PathPatternConfig>") {
		t.Errorf("rule response missing the typed PathPatternConfig block:\n%s", body)
	}
	// The flat list stays for older clients.
	if !strings.Contains(body, "<Field>path-pattern</Field><Values><member>/api/*</member></Values>") {
		t.Errorf("rule response should keep the flat Values list:\n%s", body)
	}

	// DescribeRules is the provider's read path.
	w = elbReq(t, s, "DescribeRules", map[string]string{"ListenerArn": listenerARN})
	if !strings.Contains(w.Body.String(), "<PathPatternConfig>") {
		t.Errorf("describe-rules missing the typed config:\n%s", w.Body.String())
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
