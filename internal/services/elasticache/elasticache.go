// Package elasticache emulates the AWS ElastiCache control plane.
// All state is in-memory. Cache clusters resolve to a real Valkey sidecar
// running alongside Nimbus. Subnet groups and parameter groups are accepted
// and stored verbatim — no VPC or subnet validation is performed.
package elasticache

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
	accountID    = "000000000000"
	ecNS         = "http://elasticache.amazonaws.com/doc/2015-02-02/"
	ecAPIVersion = "2015-02-02"
)

// Service implements the AWS ElastiCache control plane.
type Service struct {
	mu           sync.RWMutex
	subnetGroups map[string]*cacheSubnetGroup
	paramGroups  map[string]*cacheParamGroup
	clusters     map[string]*cacheCluster
	replGroups   map[string]*replicationGroup
	tags         map[string]map[string]string // arn -> tags
	region       string
	valkeyHost   string
	valkeyPort   int
}

type cacheSubnetGroup struct {
	name        string
	description string
	arn         string
	subnetIDs   []string
}

type cacheParamGroup struct {
	name   string
	arn    string
	family string
	desc   string
}

type cacheCluster struct {
	id        string
	arn       string
	engine    string
	engineVer string
	nodeType  string
	numNodes  int
	endpoint  string
	port      int
	status    string
	createdAt time.Time
}

type replicationGroup struct {
	id          string
	arn         string
	description string
	engine      string
	engineVer   string
	nodeType    string
	endpoint    string
	port        int
	status      string
	// memberClusters are the cache clusters that make up the group. Terraform
	// derives num_cache_clusters from this list, so an empty one reads as zero
	// nodes and drifts on every plan.
	memberClusters []string
	createdAt      time.Time
}

// New creates an ElastiCache service. valkeyHost:valkeyPort point at the Valkey
// sidecar that cluster endpoints will resolve to.
func New(region, valkeyHost string, valkeyPort int) *Service {
	if region == "" {
		region = "us-east-1"
	}
	if valkeyHost == "" {
		valkeyHost = "localhost"
	}
	if valkeyPort == 0 {
		valkeyPort = 6379
	}
	return &Service{
		region:       region,
		valkeyHost:   valkeyHost,
		valkeyPort:   valkeyPort,
		subnetGroups: map[string]*cacheSubnetGroup{},
		paramGroups:  map[string]*cacheParamGroup{},
		clusters:     map[string]*cacheCluster{},
		replGroups:   map[string]*replicationGroup{},
		tags:         map[string]map[string]string{},
	}
}

func (s *Service) Name() string { return "elasticache" }

// Reset clears all in-memory state.
func (s *Service) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subnetGroups = map[string]*cacheSubnetGroup{}
	s.paramGroups = map[string]*cacheParamGroup{}
	s.clusters = map[string]*cacheCluster{}
	s.replGroups = map[string]*replicationGroup{}
	s.tags = map[string]map[string]string{}
}

