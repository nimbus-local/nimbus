// Package rds emulates the AWS RDS/Aurora control plane.
// All state is in-memory. DB clusters resolve to a real Postgres sidecar
// running alongside Nimbus. Parameter groups are accepted and stored verbatim.
// Subnet groups record the subnets they were created with: clusters and
// instances that name a subnet group hold a reference to it, which the EC2
// service consults before deleting a subnet.
package rds

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nimbus-local/nimbus/internal/uid"
)

const (
	accountID = "000000000000"
	rdsNS     = "http://rds.amazonaws.com/doc/2014-10-31/"
)

// SubnetInfoFunc resolves an EC2 subnet ID to its VPC ID and Availability
// Zone. ok is false when the subnet is unknown to the EC2 service — e.g. a
// hardcoded ID that was never created through CreateSubnet.
type SubnetInfoFunc func(subnetID string) (vpcID, az string, ok bool)

// Service implements the AWS RDS/Aurora control plane.
type Service struct {
	mu               sync.RWMutex
	subnetGroups     map[string]*dbSubnetGroup
	clusterParamGrps map[string]*dbParamGroup
	paramGrps        map[string]*dbParamGroup
	clusters         map[string]*dbCluster
	instances        map[string]*dbInstance
	tags             map[string]map[string]string // arn -> tags
	region           string
	postgresHost     string
	postgresPort     int
	subnetInfo       SubnetInfoFunc
}

type dbSubnetGroup struct {
	name        string
	description string
	arn         string
	subnetIDs   []string
}

type dbParamGroup struct {
	name   string
	arn    string
	family string
	desc   string
}

type dbCluster struct {
	identifier    string
	arn           string
	resourceID    string // DbClusterResourceId, e.g. cluster-ABC...
	engine        string
	engineVersion string
	dbName        string
	masterUser    string
	endpoint      string
	port          int
	status        string
	subnetGroup   string // DBSubnetGroupName, empty when none was supplied
	createdAt     time.Time

	perfInsights          bool
	perfInsightsKMS       string
	perfInsightsRetention int
}

type dbInstance struct {
	identifier    string
	arn           string
	resourceID    string // DbiResourceId, e.g. db-ABC...
	clusterID     string
	engine        string
	engineVersion string
	class         string
	masterUser    string
	dbName        string
	storageGB     string // AllocatedStorage, empty for cluster members
	endpoint      string
	port          int
	status        string
	subnetGroup   string // DBSubnetGroupName, inherited from the cluster for members
	createdAt     time.Time

	perfInsights          bool
	perfInsightsKMS       string
	perfInsightsRetention int
}

// New creates an RDS service. postgresHost:postgresPort point at the Postgres
// sidecar that cluster endpoints will resolve to.
func New(region, postgresHost string, postgresPort int) *Service {
	if region == "" {
		region = "us-east-1"
	}
	if postgresHost == "" {
		postgresHost = "localhost"
	}
	if postgresPort == 0 {
		postgresPort = 5432
	}
	return &Service{
		region:           region,
		postgresHost:     postgresHost,
		postgresPort:     postgresPort,
		subnetGroups:     map[string]*dbSubnetGroup{},
		clusterParamGrps: map[string]*dbParamGroup{},
		paramGrps:        map[string]*dbParamGroup{},
		clusters:         map[string]*dbCluster{},
		instances:        map[string]*dbInstance{},
		tags:             map[string]map[string]string{},
	}
}

// SetSubnetInfo wires in the EC2 subnet lookup used to report the VPC and
// Availability Zone of a DB subnet group's subnets. Call it during startup,
// before the service begins serving requests.
func (s *Service) SetSubnetInfo(fn SubnetInfoFunc) { s.subnetInfo = fn }

func (s *Service) Name() string { return "rds" }

// Reset clears all in-memory state.
func (s *Service) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subnetGroups = map[string]*dbSubnetGroup{}
	s.clusterParamGrps = map[string]*dbParamGroup{}
	s.paramGrps = map[string]*dbParamGroup{}
	s.clusters = map[string]*dbCluster{}
	s.instances = map[string]*dbInstance{}
	s.tags = map[string]map[string]string{}
}

