// Package cloudfront emulates the AWS CloudFront distribution control plane.
// All state is in-memory. Distributions are never actually provisioned —
// DomainName is localhost-based and Status is always Deployed.
package cloudfront

import (
	"bytes"
	"encoding/json"
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
	xmlHeader = `<?xml version="1.0" encoding="UTF-8"?>`
	accountID = "000000000000"
	cfNS      = "http://cloudfront.amazonaws.com/doc/2020-05-31/"
)

// Service implements the CloudFront distribution control plane.
type Service struct {
	mu            sync.RWMutex
	distributions map[string]*dist
	region        string
}

type dist struct {
	id           string
	arn          string
	etag         string
	domain       string
	createdAt    time.Time
	enabled      bool
	comment      string
	rawConfigXML []byte // full <DistributionConfig> element bytes from create/update
}

func New(region string) *Service {
	if region == "" {
		region = "us-east-1"
	}
	return &Service{
		region:        region,
		distributions: map[string]*dist{},
	}
}

func (s *Service) Name() string { return "cloudfront" }

// Detect claims CloudFront REST API requests by path prefix /2020-05-31/.
func (s *Service) Detect(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/2020-05-31/")
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case r.Method == http.MethodPost && p == "/2020-05-31/distribution":
		s.createDistribution(w, r)
	case r.Method == http.MethodGet && p == "/2020-05-31/distribution":
		s.listDistributions(w, r)
	case r.Method == http.MethodGet && p == "/2020-05-31/tagging":
		s.listTagsForResource(w, r)
	case r.Method == http.MethodPost && p == "/2020-05-31/tagging":
		s.addTagsToResource(w, r)
	case r.Method == http.MethodGet && isDistPath(p):
		s.getDistribution(w, r)
	case r.Method == http.MethodPut && strings.HasSuffix(p, "/config"):
		s.updateDistribution(w, r)
	case r.Method == http.MethodDelete && isDistPath(p):
		s.deleteDistribution(w, r)
	default:
		http.Error(w, "not implemented", http.StatusNotImplemented)
	}
}

