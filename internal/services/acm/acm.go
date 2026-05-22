// Package acm emulates the AWS Certificate Manager (ACM) control plane.
// RequestCertificate generates a real self-signed certificate using crypto/x509.
// Certificates are returned as ISSUED immediately — no DNS or email validation challenge.
// Nothing is forwarded to AWS.
package acm

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nimbus-local/nimbus/internal/uid"
)

const accountID = "000000000000"

// Service implements the AWS ACM control plane.
type Service struct {
	mu     sync.RWMutex
	certs  map[string]*certificate
	tags   map[string]map[string]string
	region string
}

type certificate struct {
	arn       string
	domain    string
	sans      []string
	certPEM   string
	keyPEM    string
	createdAt time.Time
}

func New(region string) *Service {
	return &Service{
		certs:  make(map[string]*certificate),
		tags:   make(map[string]map[string]string),
		region: region,
	}
}

func (s *Service) Name() string { return "acm" }

func (s *Service) Detect(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), "CertificateManager.")
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	target := r.Header.Get("X-Amz-Target")
	action := ""
	if idx := strings.LastIndex(target, "."); idx != -1 {
		action = target[idx+1:]
	}

	var body map[string]interface{}
	_ = json.NewDecoder(r.Body).Decode(&body)

	switch action {
	case "RequestCertificate":
		s.requestCertificate(w, body)
	case "DescribeCertificate":
		s.describeCertificate(w, body)
	case "GetCertificate":
		s.getCertificate(w, body)
	case "ListCertificates":
		s.listCertificates(w)
	case "DeleteCertificate":
		s.deleteCertificate(w, body)
	case "AddTagsToCertificate":
		s.addTags(w, body)
	case "RemoveTagsFromCertificate":
		s.removeTags(w, body)
	case "ListTagsForCertificate":
		s.listTags(w, body)
	case "RenewCertificate", "ExportCertificate":
		jsonWrite(w, http.StatusOK, map[string]interface{}{})
	default:
		jsonError(w, http.StatusBadRequest, "InvalidAction", "unknown action: "+action)
	}
}

func (s *Service) requestCertificate(w http.ResponseWriter, body map[string]interface{}) {
	domain, _ := body["DomainName"].(string)
	if domain == "" {
		jsonError(w, http.StatusBadRequest, "InvalidParameter", "DomainName required")
		return
	}

	sans := []string{domain}
	if altNames, ok := body["SubjectAlternativeNames"].([]interface{}); ok {
		for _, raw := range altNames {
			if san, ok := raw.(string); ok && san != domain {
				sans = append(sans, san)
			}
		}
	}

	certPEM, keyPEM, err := generateSelfSigned(domain, sans)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "InternalFailure", err.Error())
		return
	}

	id := uid.New()
	arn := fmt.Sprintf("arn:aws:acm:%s:%s:certificate/%s", s.region, accountID, id)
	cert := &certificate{
		arn:       arn,
		domain:    domain,
		sans:      sans,
		certPEM:   certPEM,
		keyPEM:    keyPEM,
		createdAt: time.Now(),
	}

	s.mu.Lock()
	s.certs[arn] = cert
	if tagsRaw, ok := body["Tags"].([]interface{}); ok {
		tagMap := make(map[string]string)
		for _, t := range tagsRaw {
			if tag, ok := t.(map[string]interface{}); ok {
				k, _ := tag["Key"].(string)
				v, _ := tag["Value"].(string)
				if k != "" {
					tagMap[k] = v
				}
			}
		}
		s.tags[arn] = tagMap
	}
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{
		"CertificateArn": arn,
	})
}

func (s *Service) describeCertificate(w http.ResponseWriter, body map[string]interface{}) {
	arn, _ := body["CertificateArn"].(string)
	s.mu.RLock()
	cert, ok := s.certs[arn]
	s.mu.RUnlock()
	if !ok {
		jsonError(w, http.StatusBadRequest, "ResourceNotFoundException", "Certificate not found: "+arn)
		return
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{
		"Certificate": certDetails(cert),
	})
}

func (s *Service) getCertificate(w http.ResponseWriter, body map[string]interface{}) {
	arn, _ := body["CertificateArn"].(string)
	s.mu.RLock()
	cert, ok := s.certs[arn]
	s.mu.RUnlock()
	if !ok {
		jsonError(w, http.StatusBadRequest, "ResourceNotFoundException", "Certificate not found: "+arn)
		return
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{
		"Certificate":      cert.certPEM,
		"CertificateChain": cert.certPEM,
	})
}

func (s *Service) listCertificates(w http.ResponseWriter) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	summaries := make([]map[string]interface{}, 0, len(s.certs))
	for _, cert := range s.certs {
		summaries = append(summaries, map[string]interface{}{
			"CertificateArn": cert.arn,
			"DomainName":     cert.domain,
			"Status":         "ISSUED",
		})
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{
		"CertificateSummaryList": summaries,
	})
}