func (s *Service) Detect(r *http.Request) bool {
	if !strings.Contains(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		return false
	}
	_ = r.ParseForm()
	return r.FormValue("Version") == "2014-10-31"
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		rdsError(w, http.StatusBadRequest, "InvalidParameterValue", "cannot parse request body")
		return
	}
	switch r.FormValue("Action") {
	// Subnet groups
	case "CreateDBSubnetGroup":
		s.createDBSubnetGroup(w, r)
	case "DescribeDBSubnetGroups":
		s.describeDBSubnetGroups(w, r)
	case "DeleteDBSubnetGroup":
		s.deleteDBSubnetGroup(w, r)
	case "ModifyDBSubnetGroup":
		s.modifyDBSubnetGroup(w, r)
	// Cluster parameter groups
	case "CreateDBClusterParameterGroup":
		s.createDBClusterParameterGroup(w, r)
	case "DescribeDBClusterParameterGroups":
		s.describeDBClusterParameterGroups(w, r)
	case "DescribeDBClusterParameters":
		s.describeDBClusterParameters(w, r)
	case "ModifyDBClusterParameterGroup":
		s.modifyDBClusterParameterGroup(w, r)
	case "DeleteDBClusterParameterGroup":
		s.deleteDBClusterParameterGroup(w, r)
	// Instance parameter groups
	case "CreateDBParameterGroup":
		s.createDBParameterGroup(w, r)
	case "DescribeDBParameterGroups":
		s.describeDBParameterGroups(w, r)
	case "DescribeDBParameters":
		s.describeDBParameters(w, r)
	case "ModifyDBParameterGroup":
		s.modifyDBParameterGroup(w, r)
	case "DeleteDBParameterGroup":
		s.deleteDBParameterGroup(w, r)
	// Clusters
	case "CreateDBCluster":
		s.createDBCluster(w, r)
	case "DescribeDBClusters":
		s.describeDBClusters(w, r)
	case "ModifyDBCluster":
		s.modifyDBCluster(w, r)
	case "DeleteDBCluster":
		s.deleteDBCluster(w, r)
	// Instances
	case "CreateDBInstance":
		s.createDBInstance(w, r)
	case "DescribeDBInstances":
		s.describeDBInstances(w, r)
	case "ModifyDBInstance":
		s.modifyDBInstance(w, r)
	case "DeleteDBInstance":
		s.deleteDBInstance(w, r)
	// Engine / option queries — accept and return minimal responses
	case "DescribeDBEngineVersions":
		s.describeDBEngineVersions(w, r)
	case "DescribeOrderableDBInstanceOptions":
		s.describeOrderableDBInstanceOptions(w, r)
	case "DescribeDBClusterSnapshots":
		writeXML(w, http.StatusOK, wrap("DescribeDBClusterSnapshots", `
    <DescribeDBClusterSnapshotsResult><DBClusterSnapshots/></DescribeDBClusterSnapshotsResult>`))
	case "DescribeOptionGroups":
		writeXML(w, http.StatusOK, wrap("DescribeOptionGroups", `
    <DescribeOptionGroupsResult><OptionGroupsList/></DescribeOptionGroupsResult>`))
	// Tags
	case "AddTagsToResource":
		s.addTagsToResource(w, r)
	case "ListTagsForResource":
		s.listTagsForResource(w, r)
	case "RemoveTagsFromResource":
		s.removeTagsFromResource(w, r)
	default:
		rdsError(w, http.StatusBadRequest, "InvalidAction",
			fmt.Sprintf("Action %s is not supported.", r.FormValue("Action")))
	}
}

// ── Inspection endpoint ───────────────────────────────────────────────────────

// ClustersHandler serves /_nimbus/rds/clusters.
func (s *Service) ClustersHandler(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, "[")
	i := 0
	for _, c := range s.clusters {
		if i > 0 {
			fmt.Fprint(w, ",")
		}
		fmt.Fprintf(w, `{"identifier":%q,"endpoint":%q,"port":%d,"status":%q}`,
			c.identifier, c.endpoint, c.port, c.status)
		i++
	}
	fmt.Fprint(w, "]")
}

// ── Subnet groups ─────────────────────────────────────────────────────────────

func (s *Service) createDBSubnetGroup(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("DBSubnetGroupName")
	desc := r.FormValue("DBSubnetGroupDescription")
	arn := s.subnetGroupARN(name)

	sg := &dbSubnetGroup{name: name, description: desc, arn: arn, subnetIDs: parseSubnetIDs(r)}

	s.mu.Lock()
	s.subnetGroups[name] = sg
	s.storeTags(r, arn)
	body := s.subnetGroupXML(sg)
	s.mu.Unlock()

	writeXML(w, http.StatusOK, wrap("CreateDBSubnetGroup", fmt.Sprintf(`
    <CreateDBSubnetGroupResult>
      <DBSubnetGroup>%s</DBSubnetGroup>
    </CreateDBSubnetGroupResult>`, body)))
}

// parseSubnetIDs reads the SubnetIds list from a CreateDBSubnetGroup or
// ModifyDBSubnetGroup form. The query protocol names the list member
// `SubnetIdentifier`; `member` is accepted too since some SDK versions emit it.
func parseSubnetIDs(r *http.Request) []string {
	var ids []string
	for _, member := range []string{"SubnetIdentifier", "member"} {
		for i := 1; ; i++ {
			id := r.FormValue(fmt.Sprintf("SubnetIds.%s.%d", member, i))
			if id == "" {
				break
			}
			ids = append(ids, id)
		}
		if len(ids) > 0 {
			break
		}
	}
	return ids
}

func (s *Service) describeDBSubnetGroups(w http.ResponseWriter, r *http.Request) {
	filter := r.FormValue("DBSubnetGroupName")
	s.mu.RLock()
	defer s.mu.RUnlock()

	var items []string
	for _, sg := range s.subnetGroups {
		if filter != "" && sg.name != filter {
			continue
		}
		items = append(items, "<DBSubnetGroup>"+s.subnetGroupXML(sg)+"</DBSubnetGroup>")
	}
	if filter != "" && len(items) == 0 {
		rdsError(w, http.StatusNotFound, "DBSubnetGroupNotFoundFault",
			fmt.Sprintf("DBSubnetGroup '%s' not found.", filter))
		return
	}
	writeXML(w, http.StatusOK, wrap("DescribeDBSubnetGroups", fmt.Sprintf(`
    <DescribeDBSubnetGroupsResult>
      <DBSubnetGroups>%s</DBSubnetGroups>
    </DescribeDBSubnetGroupsResult>`, strings.Join(items, ""))))
}

func (s *Service) deleteDBSubnetGroup(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("DBSubnetGroupName")
	s.mu.Lock()
	user, inUse := s.subnetGroupUser(name)
	if !inUse {
		delete(s.subnetGroups, name)
	}
	s.mu.Unlock()
	if inUse {
		rdsError(w, http.StatusBadRequest, "InvalidDBSubnetGroupStateFault",
			fmt.Sprintf("The DB subnet group '%s' is still in use by %s.", name, user))
		return
	}
	writeXML(w, http.StatusOK, wrap("DeleteDBSubnetGroup", ""))
}

