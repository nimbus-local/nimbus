package rds

// RDS Proxy emulation.
//
// A proxy sits between an application and a DB cluster, so the local endpoint
// resolves to the same Postgres sidecar a cluster endpoint does — code pointed
// at the proxy connects to a real database rather than a name that goes nowhere.
// Nimbus does no connection pooling of its own: the pool settings round-trip so
// Terraform sees no drift, and the proxy's CloudWatch series (ClientConnections,
// DatabaseConnections, and friends) are seeded through PutMetricData like any
// other metric.
//
// Every proxy gets a "default" target group at creation, as real RDS does —
// Terraform's aws_db_proxy_default_target_group modifies that group rather than
// creating one, so it has to exist the moment the proxy does.
//
// Response shapes follow the RDS query protocol as published in botocore's
// service model: the proxy lists (DBProxies, Auth, VpcSubnetIds, TargetGroups,
// Targets) all use the default `member` element name, unlike DBInstances and
// DBSubnetGroups which name their members.

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nimbus-local/nimbus/internal/uid"
)

// Defaults real RDS applies to a proxy's default target group.
const (
	defaultMaxConnectionsPercent     = 100
	defaultMaxIdleConnectionsPercent = 50
	defaultConnectionBorrowTimeout   = 120
	defaultIdleClientTimeout         = 1800
)

type dbProxy struct {
	name              string
	arn               string
	engineFamily      string
	roleArn           string
	subnetIDs         []string
	securityGroupIDs  []string
	auth              []proxyAuth
	defaultAuthScheme string
	endpoint          string
	requireTLS        bool
	idleClientTimeout int
	debugLogging      bool
	createdAt         time.Time
	updatedAt         time.Time
	targetGroups      map[string]*dbProxyTargetGroup // TargetGroupName -> group
}

// proxyAuth is one entry of a proxy's Auth block. Nimbus does not read the
// secret or assume the role — the values are stored so they read back.
type proxyAuth struct {
	description            string
	userName               string
	authScheme             string
	secretArn              string
	iamAuth                string
	clientPasswordAuthType string
}

type dbProxyTargetGroup struct {
	name                  string
	arn                   string
	proxyName             string
	isDefault             bool
	status                string
	maxConnectionsPct     int
	maxIdleConnectionsPct int
	connectionBorrowTimeo int
	sessionPinningFilters []string
	initQuery             string
	createdAt             time.Time
	updatedAt             time.Time
	targets               []*dbProxyTarget
}

// dbProxyTarget is a registered cluster or instance. Registering a cluster
// yields one TRACKED_CLUSTER target; real RDS additionally lists the cluster's
// member instances as RDS_INSTANCE targets, which Nimbus does not synthesize —
// nothing reading a local proxy needs the expansion, and inventing entries would
// confuse Terraform's read-back of aws_db_proxy_target.
type dbProxyTarget struct {
	arn              string
	endpoint         string
	port             int
	rdsResourceID    string // instance or cluster identifier, as registered
	trackedClusterID string // set for cluster targets only
	targetType       string // RDS_INSTANCE | TRACKED_CLUSTER
	role             string
}

// ── Proxies ───────────────────────────────────────────────────────────────────

