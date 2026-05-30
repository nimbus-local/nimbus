// Package route53 emulates the AWS Route 53 control plane.
// All state is in-memory. Hosted zones and record sets are accepted and stored
// verbatim — no DNS validation or resolution is performed. GetChange always
// returns INSYNC. Nothing is forwarded to AWS.
package route53

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nimbus-local/nimbus/internal/uid"
)

const (
	r53NS = "https://route53.amazonaws.com/doc/2013-04-01/"
)

// Service implements the AWS Route 53 control plane.
type Service struct {
	mu    sync.RWMutex
	zones map[string]*hostedZone // zoneID -> zone
	tags  map[string]map[string]string
}

type hostedZone struct {
	id        string
	name      string
	callerRef string
	comment   string
	private   bool
	createdAt time.Time
	rrsets    map[string]*rrset // key = name+"|"+type -> rrset
}

type rrset struct {
	name    string
	rrType  string
	ttl     int64
	records []string
	// alias support
	aliasTarget string
	aliasZoneID string
}

func New() *Service {
	return &Service{
		zones: make(map[string]*hostedZone),
		tags:  make(map[string]map[string]string),
	}
}

func (s *Service) Name() string { return "route53" }

// Reset clears all in-memory state.
func (s *Service) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.zones = map[string]*hostedZone{}
	s.tags = map[string]map[string]string{}
}

func (s *Service) Detect(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/2013-04-01/")
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimRight(r.URL.Path, "/")

	switch {
	// GET/POST /2013-04-01/hostedzone
	case path == "/2013-04-01/hostedzone" && r.Method == http.MethodPost:
		s.createHostedZone(w, r)
	case path == "/2013-04-01/hostedzone" && r.Method == http.MethodGet:
		s.listHostedZones(w)

	// /2013-04-01/hostedzone/{Id}
	case strings.HasPrefix(path, "/2013-04-01/hostedzone/") && !strings.Contains(path[len("/2013-04-01/hostedzone/"):], "/"):
		zoneID := zoneIDFromPath(path)
		switch r.Method {
		case http.MethodGet:
			s.getHostedZone(w, zoneID)
		case http.MethodDelete:
			s.deleteHostedZone(w, zoneID)
		default:
			xmlError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		}

	// /2013-04-01/hostedzone/{Id}/rrset
	case strings.HasSuffix(path, "/rrset"):
		zoneID := zoneIDFromPath(strings.TrimSuffix(path, "/rrset"))
		switch r.Method {
		case http.MethodPost:
			s.changeResourceRecordSets(w, r, zoneID)
		case http.MethodGet:
			s.listResourceRecordSets(w, zoneID)
		default:
			xmlError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		}

	// /2013-04-01/change/{Id}
	case strings.HasPrefix(path, "/2013-04-01/change/"):
		s.getChange(w, r)

	// /2013-04-01/tags/hostedzone/{Id}
	case strings.HasPrefix(path, "/2013-04-01/tags/hostedzone/"):
		zoneID := strings.TrimPrefix(path, "/2013-04-01/tags/hostedzone/")
		switch r.Method {
		case http.MethodGet:
			s.listTagsForResource(w, zoneID)
		case http.MethodPost:
			s.changeTagsForResource(w, r, zoneID)
		}

	// /2013-04-01/hostedzonecount
	case path == "/2013-04-01/hostedzonecount":
		s.mu.RLock()
		n := len(s.zones)
		s.mu.RUnlock()
		writeXML(w, http.StatusOK, fmt.Sprintf(
			`<GetHostedZoneCountResponse xmlns=%q><HostedZoneCount>%d</HostedZoneCount></GetHostedZoneCountResponse>`,
			r53NS, n))

	default:
		xmlError(w, http.StatusBadRequest, "InvalidAction", "unknown Route 53 path: "+path)
	}
}

// ── CreateHostedZone ─────────────────────────────────────────────────────────