func (s *Service) modifyDBSubnetGroup(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("DBSubnetGroupName")
	s.mu.Lock()
	sg := s.subnetGroups[name]
	if sg != nil {
		if desc := r.FormValue("DBSubnetGroupDescription"); desc != "" {
			sg.description = desc
		}
		// A subnet replace re-points the group; the new list is what later
		// subnet deletes are checked against.
		if ids := parseSubnetIDs(r); len(ids) > 0 {
			sg.subnetIDs = ids
		}
	}
	var body string
	if sg != nil {
		body = s.subnetGroupXML(sg)
	}
	s.mu.Unlock()
	if sg == nil {
		rdsError(w, http.StatusNotFound, "DBSubnetGroupNotFoundFault",
			fmt.Sprintf("DBSubnetGroup '%s' not found.", name))
		return
	}
	writeXML(w, http.StatusOK, wrap("ModifyDBSubnetGroup", fmt.Sprintf(`
    <ModifyDBSubnetGroupResult>
      <DBSubnetGroup>%s</DBSubnetGroup>
    </ModifyDBSubnetGroupResult>`, body)))
}

// subnetGroupXML renders a DBSubnetGroup structure. VPC and Availability Zone
// come from the EC2 service when the subnets were created there; subnets that
// EC2 doesn't know about fall back to placeholders. Must be called with s.mu
// held.
func (s *Service) subnetGroupXML(sg *dbSubnetGroup) string {
	vpcID := "vpc-00000000000000001"
	var subnets strings.Builder
	for _, id := range sg.subnetIDs {
		az := s.region + "a"
		if s.subnetInfo != nil {
			if v, a, ok := s.subnetInfo(id); ok {
				vpcID = v
				az = a
			}
		}
		fmt.Fprintf(&subnets, `
          <Subnet>
            <SubnetIdentifier>%s</SubnetIdentifier>
            <SubnetAvailabilityZone><Name>%s</Name></SubnetAvailabilityZone>
            <SubnetStatus>Active</SubnetStatus>
          </Subnet>`, id, az)
	}
	return fmt.Sprintf(`
        <DBSubnetGroupArn>%s</DBSubnetGroupArn>
        <DBSubnetGroupDescription>%s</DBSubnetGroupDescription>
        <DBSubnetGroupName>%s</DBSubnetGroupName>
        <SubnetGroupStatus>Complete</SubnetGroupStatus>
        <VpcId>%s</VpcId>
        <Subnets>%s</Subnets>`, sg.arn, sg.description, sg.name, vpcID, subnets.String())
}

// subnetGroupUser returns the identifier of a cluster or instance currently
// placed in the named subnet group. Must be called with s.mu held.
func (s *Service) subnetGroupUser(name string) (string, bool) {
	for _, c := range s.clusters {
		if c.subnetGroup == name {
			return "DB cluster " + c.identifier, true
		}
	}
	for _, inst := range s.instances {
		if inst.subnetGroup == name {
			return "DB instance " + inst.identifier, true
		}
	}
	return "", false
}

// SubnetInUse reports whether a DB cluster or instance currently sits in the
// given EC2 subnet by way of its DB subnet group, returning a description of
// the resource holding the reference. The EC2 service calls this before
// deleting a subnet, mirroring the DependencyViolation real AWS returns.
func (s *Service) SubnetInUse(subnetID string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, sg := range s.subnetGroups {
		if !containsAny(sg.subnetIDs, subnetID) {
			continue
		}
		if user, ok := s.subnetGroupUser(sg.name); ok {
			return fmt.Sprintf("%s (DB subnet group %s)", user, sg.name), true
		}
	}
	return "", false
}

func (s *Service) subnetGroupARN(name string) string {
	return fmt.Sprintf("arn:aws:rds:%s:%s:subgrp:%s", s.region, accountID, name)
}

// ── Cluster parameter groups ──────────────────────────────────────────────────

func (s *Service) createDBClusterParameterGroup(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("DBClusterParameterGroupName")
	family := r.FormValue("DBParameterGroupFamily")
	desc := r.FormValue("Description")
	arn := s.clusterParamGroupARN(name)

	s.mu.Lock()
	s.clusterParamGrps[name] = &dbParamGroup{name: name, arn: arn, family: family, desc: desc}
	s.storeTags(r, arn)
	s.mu.Unlock()

	writeXML(w, http.StatusOK, wrap("CreateDBClusterParameterGroup", fmt.Sprintf(`
    <CreateDBClusterParameterGroupResult>
      <DBClusterParameterGroup>%s</DBClusterParameterGroup>
    </CreateDBClusterParameterGroupResult>`, clusterParamGroupXML(name, arn, family, desc))))
}

func (s *Service) describeDBClusterParameterGroups(w http.ResponseWriter, r *http.Request) {
	filter := r.FormValue("DBClusterParameterGroupName")
	s.mu.RLock()
	defer s.mu.RUnlock()

	var items []string
	for _, pg := range s.clusterParamGrps {
		if filter != "" && pg.name != filter {
			continue
		}
		items = append(items, "<DBClusterParameterGroup>"+clusterParamGroupXML(pg.name, pg.arn, pg.family, pg.desc)+"</DBClusterParameterGroup>")
	}
	if filter != "" && len(items) == 0 {
		rdsError(w, http.StatusNotFound, "DBParameterGroupNotFound",
			fmt.Sprintf("DBClusterParameterGroup '%s' not found.", filter))
		return
	}
	writeXML(w, http.StatusOK, wrap("DescribeDBClusterParameterGroups", fmt.Sprintf(`
    <DescribeDBClusterParameterGroupsResult>
      <DBClusterParameterGroups>%s</DBClusterParameterGroups>
    </DescribeDBClusterParameterGroupsResult>`, strings.Join(items, ""))))
}