func (s *Service) createDBProxy(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("DBProxyName")
	if name == "" {
		rdsError(w, http.StatusBadRequest, "InvalidParameterValue", "DBProxyName is required")
		return
	}
	engineFamily := r.FormValue("EngineFamily")
	if engineFamily == "" {
		rdsError(w, http.StatusBadRequest, "InvalidParameterValue", "EngineFamily is required")
		return
	}

	now := time.Now().UTC()
	p := &dbProxy{
		name:              name,
		arn:               s.proxyARN(),
		engineFamily:      engineFamily,
		roleArn:           r.FormValue("RoleArn"),
		subnetIDs:         formList(r, "VpcSubnetIds"),
		securityGroupIDs:  formList(r, "VpcSecurityGroupIds"),
		auth:              parseProxyAuth(r),
		defaultAuthScheme: r.FormValue("DefaultAuthScheme"),
		// Point at the Postgres sidecar, exactly as a cluster endpoint does, so
		// an application configured against the proxy reaches a real database.
		endpoint:          s.postgresHost,
		requireTLS:        r.FormValue("RequireTLS") == "true",
		idleClientTimeout: formInt(r, "IdleClientTimeout", defaultIdleClientTimeout),
		debugLogging:      r.FormValue("DebugLogging") == "true",
		createdAt:         now,
		updatedAt:         now,
		targetGroups:      map[string]*dbProxyTargetGroup{},
	}
	// Real RDS creates the default target group with the proxy.
	p.targetGroups["default"] = &dbProxyTargetGroup{
		name:                  "default",
		arn:                   s.proxyTargetGroupARN(),
		proxyName:             name,
		isDefault:             true,
		status:                "available",
		maxConnectionsPct:     defaultMaxConnectionsPercent,
		maxIdleConnectionsPct: defaultMaxIdleConnectionsPercent,
		connectionBorrowTimeo: defaultConnectionBorrowTimeout,
		createdAt:             now,
		updatedAt:             now,
	}

	s.mu.Lock()
	if _, exists := s.proxies[name]; exists {
		s.mu.Unlock()
		rdsError(w, http.StatusBadRequest, "DBProxyAlreadyExistsFault",
			fmt.Sprintf("DB Proxy %s already exists.", name))
		return
	}
	s.proxies[name] = p
	s.storeTags(r, p.arn)
	body := s.proxyXML(p)
	s.mu.Unlock()

	writeXML(w, http.StatusOK, wrap("CreateDBProxy", fmt.Sprintf(`
    <CreateDBProxyResult>
      <DBProxy>%s</DBProxy>
    </CreateDBProxyResult>`, body)))
}

func (s *Service) describeDBProxies(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("DBProxyName")

	s.mu.RLock()
	defer s.mu.RUnlock()

	var items []string
	for _, p := range s.proxies {
		if name != "" && p.name != name {
			continue
		}
		items = append(items, "<member>"+s.proxyXML(p)+"</member>")
	}
	if name != "" && len(items) == 0 {
		rdsError(w, http.StatusNotFound, "DBProxyNotFoundFault",
			fmt.Sprintf("DBProxy %s not found.", name))
		return
	}
	writeXML(w, http.StatusOK, wrap("DescribeDBProxies", fmt.Sprintf(`
    <DescribeDBProxiesResult>
      <DBProxies>%s</DBProxies>
    </DescribeDBProxiesResult>`, strings.Join(items, ""))))
}

// modifyDBProxy updates the mutable proxy attributes. Terraform reaches for it
// whenever an aws_db_proxy attribute changes, so without it a second apply over
// an edited fixture fails with InvalidAction.
func (s *Service) modifyDBProxy(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("DBProxyName")

	s.mu.Lock()
	p, ok := s.proxies[name]
	if !ok {
		s.mu.Unlock()
		rdsError(w, http.StatusNotFound, "DBProxyNotFoundFault",
			fmt.Sprintf("DBProxy %s not found.", name))
		return
	}
	if v := r.FormValue("NewDBProxyName"); v != "" && v != p.name {
		delete(s.proxies, p.name)
		p.name = v
		for _, tg := range p.targetGroups {
			tg.proxyName = v
		}
		s.proxies[v] = p
	}
	if v := r.FormValue("RoleArn"); v != "" {
		p.roleArn = v
	}
	if v := r.FormValue("RequireTLS"); v != "" {
		p.requireTLS = v == "true"
	}
	if v := r.FormValue("DebugLogging"); v != "" {
		p.debugLogging = v == "true"
	}
	if v := r.FormValue("IdleClientTimeout"); v != "" {
		p.idleClientTimeout = formInt(r, "IdleClientTimeout", p.idleClientTimeout)
	}
	if auth := parseProxyAuth(r); len(auth) > 0 {
		p.auth = auth
	}
	if sgs := formList(r, "SecurityGroups"); len(sgs) > 0 {
		p.securityGroupIDs = sgs
	}
	p.updatedAt = time.Now().UTC()
	body := s.proxyXML(p)
	s.mu.Unlock()

	writeXML(w, http.StatusOK, wrap("ModifyDBProxy", fmt.Sprintf(`
    <ModifyDBProxyResult>
      <DBProxy>%s</DBProxy>
    </ModifyDBProxyResult>`, body)))
}

func (s *Service) deleteDBProxy(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("DBProxyName")

	s.mu.Lock()
	p, ok := s.proxies[name]
	var body string
	if ok {
		delete(s.proxies, name)
		delete(s.tags, p.arn)
		// Report the proxy as deleting, the state real RDS returns here.
		p.updatedAt = time.Now().UTC()
		body = s.proxyStatusXML(p, "deleting")
	}
	s.mu.Unlock()

	if !ok {
		rdsError(w, http.StatusNotFound, "DBProxyNotFoundFault",
			fmt.Sprintf("DBProxy %s not found.", name))
		return
	}
	writeXML(w, http.StatusOK, wrap("DeleteDBProxy", fmt.Sprintf(`
    <DeleteDBProxyResult>
      <DBProxy>%s</DBProxy>
    </DeleteDBProxyResult>`, body)))
}