type createZoneRequest struct {
	Name   string `xml:"Name"`
	Config struct {
		Comment     string `xml:"Comment"`
		PrivateZone bool   `xml:"PrivateZone"`
	} `xml:"HostedZoneConfig"`
	CallerReference string `xml:"CallerReference"`
}

func (s *Service) createHostedZone(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req createZoneRequest
	xml.Unmarshal(body, &req)

	name := normalizeName(req.Name)
	id := "Z" + strings.ToUpper(strings.ReplaceAll(uid.New(), "-", "")[:12])
	arn := fmt.Sprintf("arn:aws:route53:::hostedzone/%s", id)

	zone := &hostedZone{
		id:        id,
		name:      name,
		callerRef: req.CallerReference,
		comment:   req.Config.Comment,
		private:   req.Config.PrivateZone,
		createdAt: time.Now(),
		rrsets:    make(map[string]*rrset),
	}

	s.mu.Lock()
	s.zones[id] = zone
	s.tags[id] = make(map[string]string)
	s.mu.Unlock()

	_ = arn
	w.Header().Set("Location", "/2013-04-01/hostedzone/"+id)
	writeXML(w, http.StatusCreated, fmt.Sprintf(
		`<CreateHostedZoneResponse xmlns=%q>%s%s%s</CreateHostedZoneResponse>`,
		r53NS, zoneXML(zone), changeInfoXML(), delegationSetXML()))
}

// ── GetHostedZone ─────────────────────────────────────────────────────────────

func (s *Service) getHostedZone(w http.ResponseWriter, zoneID string) {
	s.mu.RLock()
	zone, ok := s.zones[zoneID]
	s.mu.RUnlock()
	if !ok {
		xmlError(w, http.StatusNotFound, "NoSuchHostedZone", "No hosted zone found with ID: "+zoneID)
		return
	}
	writeXML(w, http.StatusOK, fmt.Sprintf(
		`<GetHostedZoneResponse xmlns=%q>%s%s</GetHostedZoneResponse>`,
		r53NS, zoneXML(zone), delegationSetXML()))
}

// ── ListHostedZones ───────────────────────────────────────────────────────────

func (s *Service) listHostedZones(w http.ResponseWriter) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var items strings.Builder
	for _, zone := range s.zones {
		items.WriteString(zoneXML(zone))
	}
	writeXML(w, http.StatusOK, fmt.Sprintf(
		`<ListHostedZonesResponse xmlns=%q><HostedZones>%s</HostedZones><IsTruncated>false</IsTruncated><MaxItems>100</MaxItems></ListHostedZonesResponse>`,
		r53NS, items.String()))
}

// ── DeleteHostedZone ──────────────────────────────────────────────────────────

func (s *Service) deleteHostedZone(w http.ResponseWriter, zoneID string) {
	s.mu.Lock()
	_, ok := s.zones[zoneID]
	if ok {
		delete(s.zones, zoneID)
		delete(s.tags, zoneID)
	}
	s.mu.Unlock()
	if !ok {
		xmlError(w, http.StatusNotFound, "NoSuchHostedZone", "No hosted zone found with ID: "+zoneID)
		return
	}
	writeXML(w, http.StatusOK, fmt.Sprintf(
		`<DeleteHostedZoneResponse xmlns=%q>%s</DeleteHostedZoneResponse>`,
		r53NS, changeInfoXML()))
}

// ── GetChange ────────────────────────────────────────────────────────────────

func (s *Service) getChange(w http.ResponseWriter, r *http.Request) {
	writeXML(w, http.StatusOK, fmt.Sprintf(
		`<GetChangeResponse xmlns=%q>%s</GetChangeResponse>`,
		r53NS, changeInfoXML()))
}

// ── ChangeResourceRecordSets ─────────────────────────────────────────────────