func (s *Service) Detect(r *http.Request) bool {
	if !strings.Contains(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		return false
	}
	_ = r.ParseForm()
	return r.FormValue("Version") == ecAPIVersion
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		ecError(w, http.StatusBadRequest, "InvalidParameterValue", "cannot parse request body")
		return
	}
	switch r.FormValue("Action") {
	// Subnet groups
	case "CreateCacheSubnetGroup":
		s.createCacheSubnetGroup(w, r)
	case "DescribeCacheSubnetGroups":
		s.describeCacheSubnetGroups(w, r)
	case "DeleteCacheSubnetGroup":
		s.deleteCacheSubnetGroup(w, r)
	case "ModifyCacheSubnetGroup":
		s.modifyCacheSubnetGroup(w, r)
	// Parameter groups
	case "CreateCacheParameterGroup":
		s.createCacheParameterGroup(w, r)
	case "DescribeCacheParameterGroups":
		s.describeCacheParameterGroups(w, r)
	case "DescribeCacheParameters":
		s.describeCacheParameters(w)
	case "ModifyCacheParameterGroup":
		s.modifyCacheParameterGroup(w, r)
	case "DeleteCacheParameterGroup":
		s.deleteCacheParameterGroup(w, r)
	// Clusters
	case "CreateCacheCluster":
		s.createCacheCluster(w, r)
	case "DescribeCacheClusters":
		s.describeCacheClusters(w, r)
	case "ModifyCacheCluster":
		s.modifyCacheCluster(w, r)
	case "DeleteCacheCluster":
		s.deleteCacheCluster(w, r)
	// Replication groups
	case "CreateReplicationGroup":
		s.createReplicationGroup(w, r)
	case "DescribeReplicationGroups":
		s.describeReplicationGroups(w, r)
	case "ModifyReplicationGroup":
		s.modifyReplicationGroup(w, r)
	case "DeleteReplicationGroup":
		s.deleteReplicationGroup(w, r)
	case "IncreaseReplicaCount":
		s.increaseReplicaCount(w, r)
	case "DecreaseReplicaCount":
		s.decreaseReplicaCount(w, r)
	// Engine versions — stub
	case "DescribeCacheEngineVersions":
		s.describeCacheEngineVersions(w, r)
	// Tags
	case "AddTagsToResource":
		s.addTagsToResource(w, r)
	case "ListTagsForResource":
		s.listTagsForResource(w, r)
	case "RemoveTagsFromResource":
		s.removeTagsFromResource(w, r)
	default:
		ecError(w, http.StatusBadRequest, "InvalidAction",
			fmt.Sprintf("Action %s is not supported.", r.FormValue("Action")))
	}
}

// ── Inspection endpoint ───────────────────────────────────────────────────────

// ClustersHandler serves /_nimbus/elasticache/clusters.
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
		fmt.Fprintf(w, `{"id":%q,"endpoint":%q,"port":%d,"status":%q}`,
			c.id, c.endpoint, c.port, c.status)
		i++
	}
	for _, rg := range s.replGroups {
		if i > 0 {
			fmt.Fprint(w, ",")
		}
		fmt.Fprintf(w, `{"id":%q,"endpoint":%q,"port":%d,"status":%q,"type":"replication-group"}`,
			rg.id, rg.endpoint, rg.port, rg.status)
		i++
	}
	fmt.Fprint(w, "]")
}

// ── Subnet groups ─────────────────────────────────────────────────────────────

func (s *Service) createCacheSubnetGroup(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("CacheSubnetGroupName")
	desc := r.FormValue("CacheSubnetGroupDescription")
	arn := s.subnetGroupARN(name)

	s.mu.Lock()
	sg := &cacheSubnetGroup{name: name, description: desc, arn: arn, subnetIDs: parseSubnetIDs(r)}
	s.subnetGroups[name] = sg
	s.storeTags(r, arn)
	s.mu.Unlock()

	writeXML(w, http.StatusOK, wrap("CreateCacheSubnetGroup", fmt.Sprintf(`
    <CreateCacheSubnetGroupResult>
      <CacheSubnetGroup>%s</CacheSubnetGroup>
    </CreateCacheSubnetGroupResult>`, subnetGroupXML(sg))))
}

func (s *Service) describeCacheSubnetGroups(w http.ResponseWriter, r *http.Request) {
	filter := r.FormValue("CacheSubnetGroupName")
	s.mu.RLock()
	defer s.mu.RUnlock()

	var items []string
	for _, sg := range s.subnetGroups {
		if filter != "" && sg.name != filter {
			continue
		}
		items = append(items, "<CacheSubnetGroup>"+subnetGroupXML(sg)+"</CacheSubnetGroup>")
	}
	if filter != "" && len(items) == 0 {
		ecError(w, http.StatusNotFound, "CacheSubnetGroupNotFoundFault",
			fmt.Sprintf("CacheSubnetGroup '%s' not found.", filter))
		return
	}
	writeXML(w, http.StatusOK, wrap("DescribeCacheSubnetGroups", fmt.Sprintf(`
    <DescribeCacheSubnetGroupsResult>
      <CacheSubnetGroups>%s</CacheSubnetGroups>
    </DescribeCacheSubnetGroupsResult>`, strings.Join(items, ""))))
}