// DistributionsHandler serves GET /_nimbus/cloudfront/distributions.
func (s *Service) DistributionsHandler(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	type row struct {
		ID        string    `json:"id"`
		Domain    string    `json:"domain"`
		Enabled   bool      `json:"enabled"`
		Comment   string    `json:"comment,omitempty"`
		CreatedAt time.Time `json:"createdAt"`
	}

	rows := make([]row, 0, len(s.distributions))
	for _, d := range s.distributions {
		rows = append(rows, row{
			ID:        d.id,
			Domain:    d.domain,
			Enabled:   d.enabled,
			Comment:   d.comment,
			CreatedAt: d.createdAt,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rows)
}

// --- operations ---

func (s *Service) createDistribution(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cfg := parseConfig(body)
	id := distID()
	etag := uid.New()
	d := &dist{
		id:           id,
		arn:          fmt.Sprintf("arn:aws:cloudfront::%s:distribution/%s", accountID, id),
		etag:         etag,
		domain:       fmt.Sprintf("%s.cloudfront.localhost", id),
		createdAt:    time.Now().UTC(),
		enabled:      cfg.enabled,
		comment:      cfg.comment,
		rawConfigXML: normalizeDistConfig(body),
	}
	s.mu.Lock()
	s.distributions[id] = d
	s.mu.Unlock()

	w.Header().Set("Content-Type", "text/xml")
	w.Header().Set("ETag", etag)
	w.Header().Set("Location", fmt.Sprintf("/2020-05-31/distribution/%s", id))
	w.WriteHeader(http.StatusCreated)
	writeDistXML(w, d)
}

func (s *Service) getDistribution(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/2020-05-31/distribution/")
	s.mu.RLock()
	d, ok := s.distributions[id]
	s.mu.RUnlock()
	if !ok {
		cfError(w, http.StatusNotFound, "NoSuchDistribution", "The specified distribution does not exist.")
		return
	}
	w.Header().Set("Content-Type", "text/xml")
	w.Header().Set("ETag", d.etag)
	writeDistXML(w, d)
}

func (s *Service) updateDistribution(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/2020-05-31/distribution/")
	id := strings.TrimSuffix(trimmed, "/config")

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cfg := parseConfig(body)

	s.mu.Lock()
	d, ok := s.distributions[id]
	if !ok {
		s.mu.Unlock()
		cfError(w, http.StatusNotFound, "NoSuchDistribution", "The specified distribution does not exist.")
		return
	}
	d.enabled = cfg.enabled
	d.comment = cfg.comment
	d.etag = uid.New()
	d.rawConfigXML = normalizeDistConfig(body)
	s.mu.Unlock()

	w.Header().Set("Content-Type", "text/xml")
	w.Header().Set("ETag", d.etag)
	writeDistXML(w, d)
}

func (s *Service) deleteDistribution(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/2020-05-31/distribution/")
	s.mu.Lock()
	_, ok := s.distributions[id]
	if !ok {
		s.mu.Unlock()
		cfError(w, http.StatusNotFound, "NoSuchDistribution", "The specified distribution does not exist.")
		return
	}
	delete(s.distributions, id)
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// listTagsForResource handles GET /2020-05-31/tagging?Resource=<arn>.
// The v6 provider calls this after every read to sync tags.
func (s *Service) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/xml")
	fmt.Fprint(w, xmlHeader)
	fmt.Fprintf(w, `<Tagging xmlns="%s"><Tags><Quantity>0</Quantity><Items/></Tags></Tagging>`, cfNS)
}

// addTagsToResource handles POST /2020-05-31/tagging?Resource=<arn>.
func (s *Service) addTagsToResource(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) listDistributions(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	dists := make([]*dist, 0, len(s.distributions))
	for _, d := range s.distributions {
		dists = append(dists, d)
	}
	s.mu.RUnlock()

	type summary struct {
		XMLName    xml.Name `xml:"DistributionSummary"`
		Id         string   `xml:"Id"`
		ARN        string   `xml:"ARN"`
		Status     string   `xml:"Status"`
		DomainName string   `xml:"DomainName"`
		Enabled    bool     `xml:"Enabled"`
		Comment    string   `xml:"Comment"`
	}
	type list struct {
		XMLName     xml.Name  `xml:"DistributionList"`
		Xmlns       string    `xml:"xmlns,attr"`
		Quantity    int       `xml:"Quantity"`
		IsTruncated bool      `xml:"IsTruncated"`
		Items       []summary `xml:"Items>DistributionSummary"`
	}

	items := make([]summary, 0, len(dists))
	for _, d := range dists {
		items = append(items, summary{
			Id:         d.id,
			ARN:        d.arn,
			Status:     "Deployed",
			DomainName: d.domain,
			Enabled:    d.enabled,
			Comment:    d.comment,
		})
	}

	w.Header().Set("Content-Type", "text/xml")
	fmt.Fprint(w, xmlHeader)
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	_ = enc.Encode(list{
		Xmlns:       cfNS,
		Quantity:    len(items),
		IsTruncated: false,
		Items:       items,
	})
}

// --- XML helpers ---

// writeDistXML writes a full Distribution response. The stored rawConfigXML is
// the <DistributionConfig> element that the provider originally sent; we echo
// it verbatim so that any field the provider wrote is reflected back, which
// prevents nil-pointer panics in the provider's flatten logic.
func writeDistXML(w http.ResponseWriter, d *dist) {
	configXML := stripXMLDecl(d.rawConfigXML)
	// Strip the outer xmlns attr to avoid duplicate namespace declarations.
	configXML = removeNSAttr(configXML)
	// Inject elements that the provider never sends but the v6 SDK accesses
	// without nil checks (OriginGroups.Quantity panics if nil).
	configXML = injectIfAbsent(configXML, "OriginGroups",
		`<OriginGroups><Quantity>0</Quantity><Items></Items></OriginGroups>`)

	fmt.Fprint(w, xmlHeader)
	fmt.Fprintf(w, `<Distribution xmlns="%s">`, cfNS)
	fmt.Fprintf(w, `<Id>%s</Id>`, xmlEscape(d.id))
	fmt.Fprintf(w, `<ARN>%s</ARN>`, xmlEscape(d.arn))
	fmt.Fprintf(w, `<Status>Deployed</Status>`)
	fmt.Fprintf(w, `<DomainName>%s</DomainName>`, xmlEscape(d.domain))
	// LastModifiedTime must be non-nil; the v6 provider calls .String() on it directly.
	fmt.Fprintf(w, `<LastModifiedTime>%s</LastModifiedTime>`, d.createdAt.Format(time.RFC3339))
	fmt.Fprintf(w, `<InProgressInvalidationBatches>0</InProgressInvalidationBatches>`)
	fmt.Fprint(w, `<ActiveTrustedSigners><Enabled>false</Enabled><Quantity>0</Quantity></ActiveTrustedSigners>`)
	fmt.Fprint(w, `<ActiveTrustedKeyGroups><Enabled>false</Enabled><Quantity>0</Quantity></ActiveTrustedKeyGroups>`)
	fmt.Fprint(w, `<AliasICPRecordals/>`)
	w.Write(configXML) //nolint:errcheck
	fmt.Fprint(w, `</Distribution>`)
}

func xmlEscape(s string) string {
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

// stripXMLDecl removes a leading <?xml ...?> processing instruction.
func stripXMLDecl(b []byte) []byte {
	s := bytes.TrimSpace(b)
	if bytes.HasPrefix(s, []byte("<?xml")) {
		idx := bytes.Index(s, []byte("?>"))
		if idx >= 0 {
			s = bytes.TrimSpace(s[idx+2:])
		}
	}
	return s
}

// injectIfAbsent adds xmlFrag before the closing </DistributionConfig> tag
// when element <tag> is not already present in b.
func injectIfAbsent(b []byte, tag, xmlFrag string) []byte {
	if bytes.Contains(b, []byte("<"+tag)) {
		return b
	}
	closing := []byte("</DistributionConfig>")
	idx := bytes.LastIndex(b, closing)
	if idx < 0 {
		return b
	}
	result := make([]byte, 0, len(b)+len(xmlFrag))
	result = append(result, b[:idx]...)
	result = append(result, []byte(xmlFrag)...)
	result = append(result, b[idx:]...)
	return result
}

// removeNSAttr strips xmlns="..." from the opening tag of the root element so
// that the element can be nested inside the Distribution wrapper without
// declaring a redundant namespace.
func removeNSAttr(b []byte) []byte {
	s := string(b)
	// Replace 'xmlns="..."' in the first tag only.
	idx := strings.Index(s, ">")
	if idx < 0 {
		return b
	}
	header := s[:idx+1]
	// Remove xmlns attr (handles both single and double quotes).
	for _, q := range []string{`"`, `'`} {
		prefix := ` xmlns=` + q
		start := strings.Index(header, prefix)
		if start < 0 {
			continue
		}
		end := strings.Index(header[start+len(prefix):], q)
		if end < 0 {
			continue
		}
		remove := header[start : start+len(prefix)+end+1]
		header = strings.Replace(header, remove, "", 1)
		break
	}
	return []byte(header + s[idx+1:])
}

func cfError(w http.ResponseWriter, code int, errCode, msg string) {
	type cfErr struct {
		XMLName xml.Name `xml:"ErrorResponse"`
		Xmlns   string   `xml:"xmlns,attr"`
		Code    string   `xml:"Error>Code"`
		Message string   `xml:"Error>Message"`
	}
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(code)
	fmt.Fprint(w, xmlHeader)
	_ = xml.NewEncoder(w).Encode(cfErr{Xmlns: cfNS, Code: errCode, Message: msg})
}

// --- minimal config parser ---

type parsedConfig struct {
	enabled bool
	comment string
}

func parseConfig(body []byte) parsedConfig {
	var v struct {
		Enabled bool   `xml:"Enabled"`
		Comment string `xml:"Comment"`
	}
	_ = xml.Unmarshal(body, &v)
	return parsedConfig{enabled: v.Enabled, comment: v.Comment}
}

// normalizeDistConfig ensures rawConfigXML is a <DistributionConfig> element.
// The AWS provider v6 SDK wraps the create body in <DistributionConfigWithTags>
// when tags are present. The GetDistribution response must contain
// <DistributionConfig> as a direct child of <Distribution>, so we extract it.
func normalizeDistConfig(body []byte) []byte {
	s := string(body)
	if !strings.Contains(s, "DistributionConfigWithTags") {
		return body
	}
	// Find the inner <DistributionConfig> (not <DistributionConfigWithTags>).
	start := strings.Index(s, "<DistributionConfig>")
	if start < 0 {
		return body
	}
	end := strings.LastIndex(s, "</DistributionConfig>")
	if end < 0 {
		return body
	}
	return []byte(s[start : end+len("</DistributionConfig>")])
}

// isDistPath returns true for /2020-05-31/distribution/{id} with no trailing segments.
func isDistPath(p string) bool {
	trimmed := strings.TrimPrefix(p, "/2020-05-31/distribution/")
	return trimmed != "" && !strings.Contains(trimmed, "/")
}

// distID generates a CloudFront-style 14-char uppercase alphanumeric ID.
func distID() string {
	raw := strings.ToUpper(strings.ReplaceAll(uid.New(), "-", ""))
	if len(raw) > 14 {
		return raw[:14]
	}
	return raw
}