func (s *Service) describeDBClusterParameters(w http.ResponseWriter, _ *http.Request) {
	writeXML(w, http.StatusOK, wrap("DescribeDBClusterParameters", `
    <DescribeDBClusterParametersResult>
      <Parameters/>
    </DescribeDBClusterParametersResult>`))
}

func (s *Service) modifyDBClusterParameterGroup(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("DBClusterParameterGroupName")
	writeXML(w, http.StatusOK, wrap("ModifyDBClusterParameterGroup", fmt.Sprintf(`
    <ModifyDBClusterParameterGroupResult>
      <DBClusterParameterGroupName>%s</DBClusterParameterGroupName>
    </ModifyDBClusterParameterGroupResult>`, name)))
}

func (s *Service) deleteDBClusterParameterGroup(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("DBClusterParameterGroupName")
	s.mu.Lock()
	delete(s.clusterParamGrps, name)
	s.mu.Unlock()
	writeXML(w, http.StatusOK, wrap("DeleteDBClusterParameterGroup", ""))
}

func clusterParamGroupXML(name, arn, family, desc string) string {
	return fmt.Sprintf(`
        <DBClusterParameterGroupArn>%s</DBClusterParameterGroupArn>
        <DBClusterParameterGroupName>%s</DBClusterParameterGroupName>
        <DBParameterGroupFamily>%s</DBParameterGroupFamily>
        <Description>%s</Description>`, arn, name, family, desc)
}

func (s *Service) clusterParamGroupARN(name string) string {
	return fmt.Sprintf("arn:aws:rds:%s:%s:cluster-pg:%s", s.region, accountID, name)
}

// ── Instance parameter groups ─────────────────────────────────────────────────

func (s *Service) createDBParameterGroup(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("DBParameterGroupName")
	family := r.FormValue("DBParameterGroupFamily")
	desc := r.FormValue("Description")
	arn := s.paramGroupARN(name)

	s.mu.Lock()
	s.paramGrps[name] = &dbParamGroup{name: name, arn: arn, family: family, desc: desc}
	s.storeTags(r, arn)
	s.mu.Unlock()

	writeXML(w, http.StatusOK, wrap("CreateDBParameterGroup", fmt.Sprintf(`
    <CreateDBParameterGroupResult>
      <DBParameterGroup>%s</DBParameterGroup>
    </CreateDBParameterGroupResult>`, paramGroupXML(name, arn, family, desc))))
}

func (s *Service) describeDBParameterGroups(w http.ResponseWriter, r *http.Request) {
	filter := r.FormValue("DBParameterGroupName")
	s.mu.RLock()
	defer s.mu.RUnlock()

	var items []string
	for _, pg := range s.paramGrps {
		if filter != "" && pg.name != filter {
			continue
		}
		items = append(items, "<DBParameterGroup>"+paramGroupXML(pg.name, pg.arn, pg.family, pg.desc)+"</DBParameterGroup>")
	}
	if filter != "" && len(items) == 0 {
		rdsError(w, http.StatusNotFound, "DBParameterGroupNotFound",
			fmt.Sprintf("DBParameterGroup '%s' not found.", filter))
		return
	}
	writeXML(w, http.StatusOK, wrap("DescribeDBParameterGroups", fmt.Sprintf(`
    <DescribeDBParameterGroupsResult>
      <DBParameterGroups>%s</DBParameterGroups>
    </DescribeDBParameterGroupsResult>`, strings.Join(items, ""))))
}

func (s *Service) describeDBParameters(w http.ResponseWriter, _ *http.Request) {
	writeXML(w, http.StatusOK, wrap("DescribeDBParameters", `
    <DescribeDBParametersResult>
      <Parameters/>
    </DescribeDBParametersResult>`))
}

func (s *Service) modifyDBParameterGroup(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("DBParameterGroupName")
	writeXML(w, http.StatusOK, wrap("ModifyDBParameterGroup", fmt.Sprintf(`
    <ModifyDBParameterGroupResult>
      <DBParameterGroupName>%s</DBParameterGroupName>
    </ModifyDBParameterGroupResult>`, name)))
}

func (s *Service) deleteDBParameterGroup(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("DBParameterGroupName")
	s.mu.Lock()
	delete(s.paramGrps, name)
	s.mu.Unlock()
	writeXML(w, http.StatusOK, wrap("DeleteDBParameterGroup", ""))
}

func paramGroupXML(name, arn, family, desc string) string {
	return fmt.Sprintf(`
        <DBParameterGroupArn>%s</DBParameterGroupArn>
        <DBParameterGroupName>%s</DBParameterGroupName>
        <DBParameterGroupFamily>%s</DBParameterGroupFamily>
        <Description>%s</Description>`, arn, name, family, desc)
}

func (s *Service) paramGroupARN(name string) string {
	return fmt.Sprintf("arn:aws:rds:%s:%s:pg:%s", s.region, accountID, name)
}

// ── Clusters ──────────────────────────────────────────────────────────────────

