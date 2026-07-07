// Package rds emulates the AWS RDS/Aurora control plane.
// All state is in-memory. DB clusters resolve to a real Postgres sidecar
// running alongside Nimbus. Subnet groups and parameter groups are accepted
// and stored verbatim — no VPC or subnet validation is performed.
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
}

type dbSubnetGroup struct {
	name        string
	description string
	arn         string
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
	createdAt     time.Time

	perfInsights          bool
	perfInsightsKMS       string
	perfInsightsRetention int
}

type dbInstance struct {
	identifier string
	arn        string
	resourceID string // DbiResourceId, e.g. db-ABC...
	clusterID  string
	engine     string
	class      string
	endpoint   string
	port       int
	status     string
	createdAt  time.Time

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

	s.mu.Lock()
	s.subnetGroups[name] = &dbSubnetGroup{name: name, description: desc, arn: arn}
	s.storeTags(r, arn)
	s.mu.Unlock()

	writeXML(w, http.StatusOK, wrap("CreateDBSubnetGroup", fmt.Sprintf(`
    <CreateDBSubnetGroupResult>
      <DBSubnetGroup>%s</DBSubnetGroup>
    </CreateDBSubnetGroupResult>`, s.subnetGroupXML(&dbSubnetGroup{name: name, description: desc, arn: arn}))))
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
	delete(s.subnetGroups, name)
	s.mu.Unlock()
	writeXML(w, http.StatusOK, wrap("DeleteDBSubnetGroup", ""))
}

func (s *Service) modifyDBSubnetGroup(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("DBSubnetGroupName")
	s.mu.RLock()
	sg := s.subnetGroups[name]
	s.mu.RUnlock()
	if sg == nil {
		rdsError(w, http.StatusNotFound, "DBSubnetGroupNotFoundFault",
			fmt.Sprintf("DBSubnetGroup '%s' not found.", name))
		return
	}
	writeXML(w, http.StatusOK, wrap("ModifyDBSubnetGroup", fmt.Sprintf(`
    <ModifyDBSubnetGroupResult>
      <DBSubnetGroup>%s</DBSubnetGroup>
    </ModifyDBSubnetGroupResult>`, s.subnetGroupXML(sg))))
}

func (s *Service) subnetGroupXML(sg *dbSubnetGroup) string {
	return fmt.Sprintf(`
        <DBSubnetGroupArn>%s</DBSubnetGroupArn>
        <DBSubnetGroupDescription>%s</DBSubnetGroupDescription>
        <DBSubnetGroupName>%s</DBSubnetGroupName>
        <SubnetGroupStatus>Complete</SubnetGroupStatus>
        <VpcId>vpc-00000000000000001</VpcId>
        <Subnets/>`, sg.arn, sg.description, sg.name)
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
	arn := s.clusterARN(id)

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
	s.mu.RLock()
	defer s.mu.RUnlock()

	var items []string
	for _, c := range s.clusters {
		if filter != "" && c.identifier != filter {
			continue
		}
		items = append(items, "<DBCluster>"+s.clusterXML(c)+"</DBCluster>")
	}
	if filter != "" && len(items) == 0 {
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

func (s *Service) clusterXML(c *dbCluster) string {
	return fmt.Sprintf(`
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
	arn := s.instanceARN(id)

	// Inherit endpoint from cluster if present
	endpoint := s.postgresHost
	port := s.postgresPort
	s.mu.RLock()
	if c, ok := s.clusters[clusterID]; ok {
		endpoint = c.endpoint
		port = c.port
	}
	s.mu.RUnlock()

	inst := &dbInstance{
		identifier: id,
		arn:        arn,
		resourceID: newResourceID("db"),
		clusterID:  clusterID,
		engine:     engine,
		class:      class,
		endpoint:   endpoint,
		port:       port,
		status:     "available",
		createdAt:  time.Now().UTC(),
	}
	applyPerformanceInsights(r, &inst.perfInsights, &inst.perfInsightsKMS, &inst.perfInsightsRetention)

	s.mu.Lock()
	s.instances[id] = inst
	s.storeTags(r, arn)
	s.mu.Unlock()

	writeXML(w, http.StatusOK, wrap("CreateDBInstance", fmt.Sprintf(`
    <CreateDBInstanceResult>
      <DBInstance>%s</DBInstance>
    </CreateDBInstanceResult>`, s.instanceXML(inst))))
}

func (s *Service) describeDBInstances(w http.ResponseWriter, r *http.Request) {
	filter := r.FormValue("DBInstanceIdentifier")
	s.mu.RLock()
	defer s.mu.RUnlock()

	var items []string
	for _, inst := range s.instances {
		if filter != "" && inst.identifier != filter {
			continue
		}
		items = append(items, "<DBInstance>"+s.instanceXML(inst)+"</DBInstance>")
	}
	if filter != "" && len(items) == 0 {
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
	if ok {
		applyPerformanceInsights(r, &inst.perfInsights, &inst.perfInsightsKMS, &inst.perfInsightsRetention)
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
    </ModifyDBInstanceResult>`, s.instanceXML(inst))))
}

func (s *Service) deleteDBInstance(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("DBInstanceIdentifier")
	s.mu.Lock()
	inst := s.instances[id]
	delete(s.instances, id)
	s.mu.Unlock()
	if inst == nil {
		rdsError(w, http.StatusNotFound, "DBInstanceNotFound",
			fmt.Sprintf("DBInstance '%s' not found.", id))
		return
	}
	writeXML(w, http.StatusOK, wrap("DeleteDBInstance", fmt.Sprintf(`
    <DeleteDBInstanceResult>
      <DBInstance>%s</DBInstance>
    </DeleteDBInstanceResult>`, s.instanceXML(inst))))
}

func (s *Service) instanceXML(inst *dbInstance) string {
	return fmt.Sprintf(`
        <DBInstanceArn>%s</DBInstanceArn>
        <DBInstanceIdentifier>%s</DBInstanceIdentifier>
        <DbiResourceId>%s</DbiResourceId>
        <DBClusterIdentifier>%s</DBClusterIdentifier>
        <Engine>%s</Engine>
        <DBInstanceClass>%s</DBInstanceClass>
        <DBInstanceStatus>%s</DBInstanceStatus>
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
		inst.status, inst.endpoint, inst.port,
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

// ── Performance Insights ──────────────────────────────────────────────────────

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
