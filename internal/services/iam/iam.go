package iam

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/nimbus-local/nimbus/internal/uid"
)

const accountID = "000000000000"

// Service implements the AWS IAM + STS emulator.
// Resources are stored in-memory. No policy enforcement —
// any AssumeRole succeeds and returns fake credentials.
type Service struct {
	mu               sync.RWMutex
	roles            map[string]*role             // name -> role
	attachments      map[string][]string          // roleName -> []policyARN
	managedPolicies  map[string]*managedPolicy    // arn -> policy
	inlinePolicies   map[string]map[string]string // roleName -> policyName -> document
	instanceProfiles map[string]*instanceProfile  // name -> profile
}

type instanceProfile struct {
	name, profileID, arn, path, roleName string
	createdAt                            time.Time
}

type managedPolicy struct {
	name, policyID, arn, path, description, policyDocument string
	createdAt, updatedAt                                   time.Time
}

type role struct {
	name                     string
	roleID                   string
	arn                      string
	path                     string
	description              string
	assumeRolePolicyDocument string
	maxSessionDuration       int
	createdAt                time.Time
	tags                     map[string]string
}

func New() *Service {
	return &Service{
		roles:            map[string]*role{},
		attachments:      map[string][]string{},
		managedPolicies:  map[string]*managedPolicy{},
		inlinePolicies:   map[string]map[string]string{},
		instanceProfiles: map[string]*instanceProfile{},
	}
}

func (s *Service) Name() string { return "iam" }

// Reset clears all in-memory state.
func (s *Service) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.roles = map[string]*role{}
	s.attachments = map[string][]string{}
	s.managedPolicies = map[string]*managedPolicy{}
	s.inlinePolicies = map[string]map[string]string{}
	s.instanceProfiles = map[string]*instanceProfile{}
}

// Detect identifies IAM (2010-05-08) and STS (2011-06-15) requests.
// Both services use the query protocol: form-encoded body with a Version parameter.
func (s *Service) Detect(r *http.Request) bool {
	if !strings.Contains(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		return false
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	b := string(body)
	return strings.Contains(b, "Version=2010-05-08") || strings.Contains(b, "Version=2011-06-15")
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		xmlError(w, http.StatusBadRequest, "InvalidParameterValue", "cannot parse request body")
		return
	}
	switch r.FormValue("Action") {
	case "CreateRole":
		s.createRole(w, r)
	case "GetRole":
		s.getRole(w, r)
	case "DeleteRole":
		s.deleteRole(w, r)
	case "ListRoles":
		s.listRoles(w, r)
	case "UpdateRole":
		s.updateRole(w, r)
	case "TagRole":
		s.tagRole(w, r)
	case "UntagRole":
		s.untagRole(w, r)
	case "ListRoleTags":
		s.listRoleTags(w, r)
	case "CreateInstanceProfile":
		s.createInstanceProfile(w, r)
	case "GetInstanceProfile":
		s.getInstanceProfile(w, r)
	case "DeleteInstanceProfile":
		s.deleteInstanceProfile(w, r)
	case "ListInstanceProfiles":
		s.listInstanceProfiles(w, r)
	case "ListInstanceProfilesForRole":
		s.listInstanceProfilesForRole(w, r)
	case "AddRoleToInstanceProfile":
		s.addRoleToInstanceProfile(w, r)
	case "RemoveRoleFromInstanceProfile":
		s.removeRoleFromInstanceProfile(w, r)
	case "CreatePolicy":
		s.createPolicy(w, r)
	case "GetPolicy":
		s.getPolicy(w, r)
	case "GetPolicyVersion":
		s.getPolicyVersion(w, r)
	case "ListPolicyVersions":
		s.listPolicyVersions(w, r)
	case "DeletePolicy":
		s.deletePolicy(w, r)
	case "ListPolicies":
		s.listPolicies(w, r)
	case "PutRolePolicy":
		s.putRolePolicy(w, r)
	case "GetRolePolicy":
		s.getRolePolicy(w, r)
	case "DeleteRolePolicy":
		s.deleteRolePolicy(w, r)
	case "ListRolePolicies":
		s.listRolePolicies(w, r)
	case "AttachRolePolicy":
		s.attachRolePolicy(w, r)
	case "DetachRolePolicy":
		s.detachRolePolicy(w, r)
	case "ListAttachedRolePolicies":
		s.listAttachedRolePolicies(w, r)
	case "AssumeRole":
		s.assumeRole(w, r)
	case "GetCallerIdentity":
		s.getCallerIdentity(w, r)
	default:
		xmlError(w, http.StatusBadRequest, "InvalidAction",
			fmt.Sprintf("Action %s is not supported.", r.FormValue("Action")))
	}
}