type changeRRSetsRequest struct {
	Changes []struct {
		Action string `xml:"Action"`
		RRSet  struct {
			Name    string `xml:"Name"`
			Type    string `xml:"Type"`
			TTL     int64  `xml:"TTL"`
			Records []struct {
				Value string `xml:"Value"`
			} `xml:"ResourceRecords>ResourceRecord"`
			AliasTarget struct {
				DNSName              string `xml:"DNSName"`
				HostedZoneID         string `xml:"HostedZoneId"`
				EvaluateTargetHealth bool   `xml:"EvaluateTargetHealth"`
			} `xml:"AliasTarget"`
		} `xml:"ResourceRecordSet"`
	} `xml:"ChangeBatch>Changes>Change"`
}

func (s *Service) changeResourceRecordSets(w http.ResponseWriter, r *http.Request, zoneID string) {
	s.mu.Lock()
	zone, ok := s.zones[zoneID]
	s.mu.Unlock()
	if !ok {
		xmlError(w, http.StatusNotFound, "NoSuchHostedZone", "No hosted zone found with ID: "+zoneID)
		return
	}

	body, _ := io.ReadAll(r.Body)
	var req changeRRSetsRequest
	xml.Unmarshal(body, &req)

	s.mu.Lock()
	for _, change := range req.Changes {
		key := normalizeName(change.RRSet.Name) + "|" + change.RRSet.Type
		switch strings.ToUpper(change.Action) {
		case "CREATE", "UPSERT":
			records := make([]string, 0, len(change.RRSet.Records))
			for _, rec := range change.RRSet.Records {
				records = append(records, rec.Value)
			}
			zone.rrsets[key] = &rrset{
				name:        normalizeName(change.RRSet.Name),
				rrType:      change.RRSet.Type,
				ttl:         change.RRSet.TTL,
				records:     records,
				aliasTarget: change.RRSet.AliasTarget.DNSName,
				aliasZoneID: change.RRSet.AliasTarget.HostedZoneID,
			}
		case "DELETE":
			delete(zone.rrsets, key)
		}
	}
	s.mu.Unlock()

	writeXML(w, http.StatusOK, fmt.Sprintf(
		`<ChangeResourceRecordSetsResponse xmlns=%q>%s</ChangeResourceRecordSetsResponse>`,
		r53NS, changeInfoXML()))
}

// ── ListResourceRecordSets ────────────────────────────────────────────────────

func (s *Service) listResourceRecordSets(w http.ResponseWriter, zoneID string) {
	s.mu.RLock()
	zone, ok := s.zones[zoneID]
	s.mu.RUnlock()
	if !ok {
		xmlError(w, http.StatusNotFound, "NoSuchHostedZone", "No hosted zone found with ID: "+zoneID)
		return
	}

	s.mu.RLock()
	var items strings.Builder
	for _, rr := range zone.rrsets {
		items.WriteString(rrsetXML(rr))
	}
	s.mu.RUnlock()

	writeXML(w, http.StatusOK, fmt.Sprintf(
		`<ListResourceRecordSetsResponse xmlns=%q><ResourceRecordSets>%s</ResourceRecordSets><IsTruncated>false</IsTruncated><MaxRRSets>300</MaxRRSets></ListResourceRecordSetsResponse>`,
		r53NS, items.String()))
}

// ── Tags ──────────────────────────────────────────────────────────────────────

type changeTagsRequest struct {
	AddTags []struct {
		Key   string `xml:"Key"`
		Value string `xml:"Value"`
	} `xml:"AddTags>Tag"`
	RemoveTagKeys []string `xml:"RemoveTagKeys>Key"`
}

func (s *Service) listTagsForResource(w http.ResponseWriter, zoneID string) {
	s.mu.RLock()
	tagMap := s.tags[zoneID]
	s.mu.RUnlock()
	var items strings.Builder
	for k, v := range tagMap {
		items.WriteString(fmt.Sprintf("<Tag><Key>%s</Key><Value>%s</Value></Tag>",
			xmlEscape(k), xmlEscape(v)))
	}
	writeXML(w, http.StatusOK, fmt.Sprintf(
		`<ListTagsForResourceResponse xmlns=%q><ResourceTagSet><ResourceType>hostedzone</ResourceType><ResourceId>%s</ResourceId><Tags>%s</Tags></ResourceTagSet></ListTagsForResourceResponse>`,
		r53NS, zoneID, items.String()))
}