func (s *Service) createDBCluster(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("DBClusterIdentifier")
	engine := r.FormValue("Engine")
	engineVersion := r.FormValue("EngineVersion")
	dbName := r.FormValue("DatabaseName")
	masterUser := r.FormValue("MasterUsername")
	subnetGroup := r.FormValue("DBSubnetGroupName")
	arn := s.clusterARN(id)

	if subnetGroup != "" && !s.hasSubnetGroup(subnetGroup) {
		rdsError(w, http.StatusNotFound, "DBSubnetGroupNotFoundFault",
			fmt.Sprintf("DBSubnetGroup '%s' not found.", subnetGroup))
		return
	}

	c := &dbCluster{
		identifier:    id,
		arn:           arn,
		resourceID:    newResourceID("cluster"),
		engine:        engine,
		engineVersion: engineVersion,
		dbName:        dbName,
		masterUser:    masterUser,
		endpoint:      s.postgresHost,
		port:          s.postgresPort,
		status:        "available",
		subnetGroup:   subnetGroup,
		createdAt:     time.Now().UTC(),
	}
	applyPerformanceInsights(r, &c.perfInsights, &c.perfInsightsKMS, &c.perfInsightsRetention)

	s.mu.Lock()
	s.clusters[id] = c
	s.storeTags(r, arn)
	s.mu.Unlock()

	writeXML(w, http.StatusOK, wrap("CreateDBCluster", fmt.Sprintf(`
    <CreateDBClusterResult>
      <DBCluster>%s</DBCluster>
    </CreateDBClusterResult>`, s.clusterXML(c))))
}

func (s *Service) describeDBClusters(w http.ResponseWriter, r *http.Request) {
	filter := r.FormValue("DBClusterIdentifier")
	filters := parseFilters(r)
	s.mu.RLock()
	defer s.mu.RUnlock()

	var items []string
	identifierSeen := false
	for _, c := range s.clusters {
		if filter != "" && c.identifier != filter {
			continue
		}
		identifierSeen = true
		if !clusterMatchesFilters(c, filters) {
			continue
		}
		items = append(items, "<DBCluster>"+s.clusterXML(c)+"</DBCluster>")
	}
	if filter != "" && !identifierSeen {
		rdsError(w, http.StatusNotFound, "DBClusterNotFoundFault",
			fmt.Sprintf("DBCluster '%s' not found.", filter))
		return
	}
	writeXML(w, http.StatusOK, wrap("DescribeDBClusters", fmt.Sprintf(`
    <DescribeDBClustersResult>
      <DBClusters>%s</DBClusters>
    </DescribeDBClustersResult>`, strings.Join(items, ""))))
}

func (s *Service) modifyDBCluster(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("DBClusterIdentifier")
	s.mu.Lock()
	c, ok := s.clusters[id]
	if ok {
		applyPerformanceInsights(r, &c.perfInsights, &c.perfInsightsKMS, &c.perfInsightsRetention)
	}
	s.mu.Unlock()
	if !ok {
		rdsError(w, http.StatusNotFound, "DBClusterNotFoundFault",
			fmt.Sprintf("DBCluster '%s' not found.", id))
		return
	}
	writeXML(w, http.StatusOK, wrap("ModifyDBCluster", fmt.Sprintf(`
    <ModifyDBClusterResult>
      <DBCluster>%s</DBCluster>
    </ModifyDBClusterResult>`, s.clusterXML(c))))
}

func (s *Service) deleteDBCluster(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("DBClusterIdentifier")
	s.mu.Lock()
	c := s.clusters[id]
	delete(s.clusters, id)
	s.mu.Unlock()
	if c == nil {
		rdsError(w, http.StatusNotFound, "DBClusterNotFoundFault",
			fmt.Sprintf("DBCluster '%s' not found.", id))
		return
	}
	writeXML(w, http.StatusOK, wrap("DeleteDBCluster", fmt.Sprintf(`
    <DeleteDBClusterResult>
      <DBCluster>%s</DBCluster>
    </DeleteDBClusterResult>`, s.clusterXML(c))))
}

// hasSubnetGroup reports whether the named DB subnet group exists.
func (s *Service) hasSubnetGroup(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.subnetGroups[name]
	return ok
}

func (s *Service) clusterXML(c *dbCluster) string {
	// DBCluster carries the subnet group as a bare name, unlike DBInstance
	// which nests the whole structure.
	var opt string
	if c.subnetGroup != "" {
		opt = fmt.Sprintf(`
        <DBSubnetGroup>%s</DBSubnetGroup>`, c.subnetGroup)
	}
	return opt + fmt.Sprintf(`
        <DBClusterArn>%s</DBClusterArn>
        <DBClusterIdentifier>%s</DBClusterIdentifier>
        <DbClusterResourceId>%s</DbClusterResourceId>
        <Engine>%s</Engine>
        <EngineVersion>%s</EngineVersion>
        <DatabaseName>%s</DatabaseName>
        <MasterUsername>%s</MasterUsername>
        <Status>%s</Status>
        <Endpoint>%s</Endpoint>
        <ReaderEndpoint>%s</ReaderEndpoint>
        <Port>%d</Port>
        <MultiAZ>false</MultiAZ>
        <StorageEncrypted>false</StorageEncrypted>
        <ClusterCreateTime>%s</ClusterCreateTime>%s
        <DBClusterMembers/>
        <VpcSecurityGroups/>
        <AvailabilityZones>
          <AvailabilityZone>%s</AvailabilityZone>
        </AvailabilityZones>`,
		c.arn, c.identifier, c.resourceID, c.engine, c.engineVersion, c.dbName, c.masterUser,
		c.status, c.endpoint, c.endpoint, c.port,
		c.createdAt.Format(time.RFC3339),
		performanceInsightsXML(c.perfInsights, c.perfInsightsKMS, c.perfInsightsRetention),
		s.region+"a")
}

func (s *Service) clusterARN(id string) string {
	return fmt.Sprintf("arn:aws:rds:%s:%s:cluster:%s", s.region, accountID, id)
}

// ── Instances ─────────────────────────────────────────────────────────────────