// ── Target groups ─────────────────────────────────────────────────────────────

func (s *Service) describeDBProxyTargetGroups(w http.ResponseWriter, r *http.Request) {
	proxyName := r.FormValue("DBProxyName")
	tgName := r.FormValue("TargetGroupName")

	s.mu.RLock()
	defer s.mu.RUnlock()

	p, ok := s.proxies[proxyName]
	if !ok {
		rdsError(w, http.StatusNotFound, "DBProxyNotFoundFault",
			fmt.Sprintf("DBProxy %s not found.", proxyName))
		return
	}

	var items []string
	for _, tg := range p.targetGroups {
		if tgName != "" && tg.name != tgName {
			continue
		}
		items = append(items, "<member>"+proxyTargetGroupXML(tg)+"</member>")
	}
	if tgName != "" && len(items) == 0 {
		rdsError(w, http.StatusNotFound, "DBProxyTargetGroupNotFoundFault",
			fmt.Sprintf("Target group %s not found for DBProxy %s.", tgName, proxyName))
		return
	}
	writeXML(w, http.StatusOK, wrap("DescribeDBProxyTargetGroups", fmt.Sprintf(`
    <DescribeDBProxyTargetGroupsResult>
      <TargetGroups>%s</TargetGroups>
    </DescribeDBProxyTargetGroupsResult>`, strings.Join(items, ""))))
}

func (s *Service) modifyDBProxyTargetGroup(w http.ResponseWriter, r *http.Request) {
	proxyName := r.FormValue("DBProxyName")
	tgName := r.FormValue("TargetGroupName")

	s.mu.Lock()
	tg, errCode, errMsg := s.lookupTargetGroup(proxyName, tgName)
	if tg == nil {
		s.mu.Unlock()
		rdsError(w, http.StatusNotFound, errCode, errMsg)
		return
	}
	// Only the fields present in the form are changed: the provider sends the
	// pool block it manages and nothing else.
	if v := r.FormValue("ConnectionPoolConfig.MaxConnectionsPercent"); v != "" {
		tg.maxConnectionsPct = formInt(r, "ConnectionPoolConfig.MaxConnectionsPercent", tg.maxConnectionsPct)
	}
	if v := r.FormValue("ConnectionPoolConfig.MaxIdleConnectionsPercent"); v != "" {
		tg.maxIdleConnectionsPct = formInt(r, "ConnectionPoolConfig.MaxIdleConnectionsPercent", tg.maxIdleConnectionsPct)
	}
	if v := r.FormValue("ConnectionPoolConfig.ConnectionBorrowTimeout"); v != "" {
		tg.connectionBorrowTimeo = formInt(r, "ConnectionPoolConfig.ConnectionBorrowTimeout", tg.connectionBorrowTimeo)
	}
	if filters := formList(r, "ConnectionPoolConfig.SessionPinningFilters"); len(filters) > 0 {
		tg.sessionPinningFilters = filters
	}
	if v := r.FormValue("ConnectionPoolConfig.InitQuery"); v != "" {
		tg.initQuery = v
	}
	if v := r.FormValue("NewName"); v != "" && v != tg.name {
		p := s.proxies[tg.proxyName]
		delete(p.targetGroups, tg.name)
		tg.name = v
		p.targetGroups[v] = tg
	}
	tg.updatedAt = time.Now().UTC()
	body := proxyTargetGroupXML(tg)
	s.mu.Unlock()

	writeXML(w, http.StatusOK, wrap("ModifyDBProxyTargetGroup", fmt.Sprintf(`
    <ModifyDBProxyTargetGroupResult>
      <DBProxyTargetGroup>%s</DBProxyTargetGroup>
    </ModifyDBProxyTargetGroupResult>`, body)))
}

// ── Targets ───────────────────────────────────────────────────────────────────