func (s *Service) deleteCacheSubnetGroup(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("CacheSubnetGroupName")
	s.mu.Lock()
	delete(s.subnetGroups, name)
	s.mu.Unlock()
	writeXML(w, http.StatusOK, wrap("DeleteCacheSubnetGroup", ""))
}

func (s *Service) modifyCacheSubnetGroup(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("CacheSubnetGroupName")
	s.mu.RLock()
	sg := s.subnetGroups[name]
	s.mu.RUnlock()
	if sg == nil {
		ecError(w, http.StatusNotFound, "CacheSubnetGroupNotFoundFault",
			fmt.Sprintf("CacheSubnetGroup '%s' not found.", name))
		return
	}
	if ids := parseSubnetIDs(r); len(ids) > 0 {
		s.mu.Lock()
		sg.subnetIDs = ids
		s.mu.Unlock()
	}
	writeXML(w, http.StatusOK, wrap("ModifyCacheSubnetGroup", fmt.Sprintf(`
    <ModifyCacheSubnetGroupResult>
      <CacheSubnetGroup>%s</CacheSubnetGroup>
    </ModifyCacheSubnetGroupResult>`, subnetGroupXML(sg))))
}

// subnetGroupXML reports a cache subnet group, including the subnets it was
// created with — an empty <Subnets/> left the Terraform provider re-applying the
// subnet list on every plan.
func subnetGroupXML(sg *cacheSubnetGroup) string {
	var subnets strings.Builder
	for _, id := range sg.subnetIDs {
		fmt.Fprintf(&subnets, `
          <Subnet><SubnetIdentifier>%s</SubnetIdentifier></Subnet>`, id)
	}
	return fmt.Sprintf(`
        <ARN>%s</ARN>
        <CacheSubnetGroupDescription>%s</CacheSubnetGroupDescription>
        <CacheSubnetGroupName>%s</CacheSubnetGroupName>
        <VpcId>vpc-00000000000000001</VpcId>
        <Subnets>%s</Subnets>`, sg.arn, sg.description, sg.name, subnets.String())
}

// parseSubnetIDs reads the SubnetIds list from a Create/ModifyCacheSubnetGroup
// form. The query protocol names the member `SubnetIdentifier`; `member` is
// accepted too since some SDK versions emit it.
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

func (s *Service) subnetGroupARN(name string) string {
	return fmt.Sprintf("arn:aws:elasticache:%s:%s:subnetgroup:%s", s.region, accountID, name)
}

// ── Parameter groups ──────────────────────────────────────────────────────────

func (s *Service) createCacheParameterGroup(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("CacheParameterGroupName")
	family := r.FormValue("CacheParameterGroupFamily")
	desc := r.FormValue("Description")
	arn := s.paramGroupARN(name)

	s.mu.Lock()
	s.paramGroups[name] = &cacheParamGroup{name: name, arn: arn, family: family, desc: desc}
	s.storeTags(r, arn)
	s.mu.Unlock()

	writeXML(w, http.StatusOK, wrap("CreateCacheParameterGroup", fmt.Sprintf(`
    <CreateCacheParameterGroupResult>
      <CacheParameterGroup>%s</CacheParameterGroup>
    </CreateCacheParameterGroupResult>`, paramGroupXML(name, arn, family, desc))))
}

func (s *Service) describeCacheParameterGroups(w http.ResponseWriter, r *http.Request) {
	filter := r.FormValue("CacheParameterGroupName")
	s.mu.RLock()
	defer s.mu.RUnlock()

	var items []string
	for _, pg := range s.paramGroups {
		if filter != "" && pg.name != filter {
			continue
		}
		items = append(items, "<CacheParameterGroup>"+paramGroupXML(pg.name, pg.arn, pg.family, pg.desc)+"</CacheParameterGroup>")
	}
	if filter != "" && len(items) == 0 {
		ecError(w, http.StatusNotFound, "CacheParameterGroupNotFound",
			fmt.Sprintf("CacheParameterGroup '%s' not found.", filter))
		return
	}
	writeXML(w, http.StatusOK, wrap("DescribeCacheParameterGroups", fmt.Sprintf(`
    <DescribeCacheParameterGroupsResult>
      <CacheParameterGroups>%s</CacheParameterGroups>
    </DescribeCacheParameterGroupsResult>`, strings.Join(items, ""))))
}