func (s *Service) createDBInstance(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("DBInstanceIdentifier")
	clusterID := r.FormValue("DBClusterIdentifier")
	engine := r.FormValue("Engine")
	class := r.FormValue("DBInstanceClass")
	subnetGroup := r.FormValue("DBSubnetGroupName")
	arn := s.instanceARN(id)

	// Inherit endpoint and subnet group from the cluster if present. Cluster
	// members are created without DBSubnetGroupName — they sit in whatever
	// subnet group the cluster was placed in.
	endpoint := s.postgresHost
	port := s.postgresPort
	s.mu.RLock()
	c, isMember := s.clusters[clusterID]
	if isMember {
		endpoint = c.endpoint
		port = c.port
		if subnetGroup == "" {
			subnetGroup = c.subnetGroup
		}
	}
	_, groupExists := s.subnetGroups[subnetGroup]
	s.mu.RUnlock()

	if subnetGroup != "" && !groupExists {
		rdsError(w, http.StatusNotFound, "DBSubnetGroupNotFoundFault",
			fmt.Sprintf("DBSubnetGroup '%s' not found.", subnetGroup))
		return
	}

	inst := &dbInstance{
		identifier:    id,
		arn:           arn,
		resourceID:    newResourceID("db"),
		clusterID:     clusterID,
		engine:        engine,
		engineVersion: r.FormValue("EngineVersion"),
		class:         class,
		masterUser:    r.FormValue("MasterUsername"),
		dbName:        r.FormValue("DBName"),
		storageGB:     r.FormValue("AllocatedStorage"),
		endpoint:      endpoint,
		port:          port,
		status:        "available",
		subnetGroup:   subnetGroup,
		createdAt:     time.Now().UTC(),
	}
	applyPerformanceInsights(r, &inst.perfInsights, &inst.perfInsightsKMS, &inst.perfInsightsRetention)

	s.mu.Lock()
	s.instances[id] = inst
	s.storeTags(r, arn)
	body := s.instanceXML(inst)
	s.mu.Unlock()

	writeXML(w, http.StatusOK, wrap("CreateDBInstance", fmt.Sprintf(`
    <CreateDBInstanceResult>
      <DBInstance>%s</DBInstance>
    </CreateDBInstanceResult>`, body)))
}

func (s *Service) describeDBInstances(w http.ResponseWriter, r *http.Request) {
	filter := r.FormValue("DBInstanceIdentifier")
	filters := parseFilters(r)
	s.mu.RLock()
	defer s.mu.RUnlock()

	var items []string
	identifierSeen := false
	for _, inst := range s.instances {
		if filter != "" && inst.identifier != filter {
			continue
		}
		identifierSeen = true
		if !s.instanceMatchesFilters(inst, filters) {
			continue
		}
		items = append(items, "<DBInstance>"+s.instanceXML(inst)+"</DBInstance>")
	}
	// NotFound applies to the identifier param only — a filter that matches
	// nothing returns an empty list, as real RDS does.
	if filter != "" && !identifierSeen {
		rdsError(w, http.StatusNotFound, "DBInstanceNotFound",
			fmt.Sprintf("DBInstance '%s' not found.", filter))
		return
	}
	writeXML(w, http.StatusOK, wrap("DescribeDBInstances", fmt.Sprintf(`
    <DescribeDBInstancesResult>
      <DBInstances>%s</DBInstances>
    </DescribeDBInstancesResult>`, strings.Join(items, ""))))
}

func (s *Service) modifyDBInstance(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("DBInstanceIdentifier")
	s.mu.Lock()
	inst, ok := s.instances[id]
	var body string
	if ok {
		applyPerformanceInsights(r, &inst.perfInsights, &inst.perfInsightsKMS, &inst.perfInsightsRetention)
		body = s.instanceXML(inst)
	}
	s.mu.Unlock()
	if !ok {
		rdsError(w, http.StatusNotFound, "DBInstanceNotFound",
			fmt.Sprintf("DBInstance '%s' not found.", id))
		return
	}
	writeXML(w, http.StatusOK, wrap("ModifyDBInstance", fmt.Sprintf(`
    <ModifyDBInstanceResult>
      <DBInstance>%s</DBInstance>
    </ModifyDBInstanceResult>`, body)))
}

func (s *Service) deleteDBInstance(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("DBInstanceIdentifier")
	s.mu.Lock()
	inst := s.instances[id]
	delete(s.instances, id)
	var body string
	if inst != nil {
		body = s.instanceXML(inst)
	}
	s.mu.Unlock()
	if inst == nil {
		rdsError(w, http.StatusNotFound, "DBInstanceNotFound",
			fmt.Sprintf("DBInstance '%s' not found.", id))
		return
	}
	writeXML(w, http.StatusOK, wrap("DeleteDBInstance", fmt.Sprintf(`
    <DeleteDBInstanceResult>
      <DBInstance>%s</DBInstance>
    </DeleteDBInstanceResult>`, body)))
}