func (s *Service) registerDBProxyTargets(w http.ResponseWriter, r *http.Request) {
	proxyName := r.FormValue("DBProxyName")
	tgName := r.FormValue("TargetGroupName")

	s.mu.Lock()
	tg, errCode, errMsg := s.lookupTargetGroup(proxyName, tgName)
	if tg == nil {
		s.mu.Unlock()
		rdsError(w, http.StatusNotFound, errCode, errMsg)
		return
	}

	var added []*dbProxyTarget
	for _, id := range formList(r, "DBInstanceIdentifiers") {
		inst, ok := s.instances[id]
		if !ok {
			s.mu.Unlock()
			rdsError(w, http.StatusNotFound, "DBInstanceNotFoundFault",
				fmt.Sprintf("DBInstance %s not found.", id))
			return
		}
		added = append(added, &dbProxyTarget{
			arn:           inst.arn,
			endpoint:      inst.endpoint,
			port:          inst.port,
			rdsResourceID: inst.identifier,
			targetType:    "RDS_INSTANCE",
			role:          "READ_WRITE",
		})
	}
	for _, id := range formList(r, "DBClusterIdentifiers") {
		c, ok := s.clusters[id]
		if !ok {
			s.mu.Unlock()
			rdsError(w, http.StatusNotFound, "DBClusterNotFoundFault",
				fmt.Sprintf("DBCluster %s not found.", id))
			return
		}
		added = append(added, &dbProxyTarget{
			arn:              c.arn,
			endpoint:         c.endpoint,
			port:             c.port,
			rdsResourceID:    c.identifier,
			trackedClusterID: c.identifier,
			targetType:       "TRACKED_CLUSTER",
			role:             "READ_WRITE",
		})
	}

	// Re-registering the same target replaces it rather than duplicating.
	for _, t := range added {
		tg.targets = removeTarget(tg.targets, t.rdsResourceID)
		tg.targets = append(tg.targets, t)
	}
	tg.updatedAt = time.Now().UTC()

	var items []string
	for _, t := range added {
		items = append(items, "<member>"+proxyTargetXML(t)+"</member>")
	}
	s.mu.Unlock()

	writeXML(w, http.StatusOK, wrap("RegisterDBProxyTargets", fmt.Sprintf(`
    <RegisterDBProxyTargetsResult>
      <DBProxyTargets>%s</DBProxyTargets>
    </RegisterDBProxyTargetsResult>`, strings.Join(items, ""))))
}

// deregisterDBProxyTargets is what Terraform calls when destroying an
// aws_db_proxy_target, so the fixture cannot be torn down without it.
func (s *Service) deregisterDBProxyTargets(w http.ResponseWriter, r *http.Request) {
	proxyName := r.FormValue("DBProxyName")
	tgName := r.FormValue("TargetGroupName")

	s.mu.Lock()
	tg, errCode, errMsg := s.lookupTargetGroup(proxyName, tgName)
	if tg == nil {
		s.mu.Unlock()
		rdsError(w, http.StatusNotFound, errCode, errMsg)
		return
	}
	ids := append(formList(r, "DBInstanceIdentifiers"), formList(r, "DBClusterIdentifiers")...)
	for _, id := range ids {
		tg.targets = removeTarget(tg.targets, id)
	}
	tg.updatedAt = time.Now().UTC()
	s.mu.Unlock()

	// The result element must be present even though the shape carries no
	// members: the operation declares a resultWrapper, and the SDK's query
	// parser fails with a KeyError when it is missing. Contrast
	// AddTagsToResource, which declares no output shape at all and so needs none.
	writeXML(w, http.StatusOK, wrap("DeregisterDBProxyTargets", `
    <DeregisterDBProxyTargetsResult/>`))
}

func (s *Service) describeDBProxyTargets(w http.ResponseWriter, r *http.Request) {
	proxyName := r.FormValue("DBProxyName")
	tgName := r.FormValue("TargetGroupName")

	s.mu.RLock()
	defer s.mu.RUnlock()

	tg, errCode, errMsg := s.lookupTargetGroup(proxyName, tgName)
	if tg == nil {
		rdsError(w, http.StatusNotFound, errCode, errMsg)
		return
	}

	items := make([]string, 0, len(tg.targets))
	for _, t := range tg.targets {
		items = append(items, "<member>"+proxyTargetXML(t)+"</member>")
	}
	writeXML(w, http.StatusOK, wrap("DescribeDBProxyTargets", fmt.Sprintf(`
    <DescribeDBProxyTargetsResult>
      <Targets>%s</Targets>
    </DescribeDBProxyTargetsResult>`, strings.Join(items, ""))))
}

// ── Lookup helpers ────────────────────────────────────────────────────────────