// --- Roles ---

func (s *Service) createRole(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("RoleName")
	if name == "" {
		xmlError(w, http.StatusBadRequest, "ValidationError", "RoleName is required")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.roles[name]; exists {
		xmlError(w, http.StatusConflict, "EntityAlreadyExists",
			fmt.Sprintf("Role with name %s already exists.", name))
		return
	}

	path := r.FormValue("Path")
	if path == "" {
		path = "/"
	}
	ro := &role{
		name:                     name,
		roleID:                   makeID("AROA"),
		arn:                      roleARN(name),
		path:                     path,
		description:              r.FormValue("Description"),
		assumeRolePolicyDocument: r.FormValue("AssumeRolePolicyDocument"),
		maxSessionDuration:       3600,
		createdAt:                time.Now().UTC(),
		tags:                     map[string]string{},
	}
	s.roles[name] = ro
	writeXML(w, http.StatusOK, iamWrap("CreateRole", renderRole(ro)))
}

func (s *Service) getRole(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("RoleName")
	s.mu.RLock()
	ro, ok := s.roles[name]
	s.mu.RUnlock()
	if !ok {
		xmlError(w, http.StatusNotFound, "NoSuchEntity",
			fmt.Sprintf("Role %s not found.", name))
		return
	}
	writeXML(w, http.StatusOK, iamWrap("GetRole", renderRole(ro)))
}

func (s *Service) deleteRole(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("RoleName")
	s.mu.Lock()
	if _, ok := s.roles[name]; !ok {
		s.mu.Unlock()
		xmlError(w, http.StatusNotFound, "NoSuchEntity",
			fmt.Sprintf("Role %s not found.", name))
		return
	}
	delete(s.roles, name)
	s.mu.Unlock()
	writeXML(w, http.StatusOK, iamEmpty("DeleteRole"))
}

func (s *Service) listRoles(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var buf strings.Builder
	for _, ro := range s.roles {
		buf.WriteString("<member>")
		buf.WriteString(renderRoleFields(ro))
		buf.WriteString("</member>")
	}
	result := fmt.Sprintf("<Roles>%s</Roles><IsTruncated>false</IsTruncated>", buf.String())
	writeXML(w, http.StatusOK, iamWrap("ListRoles", result))
}

// --- Role tags + update ---

func (s *Service) updateRole(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("RoleName")
	s.mu.Lock()
	ro, ok := s.roles[name]
	if !ok {
		s.mu.Unlock()
		xmlError(w, http.StatusNotFound, "NoSuchEntity", fmt.Sprintf("Role %s not found.", name))
		return
	}
	if v := r.FormValue("Description"); v != "" {
		ro.description = v
	}
	if v := r.FormValue("MaxSessionDuration"); v != "" {
		fmt.Sscanf(v, "%d", &ro.maxSessionDuration)
	}
	s.mu.Unlock()
	writeXML(w, http.StatusOK, iamEmpty("UpdateRole"))
}

func (s *Service) tagRole(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("RoleName")
	s.mu.Lock()
	ro, ok := s.roles[name]
	if !ok {
		s.mu.Unlock()
		xmlError(w, http.StatusNotFound, "NoSuchEntity", fmt.Sprintf("Role %s not found.", name))
		return
	}
	for i := 1; ; i++ {
		k := r.FormValue(fmt.Sprintf("Tags.member.%d.Key", i))
		if k == "" {
			break
		}
		ro.tags[k] = r.FormValue(fmt.Sprintf("Tags.member.%d.Value", i))
	}
	s.mu.Unlock()
	writeXML(w, http.StatusOK, iamEmpty("TagRole"))
}

func (s *Service) untagRole(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("RoleName")
	s.mu.Lock()
	ro, ok := s.roles[name]
	if !ok {
		s.mu.Unlock()
		xmlError(w, http.StatusNotFound, "NoSuchEntity", fmt.Sprintf("Role %s not found.", name))
		return
	}
	for i := 1; ; i++ {
		k := r.FormValue(fmt.Sprintf("TagKeys.member.%d", i))
		if k == "" {
			break
		}
		delete(ro.tags, k)
	}
	s.mu.Unlock()
	writeXML(w, http.StatusOK, iamEmpty("UntagRole"))
}

func (s *Service) listRoleTags(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("RoleName")
	s.mu.RLock()
	ro, ok := s.roles[name]
	s.mu.RUnlock()
	if !ok {
		xmlError(w, http.StatusNotFound, "NoSuchEntity", fmt.Sprintf("Role %s not found.", name))
		return
	}
	var buf strings.Builder
	for k, v := range ro.tags {
		buf.WriteString(fmt.Sprintf("<member><Key>%s</Key><Value>%s</Value></member>", esc(k), esc(v)))
	}
	result := fmt.Sprintf("<Tags>%s</Tags><IsTruncated>false</IsTruncated>", buf.String())
	writeXML(w, http.StatusOK, iamWrap("ListRoleTags", result))
}

// --- Instance profiles ---

func (s *Service) createInstanceProfile(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("InstanceProfileName")
	if name == "" {
		xmlError(w, http.StatusBadRequest, "ValidationError", "InstanceProfileName is required")
		return
	}
	path := r.FormValue("Path")
	if path == "" {
		path = "/"
	}
	p := &instanceProfile{
		name:      name,
		profileID: makeID("AIPA"),
		arn:       profileARN(name),
		path:      path,
		createdAt: time.Now().UTC(),
	}
	s.mu.Lock()
	if _, exists := s.instanceProfiles[name]; exists {
		s.mu.Unlock()
		xmlError(w, http.StatusConflict, "EntityAlreadyExists",
			fmt.Sprintf("Instance profile %s already exists.", name))
		return
	}
	s.instanceProfiles[name] = p
	profileXML := s.renderProfile(p)
	s.mu.Unlock()
	writeXML(w, http.StatusOK, iamWrap("CreateInstanceProfile", profileXML))
}

func (s *Service) getInstanceProfile(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("InstanceProfileName")
	s.mu.RLock()
	p, ok := s.instanceProfiles[name]
	var profileXML string
	if ok {
		profileXML = s.renderProfile(p)
	}
	s.mu.RUnlock()
	if !ok {
		xmlError(w, http.StatusNotFound, "NoSuchEntity",
			fmt.Sprintf("Instance profile %s not found.", name))
		return
	}
	writeXML(w, http.StatusOK, iamWrap("GetInstanceProfile", profileXML))
}

func (s *Service) deleteInstanceProfile(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("InstanceProfileName")
	s.mu.Lock()
	delete(s.instanceProfiles, name)
	s.mu.Unlock()
	writeXML(w, http.StatusOK, iamEmpty("DeleteInstanceProfile"))
}

func (s *Service) listInstanceProfiles(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var buf strings.Builder
	for _, p := range s.instanceProfiles {
		buf.WriteString("<member>")
		buf.WriteString(s.renderProfile(p))
		buf.WriteString("</member>")
	}
	result := fmt.Sprintf("<InstanceProfiles>%s</InstanceProfiles><IsTruncated>false</IsTruncated>",
		buf.String())
	writeXML(w, http.StatusOK, iamWrap("ListInstanceProfiles", result))
}

func (s *Service) listInstanceProfilesForRole(w http.ResponseWriter, r *http.Request) {
	roleName := r.FormValue("RoleName")
	s.mu.RLock()
	defer s.mu.RUnlock()
	var buf strings.Builder
	for _, p := range s.instanceProfiles {
		if p.roleName != roleName {
			continue
		}
		buf.WriteString("<member>")
		buf.WriteString(s.renderProfile(p))
		buf.WriteString("</member>")
	}
	result := fmt.Sprintf("<InstanceProfiles>%s</InstanceProfiles><IsTruncated>false</IsTruncated>",
		buf.String())
	writeXML(w, http.StatusOK, iamWrap("ListInstanceProfilesForRole", result))
}

func (s *Service) addRoleToInstanceProfile(w http.ResponseWriter, r *http.Request) {
	profileName := r.FormValue("InstanceProfileName")
	roleName := r.FormValue("RoleName")
	s.mu.Lock()
	p, ok := s.instanceProfiles[profileName]
	if !ok {
		s.mu.Unlock()
		xmlError(w, http.StatusNotFound, "NoSuchEntity",
			fmt.Sprintf("Instance profile %s not found.", profileName))
		return
	}
	p.roleName = roleName
	s.mu.Unlock()
	writeXML(w, http.StatusOK, iamEmpty("AddRoleToInstanceProfile"))
}

func (s *Service) removeRoleFromInstanceProfile(w http.ResponseWriter, r *http.Request) {
	profileName := r.FormValue("InstanceProfileName")
	s.mu.Lock()
	if p, ok := s.instanceProfiles[profileName]; ok {
		p.roleName = ""
	}
	s.mu.Unlock()
	writeXML(w, http.StatusOK, iamEmpty("RemoveRoleFromInstanceProfile"))
}

// --- Managed policies ---

func (s *Service) createPolicy(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("PolicyName")
	if name == "" {
		xmlError(w, http.StatusBadRequest, "ValidationError", "PolicyName is required")
		return
	}
	path := r.FormValue("Path")
	if path == "" {
		path = "/"
	}
	now := time.Now().UTC()
	p := &managedPolicy{
		name:           name,
		policyID:       makeID("ANPA"),
		arn:            policyARN(name),
		path:           path,
		description:    r.FormValue("Description"),
		policyDocument: r.FormValue("PolicyDocument"),
		createdAt:      now,
		updatedAt:      now,
	}

	s.mu.Lock()
	if _, exists := s.managedPolicies[p.arn]; exists {
		s.mu.Unlock()
		xmlError(w, http.StatusConflict, "EntityAlreadyExists",
			fmt.Sprintf("Policy %s already exists.", name))
		return
	}
	s.managedPolicies[p.arn] = p
	s.mu.Unlock()
	writeXML(w, http.StatusOK, iamWrap("CreatePolicy", renderPolicy(p)))
}

func (s *Service) getPolicy(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("PolicyArn")
	s.mu.RLock()
	p := s.managedPolicies[arn]
	s.mu.RUnlock()
	if p == nil {
		// Stub for AWS-managed policy ARNs so Terraform data sources work.
		if strings.HasPrefix(arn, "arn:aws:iam::aws:") {
			writeXML(w, http.StatusOK, iamWrap("GetPolicy", renderAWSManagedPolicy(arn)))
			return
		}
		xmlError(w, http.StatusNotFound, "NoSuchEntity",
			fmt.Sprintf("Policy %s not found.", arn))
		return
	}
	writeXML(w, http.StatusOK, iamWrap("GetPolicy", renderPolicy(p)))
}

func (s *Service) getPolicyVersion(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("PolicyArn")
	s.mu.RLock()
	p := s.managedPolicies[arn]
	s.mu.RUnlock()
	doc := "{}"
	if p != nil {
		doc = p.policyDocument
	}
	result := fmt.Sprintf(`<PolicyVersion>
    <Document>%s</Document>
    <VersionId>v1</VersionId>
    <IsDefaultVersion>true</IsDefaultVersion>
    <CreateDate>%s</CreateDate>
  </PolicyVersion>`, url.QueryEscape(doc), time.Now().UTC().Format(time.RFC3339))
	writeXML(w, http.StatusOK, iamWrap("GetPolicyVersion", result))
}

func (s *Service) listPolicyVersions(w http.ResponseWriter, r *http.Request) {
	version := fmt.Sprintf(`<member>
    <VersionId>v1</VersionId>
    <IsDefaultVersion>true</IsDefaultVersion>
    <CreateDate>%s</CreateDate>
  </member>`, time.Now().UTC().Format(time.RFC3339))
	result := fmt.Sprintf("<Versions>%s</Versions><IsTruncated>false</IsTruncated>", version)
	writeXML(w, http.StatusOK, iamWrap("ListPolicyVersions", result))
}

func (s *Service) deletePolicy(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("PolicyArn")
	s.mu.Lock()
	delete(s.managedPolicies, arn)
	s.mu.Unlock()
	writeXML(w, http.StatusOK, iamEmpty("DeletePolicy"))
}

func (s *Service) listPolicies(w http.ResponseWriter, r *http.Request) {
	scope := r.FormValue("Scope") // Local, AWS, All — we only hold Local
	s.mu.RLock()
	defer s.mu.RUnlock()
	var buf strings.Builder
	if scope != "AWS" {
		for _, p := range s.managedPolicies {
			buf.WriteString("<member>")
			buf.WriteString(renderPolicy(p))
			buf.WriteString("</member>")
		}
	}
	result := fmt.Sprintf("<Policies>%s</Policies><IsTruncated>false</IsTruncated>", buf.String())
	writeXML(w, http.StatusOK, iamWrap("ListPolicies", result))
}

// --- Inline policies ---

func (s *Service) putRolePolicy(w http.ResponseWriter, r *http.Request) {
	roleName := r.FormValue("RoleName")
	policyName := r.FormValue("PolicyName")
	doc := r.FormValue("PolicyDocument")

	s.mu.Lock()
	if _, ok := s.roles[roleName]; !ok {
		s.mu.Unlock()
		xmlError(w, http.StatusNotFound, "NoSuchEntity",
			fmt.Sprintf("Role %s not found.", roleName))
		return
	}
	if s.inlinePolicies[roleName] == nil {
		s.inlinePolicies[roleName] = map[string]string{}
	}
	s.inlinePolicies[roleName][policyName] = doc
	s.mu.Unlock()
	writeXML(w, http.StatusOK, iamEmpty("PutRolePolicy"))
}

func (s *Service) getRolePolicy(w http.ResponseWriter, r *http.Request) {
	roleName := r.FormValue("RoleName")
	policyName := r.FormValue("PolicyName")

	s.mu.RLock()
	doc, ok := s.inlinePolicies[roleName][policyName]
	s.mu.RUnlock()
	if !ok {
		xmlError(w, http.StatusNotFound, "NoSuchEntity",
			fmt.Sprintf("Policy %s not found on role %s.", policyName, roleName))
		return
	}
	result := fmt.Sprintf("<RoleName>%s</RoleName><PolicyName>%s</PolicyName><PolicyDocument>%s</PolicyDocument>",
		esc(roleName), esc(policyName), url.QueryEscape(doc))
	writeXML(w, http.StatusOK, iamWrap("GetRolePolicy", result))
}

func (s *Service) deleteRolePolicy(w http.ResponseWriter, r *http.Request) {
	roleName := r.FormValue("RoleName")
	policyName := r.FormValue("PolicyName")
	s.mu.Lock()
	delete(s.inlinePolicies[roleName], policyName)
	s.mu.Unlock()
	writeXML(w, http.StatusOK, iamEmpty("DeleteRolePolicy"))
}

func (s *Service) listRolePolicies(w http.ResponseWriter, r *http.Request) {
	roleName := r.FormValue("RoleName")
	s.mu.RLock()
	policies := s.inlinePolicies[roleName]
	s.mu.RUnlock()
	var buf strings.Builder
	for name := range policies {
		buf.WriteString(fmt.Sprintf("<member>%s</member>", esc(name)))
	}
	result := fmt.Sprintf("<PolicyNames>%s</PolicyNames><IsTruncated>false</IsTruncated>", buf.String())
	writeXML(w, http.StatusOK, iamWrap("ListRolePolicies", result))
}

// --- Policy attachments ---

func (s *Service) attachRolePolicy(w http.ResponseWriter, r *http.Request) {
	roleName := r.FormValue("RoleName")
	policyArn := r.FormValue("PolicyArn")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.roles[roleName]; !ok {
		xmlError(w, http.StatusNotFound, "NoSuchEntity",
			fmt.Sprintf("Role %s not found.", roleName))
		return
	}
	for _, a := range s.attachments[roleName] {
		if a == policyArn {
			writeXML(w, http.StatusOK, iamEmpty("AttachRolePolicy"))
			return
		}
	}
	s.attachments[roleName] = append(s.attachments[roleName], policyArn)
	writeXML(w, http.StatusOK, iamEmpty("AttachRolePolicy"))
}

func (s *Service) detachRolePolicy(w http.ResponseWriter, r *http.Request) {
	roleName := r.FormValue("RoleName")
	policyArn := r.FormValue("PolicyArn")

	s.mu.Lock()
	atts := s.attachments[roleName]
	for i, a := range atts {
		if a == policyArn {
			s.attachments[roleName] = append(atts[:i], atts[i+1:]...)
			break
		}
	}
	s.mu.Unlock()
	writeXML(w, http.StatusOK, iamEmpty("DetachRolePolicy"))
}

func (s *Service) listAttachedRolePolicies(w http.ResponseWriter, r *http.Request) {
	roleName := r.FormValue("RoleName")

	s.mu.RLock()
	atts := s.attachments[roleName]
	s.mu.RUnlock()

	var buf strings.Builder
	for _, arn := range atts {
		buf.WriteString(fmt.Sprintf(
			"<member><PolicyName>%s</PolicyName><PolicyArn>%s</PolicyArn></member>",
			esc(arnLeaf(arn)), esc(arn)))
	}
	result := fmt.Sprintf("<AttachedPolicies>%s</AttachedPolicies><IsTruncated>false</IsTruncated>",
		buf.String())
	writeXML(w, http.StatusOK, iamWrap("ListAttachedRolePolicies", result))
}

// --- STS ---

func (s *Service) assumeRole(w http.ResponseWriter, r *http.Request) {
	roleArn := r.FormValue("RoleArn")
	session := r.FormValue("RoleSessionName")
	if session == "" {
		session = "nimbus-session"
	}
	id := makeRawID()
	expiry := time.Now().UTC().Add(12 * time.Hour)
	result := fmt.Sprintf(`<Credentials>
    <AccessKeyId>ASIA%s</AccessKeyId>
    <SecretAccessKey>fake-secret-key</SecretAccessKey>
    <SessionToken>fake-session-token-%s</SessionToken>
    <Expiration>%s</Expiration>
  </Credentials>
  <AssumedRoleUser>
    <AssumedRoleId>AROA%s:%s</AssumedRoleId>
    <Arn>arn:aws:sts::%s:assumed-role/%s/%s</Arn>
  </AssumedRoleUser>
  <PackedPolicySize>0</PackedPolicySize>`,
		id[:16], uid.New()[:8], expiry.Format(time.RFC3339),
		id[:16], esc(session),
		accountID, esc(arnLeaf(roleArn)), esc(session))
	writeXML(w, http.StatusOK, stsWrap("AssumeRole", result))
}

func (s *Service) getCallerIdentity(w http.ResponseWriter, r *http.Request) {
	result := fmt.Sprintf(`<Account>%s</Account>
  <UserId>AIDAIOSFODNN7EXAMPLE</UserId>
  <Arn>arn:aws:iam::%s:root</Arn>`, accountID, accountID)
	writeXML(w, http.StatusOK, stsWrap("GetCallerIdentity", result))
}

// --- Render helpers ---

func renderPolicy(p *managedPolicy) string {
	return fmt.Sprintf(`<Policy>
    <PolicyName>%s</PolicyName>
    <PolicyId>%s</PolicyId>
    <Arn>%s</Arn>
    <Path>%s</Path>
    <DefaultVersionId>v1</DefaultVersionId>
    <AttachmentCount>0</AttachmentCount>
    <IsAttachable>true</IsAttachable>
    <CreateDate>%s</CreateDate>
    <UpdateDate>%s</UpdateDate>
    <Description>%s</Description>
  </Policy>`,
		esc(p.name), esc(p.policyID), esc(p.arn), esc(p.path),
		p.createdAt.Format(time.RFC3339), p.updatedAt.Format(time.RFC3339),
		esc(p.description))
}

func renderAWSManagedPolicy(arn string) string {
	return fmt.Sprintf(`<Policy>
    <PolicyName>%s</PolicyName>
    <PolicyId>ANPA000000000000AWS</PolicyId>
    <Arn>%s</Arn>
    <Path>/</Path>
    <DefaultVersionId>v1</DefaultVersionId>
    <AttachmentCount>0</AttachmentCount>
    <IsAttachable>true</IsAttachable>
    <CreateDate>2015-01-01T00:00:00Z</CreateDate>
    <UpdateDate>2015-01-01T00:00:00Z</UpdateDate>
  </Policy>`, esc(arnLeaf(arn)), esc(arn))
}

// renderProfile must be called with s.mu held (reads s.roles for the nested role).
func (s *Service) renderProfile(p *instanceProfile) string {
	var roleXML string
	if p.roleName != "" {
		if ro, ok := s.roles[p.roleName]; ok {
			// In the query protocol, list members contain fields directly — no <Role> wrapper.
			roleXML = "<member>" + renderRoleFields(ro) + "</member>"
		}
	}
	return fmt.Sprintf(`<InstanceProfile>
    <Path>%s</Path>
    <InstanceProfileName>%s</InstanceProfileName>
    <InstanceProfileId>%s</InstanceProfileId>
    <Arn>%s</Arn>
    <CreateDate>%s</CreateDate>
    <Roles>%s</Roles>
  </InstanceProfile>`,
		esc(p.path), esc(p.name), esc(p.profileID), esc(p.arn),
		p.createdAt.Format(time.RFC3339), roleXML)
}

// renderRoleFields returns the inner XML fields of a role without the outer <Role> wrapper.
// Use inside <member> elements for list responses (ListRoles, InstanceProfile.Roles).
func renderRoleFields(ro *role) string {
	return fmt.Sprintf(`
    <Path>%s</Path>
    <RoleName>%s</RoleName>
    <RoleId>%s</RoleId>
    <Arn>%s</Arn>
    <CreateDate>%s</CreateDate>
    <AssumeRolePolicyDocument>%s</AssumeRolePolicyDocument>
    <Description>%s</Description>
    <MaxSessionDuration>%d</MaxSessionDuration>`,
		esc(ro.path), esc(ro.name), esc(ro.roleID), esc(ro.arn),
		ro.createdAt.Format(time.RFC3339),
		url.QueryEscape(ro.assumeRolePolicyDocument),
		esc(ro.description),
		ro.maxSessionDuration)
}

func renderRole(ro *role) string {
	return "<Role>" + renderRoleFields(ro) + "\n  </Role>"
}

// --- XML response wrappers ---

// iamWrap returns an IAM response with a result element.
func iamWrap(action, resultBody string) string {
	return fmt.Sprintf(`<%sResponse xmlns="https://iam.amazonaws.com/doc/2010-05-08/">
  <%sResult>%s</%sResult>
  <ResponseMetadata><RequestId>%s</RequestId></ResponseMetadata>
</%sResponse>`, action, action, resultBody, action, uid.New(), action)
}

// iamEmpty returns an IAM response with no result element (delete/put operations).
func iamEmpty(action string) string {
	return fmt.Sprintf(`<%sResponse xmlns="https://iam.amazonaws.com/doc/2010-05-08/">
  <ResponseMetadata><RequestId>%s</RequestId></ResponseMetadata>
</%sResponse>`, action, uid.New(), action)
}

// stsWrap returns a STS response envelope.
func stsWrap(action, resultBody string) string {
	return fmt.Sprintf(`<%sResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
  <%sResult>%s</%sResult>
  <ResponseMetadata><RequestId>%s</RequestId></ResponseMetadata>
</%sResponse>`, action, action, resultBody, action, uid.New(), action)
}

func writeXML(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>%s`, body)
}

func xmlError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><ErrorResponse xmlns="https://iam.amazonaws.com/doc/2010-05-08/">
  <Error><Code>%s</Code><Message>%s</Message></Error>
  <RequestId>%s</RequestId>
</ErrorResponse>`, esc(code), esc(message), uid.New())
}

// esc XML-escapes a string.
func esc(s string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

// arnLeaf extracts the last path component from an ARN.
func arnLeaf(arn string) string {
	if i := strings.LastIndex(arn, "/"); i != -1 {
		return arn[i+1:]
	}
	return arn
}

func roleARN(name string) string {
	return fmt.Sprintf("arn:aws:iam::%s:role/%s", accountID, name)
}

func policyARN(name string) string {
	return fmt.Sprintf("arn:aws:iam::%s:policy/%s", accountID, name)
}

func profileARN(name string) string {
	return fmt.Sprintf("arn:aws:iam::%s:instance-profile/%s", accountID, name)
}

// makeID returns a prefix + 16 uppercase hex chars derived from a UUID.
func makeID(prefix string) string {
	return prefix + makeRawID()[:16]
}

// makeRawID returns 32 uppercase hex chars from a UUID (dashes removed).
func makeRawID() string {
	return strings.ToUpper(strings.ReplaceAll(uid.New(), "-", ""))
}