// instanceXML renders a DBInstance structure. Must be called with s.mu held —
// it reads the subnet group store.
func (s *Service) instanceXML(inst *dbInstance) string {
	// Optional elements only standalone instances carry — cluster members
	// inherit these from the cluster and the fields stay empty.
	var opt strings.Builder
	if inst.engineVersion != "" {
		fmt.Fprintf(&opt, `
        <EngineVersion>%s</EngineVersion>`, inst.engineVersion)
	}
	if inst.masterUser != "" {
		fmt.Fprintf(&opt, `
        <MasterUsername>%s</MasterUsername>`, inst.masterUser)
	}
	if inst.dbName != "" {
		fmt.Fprintf(&opt, `
        <DBName>%s</DBName>`, inst.dbName)
	}
	if inst.storageGB != "" {
		fmt.Fprintf(&opt, `
        <AllocatedStorage>%s</AllocatedStorage>`, inst.storageGB)
	}
	// Unlike DBCluster, DBInstance nests the full subnet group structure —
	// that's where the provider reads db_subnet_group_name from.
	if sg := s.subnetGroups[inst.subnetGroup]; sg != nil {
		fmt.Fprintf(&opt, `
        <DBSubnetGroup>%s</DBSubnetGroup>`, s.subnetGroupXML(sg))
	}
	return fmt.Sprintf(`
        <DBInstanceArn>%s</DBInstanceArn>
        <DBInstanceIdentifier>%s</DBInstanceIdentifier>
        <DbiResourceId>%s</DbiResourceId>
        <DBClusterIdentifier>%s</DBClusterIdentifier>
        <Engine>%s</Engine>
        <DBInstanceClass>%s</DBInstanceClass>
        <DBInstanceStatus>%s</DBInstanceStatus>%s
        <Endpoint>
          <Address>%s</Address>
          <Port>%d</Port>
        </Endpoint>
        <MultiAZ>false</MultiAZ>
        <StorageEncrypted>false</StorageEncrypted>
        <InstanceCreateTime>%s</InstanceCreateTime>%s
        <DBParameterGroups/>
        <VpcSecurityGroups/>
        <AvailabilityZone>%s</AvailabilityZone>`,
		inst.arn, inst.identifier, inst.resourceID, inst.clusterID, inst.engine, inst.class,
		inst.status, opt.String(), inst.endpoint, inst.port,
		inst.createdAt.Format(time.RFC3339),
		performanceInsightsXML(inst.perfInsights, inst.perfInsightsKMS, inst.perfInsightsRetention),
		s.region+"a")
}

func (s *Service) instanceARN(id string) string {
	return fmt.Sprintf("arn:aws:rds:%s:%s:db:%s", s.region, accountID, id)
}

// ── Engine / option queries ───────────────────────────────────────────────────

func (s *Service) describeDBEngineVersions(w http.ResponseWriter, r *http.Request) {
	engine := r.FormValue("Engine")
	version := r.FormValue("EngineVersion")
	if version == "" {
		version = "16.1"
	}
	writeXML(w, http.StatusOK, wrap("DescribeDBEngineVersions", fmt.Sprintf(`
    <DescribeDBEngineVersionsResult>
      <DBEngineVersions>
        <DBEngineVersion>
          <Engine>%s</Engine>
          <EngineVersion>%s</EngineVersion>
          <DBEngineDescription>%s</DBEngineDescription>
          <DBEngineVersionDescription>%s %s</DBEngineVersionDescription>
          <SupportedEngineModes><member>provisioned</member><member>serverless</member></SupportedEngineModes>
          <SupportedFeatureNames/>
        </DBEngineVersion>
      </DBEngineVersions>
    </DescribeDBEngineVersionsResult>`, engine, version, engine, engine, version)))
}

func (s *Service) describeOrderableDBInstanceOptions(w http.ResponseWriter, r *http.Request) {
	engine := r.FormValue("Engine")
	class := r.FormValue("DBInstanceClass")
	if class == "" {
		class = "db.serverless"
	}
	writeXML(w, http.StatusOK, wrap("DescribeOrderableDBInstanceOptions", fmt.Sprintf(`
    <DescribeOrderableDBInstanceOptionsResult>
      <OrderableDBInstanceOptions>
        <OrderableDBInstanceOption>
          <Engine>%s</Engine>
          <DBInstanceClass>%s</DBInstanceClass>
          <MultiAZCapable>false</MultiAZCapable>
          <ReadReplicaCapable>false</ReadReplicaCapable>
          <Vpc>true</Vpc>
        </OrderableDBInstanceOption>
      </OrderableDBInstanceOptions>
    </DescribeOrderableDBInstanceOptionsResult>`, engine, class)))
}

// ── Tags ──────────────────────────────────────────────────────────────────────

func (s *Service) addTagsToResource(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("ResourceName")
	s.mu.Lock()
	s.storeTags(r, arn)
	s.mu.Unlock()
	writeXML(w, http.StatusOK, wrap("AddTagsToResource", ""))
}

func (s *Service) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("ResourceName")
	s.mu.RLock()
	tags := s.tags[arn]
	s.mu.RUnlock()

	var items []string
	for k, v := range tags {
		items = append(items, fmt.Sprintf("<Tag><Key>%s</Key><Value>%s</Value></Tag>", k, v))
	}
	writeXML(w, http.StatusOK, wrap("ListTagsForResource", fmt.Sprintf(`
    <ListTagsForResourceResult>
      <TagList>%s</TagList>
    </ListTagsForResourceResult>`, strings.Join(items, ""))))
}

func (s *Service) removeTagsFromResource(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("ResourceName")
	s.mu.Lock()
	if tags, ok := s.tags[arn]; ok {
		for i := 1; ; i++ {
			key := r.FormValue(fmt.Sprintf("TagKeys.member.%d", i))
			if key == "" {
				break
			}
			delete(tags, key)
		}
	}
	s.mu.Unlock()
	writeXML(w, http.StatusOK, wrap("RemoveTagsFromResource", ""))
}

// storeTags parses Tags.Tag.N.Key/Value from the form and stores them.
// Must be called with s.mu held for writing (or during construction).
func (s *Service) storeTags(r *http.Request, arn string) {
	tags := map[string]string{}
	for i := 1; ; i++ {
		key := r.FormValue(fmt.Sprintf("Tags.Tag.%d.Key", i))
		if key == "" {
			break
		}
		tags[key] = r.FormValue(fmt.Sprintf("Tags.Tag.%d.Value", i))
	}
	if len(tags) > 0 {
		if s.tags[arn] == nil {
			s.tags[arn] = map[string]string{}
		}
		for k, v := range tags {
			s.tags[arn][k] = v
		}
	}
}