// lookupTargetGroup resolves a proxy's target group, defaulting to "default"
// when the caller named none. On failure it returns the RDS fault code and
// message to report. Caller must hold s.mu.
func (s *Service) lookupTargetGroup(proxyName, tgName string) (*dbProxyTargetGroup, string, string) {
	p, ok := s.proxies[proxyName]
	if !ok {
		return nil, "DBProxyNotFoundFault", fmt.Sprintf("DBProxy %s not found.", proxyName)
	}
	if tgName == "" {
		tgName = "default"
	}
	tg, ok := p.targetGroups[tgName]
	if !ok {
		return nil, "DBProxyTargetGroupNotFoundFault",
			fmt.Sprintf("Target group %s not found for DBProxy %s.", tgName, proxyName)
	}
	return tg, "", ""
}

// removeTarget drops the target registered for an identifier, if present.
func removeTarget(targets []*dbProxyTarget, rdsResourceID string) []*dbProxyTarget {
	out := targets[:0]
	for _, t := range targets {
		if t.rdsResourceID != rdsResourceID {
			out = append(out, t)
		}
	}
	return out
}

// ProxyCount reports how many proxies exist, for the inspection endpoint.
func (s *Service) ProxyCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.proxies)
}

// ── XML serialisation ─────────────────────────────────────────────────────────

func (s *Service) proxyXML(p *dbProxy) string {
	return s.proxyStatusXML(p, "available")
}

func (s *Service) proxyStatusXML(p *dbProxy, status string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `
        <DBProxyName>%s</DBProxyName>
        <DBProxyArn>%s</DBProxyArn>
        <Status>%s</Status>
        <EngineFamily>%s</EngineFamily>
        <VpcId>%s</VpcId>
        <RoleArn>%s</RoleArn>
        <Endpoint>%s</Endpoint>
        <RequireTLS>%t</RequireTLS>
        <IdleClientTimeout>%d</IdleClientTimeout>
        <DebugLogging>%t</DebugLogging>
        <CreatedDate>%s</CreatedDate>
        <UpdatedDate>%s</UpdatedDate>`,
		p.name, p.arn, status, p.engineFamily, s.proxyVpcID(p), p.roleArn, p.endpoint,
		p.requireTLS, p.idleClientTimeout, p.debugLogging,
		p.createdAt.Format(time.RFC3339), p.updatedAt.Format(time.RFC3339))

	if p.defaultAuthScheme != "" {
		fmt.Fprintf(&b, `
        <DefaultAuthScheme>%s</DefaultAuthScheme>`, p.defaultAuthScheme)
	}
	b.WriteString(memberList("VpcSubnetIds", p.subnetIDs))
	b.WriteString(memberList("VpcSecurityGroupIds", p.securityGroupIDs))

	b.WriteString(`
        <Auth>`)
	for _, a := range p.auth {
		b.WriteString(`
          <member>`)
		for _, f := range []struct{ tag, val string }{
			{"Description", a.description},
			{"UserName", a.userName},
			{"AuthScheme", a.authScheme},
			{"SecretArn", a.secretArn},
			{"IAMAuth", a.iamAuth},
			{"ClientPasswordAuthType", a.clientPasswordAuthType},
		} {
			if f.val != "" {
				fmt.Fprintf(&b, `
            <%s>%s</%s>`, f.tag, f.val, f.tag)
			}
		}
		b.WriteString(`
          </member>`)
	}
	b.WriteString(`
        </Auth>`)
	return b.String()
}

// proxyVpcID resolves the proxy's VPC from its subnets, mirroring how a DB
// subnet group reports one. Caller must hold s.mu.
func (s *Service) proxyVpcID(p *dbProxy) string {
	vpcID := "vpc-00000000000000001"
	if s.subnetInfo != nil {
		for _, id := range p.subnetIDs {
			if v, _, ok := s.subnetInfo(id); ok {
				return v
			}
		}
	}
	return vpcID
}