func (s *Service) describeCacheParameters(w http.ResponseWriter) {
	writeXML(w, http.StatusOK, wrap("DescribeCacheParameters", `
    <DescribeCacheParametersResult>
      <Parameters/>
    </DescribeCacheParametersResult>`))
}

func (s *Service) modifyCacheParameterGroup(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("CacheParameterGroupName")
	writeXML(w, http.StatusOK, wrap("ModifyCacheParameterGroup", fmt.Sprintf(`
    <ModifyCacheParameterGroupResult>
      <CacheParameterGroupName>%s</CacheParameterGroupName>
    </ModifyCacheParameterGroupResult>`, name)))
}

func (s *Service) deleteCacheParameterGroup(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("CacheParameterGroupName")
	s.mu.Lock()
	delete(s.paramGroups, name)
	s.mu.Unlock()
	writeXML(w, http.StatusOK, wrap("DeleteCacheParameterGroup", ""))
}

func paramGroupXML(name, arn, family, desc string) string {
	return fmt.Sprintf(`
        <ARN>%s</ARN>
        <CacheParameterGroupName>%s</CacheParameterGroupName>
        <CacheParameterGroupFamily>%s</CacheParameterGroupFamily>
        <Description>%s</Description>
        <IsGlobal>false</IsGlobal>`, arn, name, family, desc)
}

func (s *Service) paramGroupARN(name string) string {
	return fmt.Sprintf("arn:aws:elasticache:%s:%s:parametergroup:%s", s.region, accountID, name)
}

// ── Clusters ──────────────────────────────────────────────────────────────────

func (s *Service) createCacheCluster(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("CacheClusterId")
	engine := r.FormValue("Engine")
	if engine == "" {
		engine = "valkey"
	}
	engineVer := r.FormValue("EngineVersion")
	if engineVer == "" {
		engineVer = "7.2"
	}
	nodeType := r.FormValue("CacheNodeType")
	arn := s.clusterARN(id)

	c := &cacheCluster{
		id:        id,
		arn:       arn,
		engine:    engine,
		engineVer: engineVer,
		nodeType:  nodeType,
		numNodes:  1,
		endpoint:  s.valkeyHost,
		port:      s.valkeyPort,
		status:    "available",
		createdAt: time.Now().UTC(),
	}

	s.mu.Lock()
	s.clusters[id] = c
	s.storeTags(r, arn)
	s.mu.Unlock()

	writeXML(w, http.StatusOK, wrap("CreateCacheCluster", fmt.Sprintf(`
    <CreateCacheClusterResult>
      <CacheCluster>%s</CacheCluster>
    </CreateCacheClusterResult>`, s.clusterXML(c))))
}

func (s *Service) describeCacheClusters(w http.ResponseWriter, r *http.Request) {
	filter := r.FormValue("CacheClusterId")
	s.mu.RLock()
	defer s.mu.RUnlock()

	var items []string
	for _, c := range s.clusters {
		if filter != "" && c.id != filter {
			continue
		}
		items = append(items, "<CacheCluster>"+s.clusterXML(c)+"</CacheCluster>")
	}
	if filter != "" && len(items) == 0 {
		ecError(w, http.StatusNotFound, "CacheClusterNotFound",
			fmt.Sprintf("CacheCluster '%s' not found.", filter))
		return
	}
	writeXML(w, http.StatusOK, wrap("DescribeCacheClusters", fmt.Sprintf(`
    <DescribeCacheClustersResult>
      <CacheClusters>%s</CacheClusters>
    </DescribeCacheClustersResult>`, strings.Join(items, ""))))
}

func (s *Service) modifyCacheCluster(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("CacheClusterId")
	s.mu.RLock()
	c, ok := s.clusters[id]
	s.mu.RUnlock()
	if !ok {
		ecError(w, http.StatusNotFound, "CacheClusterNotFound",
			fmt.Sprintf("CacheCluster '%s' not found.", id))
		return
	}
	writeXML(w, http.StatusOK, wrap("ModifyCacheCluster", fmt.Sprintf(`
    <ModifyCacheClusterResult>
      <CacheCluster>%s</CacheCluster>
    </ModifyCacheClusterResult>`, s.clusterXML(c))))
}