func (s *Service) deleteCertificate(w http.ResponseWriter, body map[string]interface{}) {
	arn, _ := body["CertificateArn"].(string)
	s.mu.Lock()
	_, ok := s.certs[arn]
	if ok {
		delete(s.certs, arn)
		delete(s.tags, arn)
	}
	s.mu.Unlock()
	if !ok {
		jsonError(w, http.StatusBadRequest, "ResourceNotFoundException", "Certificate not found: "+arn)
		return
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) addTags(w http.ResponseWriter, body map[string]interface{}) {
	arn, _ := body["CertificateArn"].(string)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.certs[arn]; !ok {
		jsonError(w, http.StatusBadRequest, "ResourceNotFoundException", "Certificate not found: "+arn)
		return
	}
	if s.tags[arn] == nil {
		s.tags[arn] = make(map[string]string)
	}
	if tagsRaw, ok := body["Tags"].([]interface{}); ok {
		for _, t := range tagsRaw {
			if tag, ok := t.(map[string]interface{}); ok {
				k, _ := tag["Key"].(string)
				v, _ := tag["Value"].(string)
				if k != "" {
					s.tags[arn][k] = v
				}
			}
		}
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) removeTags(w http.ResponseWriter, body map[string]interface{}) {
	arn, _ := body["CertificateArn"].(string)
	s.mu.Lock()
	defer s.mu.Unlock()
	if tagsRaw, ok := body["Tags"].([]interface{}); ok {
		for _, t := range tagsRaw {
			if tag, ok := t.(map[string]interface{}); ok {
				k, _ := tag["Key"].(string)
				delete(s.tags[arn], k)
			}
		}
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) listTags(w http.ResponseWriter, body map[string]interface{}) {
	arn, _ := body["CertificateArn"].(string)
	s.mu.RLock()
	tagMap := s.tags[arn]
	s.mu.RUnlock()
	tags := make([]map[string]string, 0, len(tagMap))
	for k, v := range tagMap {
		tags = append(tags, map[string]string{"Key": k, "Value": v})
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{"Tags": tags})
}

// CertHandler serves GET /_nimbus/acm/certs/{arn} — downloads the PEM cert.
func (s *Service) CertHandler(w http.ResponseWriter, r *http.Request) {
	// arn is URL-encoded in the path suffix; last path segment is the cert UUID
	// but we match by looking up all certs whose ARN ends with the path suffix
	suffix := strings.TrimPrefix(r.URL.Path, "/_nimbus/acm/certs/")
	s.mu.RLock()
	var cert *certificate
	for arn, c := range s.certs {
		if strings.HasSuffix(arn, suffix) || arn == suffix {
			cert = c
			break
		}
	}
	s.mu.RUnlock()
	if cert == nil {
		http.Error(w, "certificate not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	fmt.Fprint(w, cert.certPEM)
}

func certDetails(cert *certificate) map[string]interface{} {
	domainValidations := make([]map[string]interface{}, 0, len(cert.sans))
	for _, san := range cert.sans {
		domainValidations = append(domainValidations, map[string]interface{}{
			"DomainName":       san,
			"ValidationStatus": "SUCCESS",
			"ValidationMethod": "DNS",
			"ResourceRecord": map[string]interface{}{
				"Name":  "_nimbus." + san,
				"Type":  "CNAME",
				"Value": "nimbus.local.",
			},
		})
	}
	return map[string]interface{}{
		"CertificateArn":          cert.arn,
		"DomainName":              cert.domain,
		"SubjectAlternativeNames": cert.sans,
		"Status":                  "ISSUED",
		"Type":                    "AMAZON_ISSUED",
		"KeyAlgorithm":            "RSA_2048",
		"SignatureAlgorithm":      "SHA256WITHRSA",
		"DomainValidationOptions": domainValidations,
		"NotBefore":               cert.createdAt.Unix(),
		"NotAfter":                cert.createdAt.Add(365 * 24 * time.Hour).Unix(),
		"IssuedAt":                cert.createdAt.Unix(),
		"CreatedAt":               cert.createdAt.Unix(),
		"InUseBy":                 []string{},
		"RenewalEligibility":      "INELIGIBLE",
		"Options": map[string]interface{}{
			"CertificateTransparencyLoggingPreference": "ENABLED",
		},
	}
}

func generateSelfSigned(domain string, sans []string) (certPEM, keyPEM string, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", err
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", err
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   domain,
			Organization: []string{"Nimbus Local"},
		},
		DNSNames:    sans,
		NotBefore:   time.Now().Add(-time.Hour),
		NotAfter:    time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return "", "", err
	}

	var certBuf, keyBuf bytes.Buffer
	if err = pem.Encode(&certBuf, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		return "", "", err
	}
	if err = pem.Encode(&keyBuf, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}); err != nil {
		return "", "", err
	}

	return certBuf.String(), keyBuf.String(), nil
}

func jsonWrite(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"__type":  code,
		"message": message,
	})
}