func proxyTargetGroupXML(tg *dbProxyTargetGroup) string {
	var b strings.Builder
	fmt.Fprintf(&b, `
        <DBProxyName>%s</DBProxyName>
        <TargetGroupName>%s</TargetGroupName>
        <TargetGroupArn>%s</TargetGroupArn>
        <IsDefault>%t</IsDefault>
        <Status>%s</Status>
        <CreatedDate>%s</CreatedDate>
        <UpdatedDate>%s</UpdatedDate>
        <ConnectionPoolConfig>
          <MaxConnectionsPercent>%d</MaxConnectionsPercent>
          <MaxIdleConnectionsPercent>%d</MaxIdleConnectionsPercent>
          <ConnectionBorrowTimeout>%d</ConnectionBorrowTimeout>`,
		tg.proxyName, tg.name, tg.arn, tg.isDefault, tg.status,
		tg.createdAt.Format(time.RFC3339), tg.updatedAt.Format(time.RFC3339),
		tg.maxConnectionsPct, tg.maxIdleConnectionsPct, tg.connectionBorrowTimeo)
	if tg.initQuery != "" {
		fmt.Fprintf(&b, `
          <InitQuery>%s</InitQuery>`, tg.initQuery)
	}
	b.WriteString(memberList("SessionPinningFilters", tg.sessionPinningFilters))
	b.WriteString(`
        </ConnectionPoolConfig>`)
	return b.String()
}

func proxyTargetXML(t *dbProxyTarget) string {
	var b strings.Builder
	fmt.Fprintf(&b, `
        <TargetArn>%s</TargetArn>
        <Endpoint>%s</Endpoint>
        <RdsResourceId>%s</RdsResourceId>
        <Port>%d</Port>
        <Type>%s</Type>
        <Role>%s</Role>
        <TargetHealth>
          <State>AVAILABLE</State>
        </TargetHealth>`,
		t.arn, t.endpoint, t.rdsResourceID, t.port, t.targetType, t.role)
	if t.trackedClusterID != "" {
		fmt.Fprintf(&b, `
        <TrackedClusterId>%s</TrackedClusterId>`, t.trackedClusterID)
	}
	return b.String()
}

// memberList renders a query-protocol string list. An empty list still emits the
// wrapper element, which is how RDS reports "none".
func memberList(tag string, values []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `
        <%s>`, tag)
	for _, v := range values {
		fmt.Fprintf(&b, `
          <member>%s</member>`, v)
	}
	fmt.Fprintf(&b, `
        </%s>`, tag)
	return b.String()
}

// ── Request parsing ───────────────────────────────────────────────────────────

// parseProxyAuth reads the Auth.member.N.* block of a Create/ModifyDBProxy form.
func parseProxyAuth(r *http.Request) []proxyAuth {
	var out []proxyAuth
	for i := 1; ; i++ {
		prefix := fmt.Sprintf("Auth.member.%d.", i)
		a := proxyAuth{
			description:            r.FormValue(prefix + "Description"),
			userName:               r.FormValue(prefix + "UserName"),
			authScheme:             r.FormValue(prefix + "AuthScheme"),
			secretArn:              r.FormValue(prefix + "SecretArn"),
			iamAuth:                r.FormValue(prefix + "IAMAuth"),
			clientPasswordAuthType: r.FormValue(prefix + "ClientPasswordAuthType"),
		}
		if a == (proxyAuth{}) {
			break
		}
		out = append(out, a)
	}
	return out
}

// formList reads a query-protocol string list, accepting the standard
// `<prefix>.member.N` form as well as the bare `<prefix>.N` some callers send.
func formList(r *http.Request, prefix string) []string {
	var out []string
	for _, member := range []string{".member.", "."} {
		for i := 1; ; i++ {
			v := r.FormValue(fmt.Sprintf("%s%s%d", prefix, member, i))
			if v == "" {
				break
			}
			out = append(out, v)
		}
		if len(out) > 0 {
			break
		}
	}
	return out
}

// formInt reads an integer form field, returning fallback when absent or unparsable.
func formInt(r *http.Request, key string, fallback int) int {
	v := r.FormValue(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

// ── ARNs ──────────────────────────────────────────────────────────────────────

// proxyARN builds a proxy ARN. Real RDS keys it on a generated prx- identifier
// rather than the proxy name, and Terraform stores the ARN verbatim.
func (s *Service) proxyARN() string {
	return fmt.Sprintf("arn:aws:rds:%s:%s:db-proxy:%s", s.region, accountID, newProxyID("prx"))
}

func (s *Service) proxyTargetGroupARN() string {
	return fmt.Sprintf("arn:aws:rds:%s:%s:target-group:%s", s.region, accountID, newProxyID("prx-tg"))
}

// newProxyID generates an AWS-style proxy identifier: prx- followed by 17 hex
// characters.
func newProxyID(prefix string) string {
	hex := strings.ReplaceAll(uid.New(), "-", "")
	return prefix + "-" + hex[:17]
}