func (s *Service) deleteCacheCluster(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("CacheClusterId")
	s.mu.Lock()
	c := s.clusters[id]
	delete(s.clusters, id)
	s.mu.Unlock()
	if c == nil {
		ecError(w, http.StatusNotFound, "CacheClusterNotFound",
			fmt.Sprintf("CacheCluster '%s' not found.", id))
		return
	}
	writeXML(w, http.StatusOK, wrap("DeleteCacheCluster", fmt.Sprintf(`
    <DeleteCacheClusterResult>
      <CacheCluster>%s</CacheCluster>
    </DeleteCacheClusterResult>`, s.clusterXML(c))))
}

func (s *Service) clusterXML(c *cacheCluster) string {
	return fmt.Sprintf(`
        <ARN>%s</ARN>
        <CacheClusterId>%s</CacheClusterId>
        <Engine>%s</Engine>
        <EngineVersion>%s</EngineVersion>
        <CacheNodeType>%s</CacheNodeType>
        <CacheClusterStatus>%s</CacheClusterStatus>
        <NumCacheNodes>%d</NumCacheNodes>
        <CacheClusterCreateTime>%s</CacheClusterCreateTime>
        <ConfigurationEndpoint>
          <Address>%s</Address>
          <Port>%d</Port>
        </ConfigurationEndpoint>
        <CacheNodes>
          <CacheNode>
            <CacheNodeId>0001</CacheNodeId>
            <CacheNodeStatus>available</CacheNodeStatus>
            <Endpoint>
              <Address>%s</Address>
              <Port>%d</Port>
            </Endpoint>
          </CacheNode>
        </CacheNodes>
        <SecurityGroups/>`,
		c.arn, c.id, c.engine, c.engineVer, c.nodeType, c.status, c.numNodes,
		c.createdAt.Format(time.RFC3339),
		c.endpoint, c.port, c.endpoint, c.port)
}

func (s *Service) clusterARN(id string) string {
	return fmt.Sprintf("arn:aws:elasticache:%s:%s:cluster:%s", s.region, accountID, id)
}

// ── Replication groups ────────────────────────────────────────────────────────

func (s *Service) createReplicationGroup(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("ReplicationGroupId")
	desc := r.FormValue("ReplicationGroupDescription")
	engine := r.FormValue("Engine")
	if engine == "" {
		engine = "valkey"
	}
	engineVer := r.FormValue("EngineVersion")
	if engineVer == "" {
		engineVer = "7.2"
	}
	nodeType := r.FormValue("CacheNodeType")
	arn := s.replGroupARN(id)

	// One member per requested node, named the way real ElastiCache names them
	// (<group>-001). NumCacheClusters and NumNodeGroups/ReplicasPerNodeGroup are
	// alternative spellings of the same request; default to a single node.
	count := formInt(r, "NumCacheClusters")
	if count == 0 {
		count = formInt(r, "NumNodeGroups") + formInt(r, "ReplicasPerNodeGroup")
	}
	if count == 0 {
		count = 1
	}
	members := make([]string, 0, count)
	for i := 1; i <= count; i++ {
		members = append(members, fmt.Sprintf("%s-%03d", id, i))
	}

	rg := &replicationGroup{
		id:             id,
		arn:            arn,
		description:    desc,
		engine:         engine,
		engineVer:      engineVer,
		nodeType:       nodeType,
		endpoint:       s.valkeyHost,
		port:           s.valkeyPort,
		status:         "available",
		memberClusters: members,
		createdAt:      time.Now().UTC(),
	}

	s.mu.Lock()
	s.replGroups[id] = rg
	s.storeTags(r, arn)
	s.mu.Unlock()

	writeXML(w, http.StatusOK, wrap("CreateReplicationGroup", fmt.Sprintf(`
    <CreateReplicationGroupResult>
      <ReplicationGroup>%s</ReplicationGroup>
    </CreateReplicationGroupResult>`, s.replGroupXML(rg))))
}