func (s *Service) changeTagsForResource(w http.ResponseWriter, r *http.Request, zoneID string) {
	body, _ := io.ReadAll(r.Body)
	var req changeTagsRequest
	xml.Unmarshal(body, &req)

	s.mu.Lock()
	if s.tags[zoneID] == nil {
		s.tags[zoneID] = make(map[string]string)
	}
	for _, tag := range req.AddTags {
		s.tags[zoneID][tag.Key] = tag.Value
	}
	for _, key := range req.RemoveTagKeys {
		delete(s.tags[zoneID], key)
	}
	s.mu.Unlock()

	writeXML(w, http.StatusOK, fmt.Sprintf(
		`<ChangeTagsForResourceResponse xmlns=%q/></ChangeTagsForResourceResponse>`,
		r53NS))
}

// ── XML helpers ───────────────────────────────────────────────────────────────

func zoneXML(z *hostedZone) string {
	return fmt.Sprintf(
		`<HostedZone><Id>/hostedzone/%s</Id><Name>%s</Name><CallerReference>%s</CallerReference>`+
			`<Config><Comment>%s</Comment><PrivateZone>%v</PrivateZone></Config>`+
			`<ResourceRecordSetCount>%d</ResourceRecordSetCount></HostedZone>`,
		z.id, xmlEscape(z.name), xmlEscape(z.callerRef), xmlEscape(z.comment), z.private, len(z.rrsets))
}

func rrsetXML(rr *rrset) string {
	var inner strings.Builder
	if rr.aliasTarget != "" {
		inner.WriteString(fmt.Sprintf(
			`<AliasTarget><HostedZoneId>%s</HostedZoneId><DNSName>%s</DNSName><EvaluateTargetHealth>false</EvaluateTargetHealth></AliasTarget>`,
			xmlEscape(rr.aliasZoneID), xmlEscape(rr.aliasTarget)))
	} else {
		inner.WriteString(fmt.Sprintf("<TTL>%d</TTL><ResourceRecords>", rr.ttl))
		for _, v := range rr.records {
			inner.WriteString(fmt.Sprintf("<ResourceRecord><Value>%s</Value></ResourceRecord>", xmlEscape(v)))
		}
		inner.WriteString("</ResourceRecords>")
	}
	return fmt.Sprintf("<ResourceRecordSet><Name>%s</Name><Type>%s</Type>%s</ResourceRecordSet>",
		xmlEscape(rr.name), rr.rrType, inner.String())
}

func changeInfoXML() string {
	return fmt.Sprintf(
		`<ChangeInfo><Id>/change/%s</Id><Status>INSYNC</Status><SubmittedAt>%s</SubmittedAt></ChangeInfo>`,
		strings.ToUpper(uid.New()[:12]), time.Now().UTC().Format(time.RFC3339))
}

func delegationSetXML() string {
	return `<DelegationSet><NameServers>` +
		`<NameServer>ns1.nimbus.local</NameServer>` +
		`<NameServer>ns2.nimbus.local</NameServer>` +
		`</NameServers></DelegationSet>`
}

func writeXML(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(status)
	fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>`)
	fmt.Fprint(w, body)
}

func xmlError(w http.ResponseWriter, status int, code, msg string) {
	writeXML(w, status, fmt.Sprintf(
		`<ErrorResponse xmlns=%q><Error><Type>Sender</Type><Code>%s</Code><Message>%s</Message></Error></ErrorResponse>`,
		r53NS, code, xmlEscape(msg)))
}

func zoneIDFromPath(path string) string {
	// extract last path segment, strip /hostedzone/ prefix if present
	parts := strings.Split(strings.TrimSuffix(path, "/"), "/")
	id := parts[len(parts)-1]
	return strings.TrimPrefix(id, "/hostedzone/")
}

func normalizeName(name string) string {
	if name != "" && !strings.HasSuffix(name, ".") {
		return name + "."
	}
	return name
}

func xmlEscape(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return b.String()
}