// ── Describe filters ──────────────────────────────────────────────────────────

// parseFilters reads Filters.Filter.N.Name / Filters.Filter.N.Values.Value.M
// query-protocol params into a name -> values map. The TF AWS provider reads
// resources with these filters instead of the identifier params.
func parseFilters(r *http.Request) map[string][]string {
	filters := map[string][]string{}
	for i := 1; ; i++ {
		name := r.FormValue(fmt.Sprintf("Filters.Filter.%d.Name", i))
		if name == "" {
			break
		}
		var vals []string
		for j := 1; ; j++ {
			v := r.FormValue(fmt.Sprintf("Filters.Filter.%d.Values.Value.%d", i, j))
			if v == "" {
				break
			}
			vals = append(vals, v)
		}
		filters[name] = vals
	}
	return filters
}

// instanceMatchesFilters reports whether inst satisfies every filter. A
// filter's values are OR-ed; identifier filters match by name or ARN because
// the TF provider passes either. Unknown filter names are ignored rather than
// rejected. Must be called with s.mu held.
func (s *Service) instanceMatchesFilters(inst *dbInstance, filters map[string][]string) bool {
	for name, values := range filters {
		matched := true
		switch name {
		case "db-instance-id":
			matched = containsAny(values, inst.identifier, inst.arn)
		case "db-cluster-id":
			matched = inst.clusterID != "" &&
				containsAny(values, inst.clusterID, s.clusterARN(inst.clusterID))
		case "dbi-resource-id":
			matched = containsAny(values, inst.resourceID)
		}
		if !matched {
			return false
		}
	}
	return true
}

// clusterMatchesFilters is the DBCluster analogue of instanceMatchesFilters.
func clusterMatchesFilters(c *dbCluster, filters map[string][]string) bool {
	for name, values := range filters {
		matched := true
		switch name {
		case "db-cluster-id":
			matched = containsAny(values, c.identifier, c.arn)
		case "db-cluster-resource-id":
			matched = containsAny(values, c.resourceID)
		}
		if !matched {
			return false
		}
	}
	return true
}

func containsAny(values []string, candidates ...string) bool {
	for _, v := range values {
		for _, c := range candidates {
			if c != "" && v == c {
				return true
			}
		}
	}
	return false
}

// ── Performance Insights ──────────────────────────────────────────────────────

// HasResourceID reports whether any instance or cluster owns the given
// Performance Insights resource ID (DbiResourceId / DbClusterResourceId).
// Used by the PI service to validate identifiers.
func (s *Service) HasResourceID(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, inst := range s.instances {
		if inst.resourceID == id {
			return true
		}
	}
	for _, c := range s.clusters {
		if c.resourceID == id {
			return true
		}
	}
	return false
}

// applyPerformanceInsights copies Performance Insights form fields into the
// target fields. Only keys present in the form are applied, so Modify calls
// that don't touch PI leave existing values intact. Retention defaults to 7
// days (the AWS free-tier default) when PI is enabled without an explicit value.
func applyPerformanceInsights(r *http.Request, enabled *bool, kmsKeyID *string, retention *int) {
	if r.Form.Has("EnablePerformanceInsights") {
		*enabled = r.FormValue("EnablePerformanceInsights") == "true"
	}
	if r.Form.Has("PerformanceInsightsKMSKeyId") {
		*kmsKeyID = r.FormValue("PerformanceInsightsKMSKeyId")
	}
	if r.Form.Has("PerformanceInsightsRetentionPeriod") {
		if n, err := strconv.Atoi(r.FormValue("PerformanceInsightsRetentionPeriod")); err == nil {
			*retention = n
		}
	}
	if *enabled && *retention == 0 {
		*retention = 7
	}
}

func performanceInsightsXML(enabled bool, kmsKeyID string, retention int) string {
	if !enabled {
		return `
        <PerformanceInsightsEnabled>false</PerformanceInsightsEnabled>`
	}
	var b strings.Builder
	b.WriteString(`
        <PerformanceInsightsEnabled>true</PerformanceInsightsEnabled>`)
	if kmsKeyID != "" {
		fmt.Fprintf(&b, `
        <PerformanceInsightsKMSKeyId>%s</PerformanceInsightsKMSKeyId>`, kmsKeyID)
	}
	fmt.Fprintf(&b, `
        <PerformanceInsightsRetentionPeriod>%d</PerformanceInsightsRetentionPeriod>`, retention)
	return b.String()
}

// newResourceID generates an immutable AWS-style resource ID such as
// db-3NHX0G0S8X8DE2DSQFVIL5EBBU or cluster-....
func newResourceID(prefix string) string {
	hex := strings.ToUpper(strings.ReplaceAll(uid.New(), "-", ""))
	return prefix + "-" + hex[:26]
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func wrap(action, body string) string {
	return fmt.Sprintf(`<%sResponse xmlns=%q>%s<ResponseMetadata><RequestId>%s</RequestId></ResponseMetadata></%sResponse>`,
		action, rdsNS, body, uid.New(), action)
}

func writeXML(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(status)
	fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>`)
	fmt.Fprint(w, body)
}

func rdsError(w http.ResponseWriter, status int, code, msg string) {
	writeXML(w, status, fmt.Sprintf(
		`<ErrorResponse xmlns=%q><Error><Type>Sender</Type><Code>%s</Code><Message>%s</Message></Error><RequestId>%s</RequestId></ErrorResponse>`,
		rdsNS, code, msg, uid.New()))
}