func (s *Service) describeReplicationGroups(w http.ResponseWriter, r *http.Request) {
	filter := r.FormValue("ReplicationGroupId")
	s.mu.RLock()
	defer s.mu.RUnlock()

	var items []string
	for _, rg := range s.replGroups {
		if filter != "" && rg.id != filter {
			continue
		}
		items = append(items, "<ReplicationGroup>"+s.replGroupXML(rg)+"</ReplicationGroup>")
	}
	if filter != "" && len(items) == 0 {
		ecError(w, http.StatusNotFound, "ReplicationGroupNotFoundFault",
			fmt.Sprintf("ReplicationGroup '%s' not found.", filter))
		return
	}
	writeXML(w, http.StatusOK, wrap("DescribeReplicationGroups", fmt.Sprintf(`
    <DescribeReplicationGroupsResult>
      <ReplicationGroups>%s</ReplicationGroups>
    </DescribeReplicationGroupsResult>`, strings.Join(items, ""))))
}

func (s *Service) modifyReplicationGroup(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("ReplicationGroupId")
	s.mu.RLock()
	rg, ok := s.replGroups[id]
	s.mu.RUnlock()
	if !ok {
		ecError(w, http.StatusNotFound, "ReplicationGroupNotFoundFault",
			fmt.Sprintf("ReplicationGroup '%s' not found.", id))
		return
	}
	writeXML(w, http.StatusOK, wrap("ModifyReplicationGroup", fmt.Sprintf(`
    <ModifyReplicationGroupResult>
      <ReplicationGroup>%s</ReplicationGroup>
    </ModifyReplicationGroupResult>`, s.replGroupXML(rg))))
}

func (s *Service) deleteReplicationGroup(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("ReplicationGroupId")
	s.mu.Lock()
	rg := s.replGroups[id]
	delete(s.replGroups, id)
	s.mu.Unlock()
	if rg == nil {
		ecError(w, http.StatusNotFound, "ReplicationGroupNotFoundFault",
			fmt.Sprintf("ReplicationGroup '%s' not found.", id))
		return
	}
	writeXML(w, http.StatusOK, wrap("DeleteReplicationGroup", fmt.Sprintf(`
    <DeleteReplicationGroupResult>
      <ReplicationGroup>%s</ReplicationGroup>
    </DeleteReplicationGroupResult>`, s.replGroupXML(rg))))
}

func (s *Service) increaseReplicaCount(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("ReplicationGroupId")
	s.mu.RLock()
	rg := s.replGroups[id]
	s.mu.RUnlock()
	if rg == nil {
		ecError(w, http.StatusNotFound, "ReplicationGroupNotFoundFault",
			fmt.Sprintf("ReplicationGroup '%s' not found.", id))
		return
	}
	writeXML(w, http.StatusOK, wrap("IncreaseReplicaCount", fmt.Sprintf(`
    <IncreaseReplicaCountResult>
      <ReplicationGroup>%s</ReplicationGroup>
    </IncreaseReplicaCountResult>`, s.replGroupXML(rg))))
}

func (s *Service) decreaseReplicaCount(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("ReplicationGroupId")
	s.mu.RLock()
	rg := s.replGroups[id]
	s.mu.RUnlock()
	if rg == nil {
		ecError(w, http.StatusNotFound, "ReplicationGroupNotFoundFault",
			fmt.Sprintf("ReplicationGroup '%s' not found.", id))
		return
	}
	writeXML(w, http.StatusOK, wrap("DecreaseReplicaCount", fmt.Sprintf(`
    <DecreaseReplicaCountResult>
      <ReplicationGroup>%s</ReplicationGroup>
    </DecreaseReplicaCountResult>`, s.replGroupXML(rg))))
}

func (s *Service) replGroupXML(rg *replicationGroup) string {
	return fmt.Sprintf(`
        <ARN>%s</ARN>
        <ReplicationGroupId>%s</ReplicationGroupId>
        <Description>%s</Description>
        <Status>%s</Status>
        <ClusterEnabled>false</ClusterEnabled>
        <ConfigurationEndpoint>
          <Address>%s</Address>
          <Port>%d</Port>
        </ConfigurationEndpoint>
        <NodeGroups>
          <NodeGroup>
            <NodeGroupId>0001</NodeGroupId>
            <Status>available</Status>
            <PrimaryEndpoint>
              <Address>%s</Address>
              <Port>%d</Port>
            </PrimaryEndpoint>
            <ReaderEndpoint>
              <Address>%s</Address>
              <Port>%d</Port>
            </ReaderEndpoint>
            <NodeGroupMembers/>
          </NodeGroup>
        </NodeGroups>
        <MemberClusters>%s</MemberClusters>
        <SnapshottingClusterId/>`,
		rg.arn, rg.id, rg.description, rg.status,
		rg.endpoint, rg.port,
		rg.endpoint, rg.port,
		rg.endpoint, rg.port,
		memberClustersXML(rg.memberClusters))
}

// memberClustersXML renders a replication group's member cluster IDs.
func memberClustersXML(members []string) string {
	var b strings.Builder
	for _, m := range members {
		fmt.Fprintf(&b, `
          <ClusterId>%s</ClusterId>`, m)
	}
	return b.String()
}

// formInt reads an integer form field, returning 0 when absent or unparsable.
func formInt(r *http.Request, key string) int {
	n, err := strconv.Atoi(r.FormValue(key))
	if err != nil {
		return 0
	}
	return n
}

func (s *Service) replGroupARN(id string) string {
	return fmt.Sprintf("arn:aws:elasticache:%s:%s:replicationgroup:%s", s.region, accountID, id)
}

// ── Engine versions ───────────────────────────────────────────────────────────

func (s *Service) describeCacheEngineVersions(w http.ResponseWriter, r *http.Request) {
	engine := r.FormValue("Engine")
	if engine == "" {
		engine = "valkey"
	}
	version := r.FormValue("EngineVersion")
	if version == "" {
		version = "7.2"
	}
	writeXML(w, http.StatusOK, wrap("DescribeCacheEngineVersions", fmt.Sprintf(`
    <DescribeCacheEngineVersionsResult>
      <CacheEngineVersions>
        <CacheEngineVersion>
          <Engine>%s</Engine>
          <EngineVersion>%s</EngineVersion>
          <CacheEngineDescription>%s</CacheEngineDescription>
          <CacheEngineVersionDescription>%s %s</CacheEngineVersionDescription>
          <CacheParameterGroupFamily>%s%s</CacheParameterGroupFamily>
        </CacheEngineVersion>
      </CacheEngineVersions>
    </DescribeCacheEngineVersionsResult>`, engine, version, engine, engine, version, engine, majorVer(version))))
}

func majorVer(v string) string {
	if idx := strings.Index(v, "."); idx > 0 {
		return v[:idx]
	}
	return v
}

// ── Tags ──────────────────────────────────────────────────────────────────────

func (s *Service) addTagsToResource(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("ResourceName")
	s.mu.Lock()
	s.storeTags(r, arn)
	s.mu.Unlock()
	writeXML(w, http.StatusOK, wrap("AddTagsToResource", `<AddTagsToResourceResult/>`))
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
	writeXML(w, http.StatusOK, wrap("RemoveTagsFromResource", `<RemoveTagsFromResourceResult/>`))
}

func (s *Service) storeTags(r *http.Request, arn string) {
	tags := map[string]string{}
	for i := 1; ; i++ {
		// ElastiCache query protocol uses locationName "Tag" for list members.
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

// ── Helpers ───────────────────────────────────────────────────────────────────

func wrap(action, body string) string {
	return fmt.Sprintf(`<%sResponse xmlns=%q>%s<ResponseMetadata><RequestId>%s</RequestId></ResponseMetadata></%sResponse>`,
		action, ecNS, body, uid.New(), action)
}

func writeXML(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(status)
	fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>`)
	fmt.Fprint(w, body)
}

func ecError(w http.ResponseWriter, status int, code, msg string) {
	writeXML(w, status, fmt.Sprintf(
		`<ErrorResponse xmlns=%q><Error><Type>Sender</Type><Code>%s</Code><Message>%s</Message></Error><RequestId>%s</RequestId></ErrorResponse>`,
		ecNS, code, msg, uid.New()))
}
