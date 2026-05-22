# ACM (Certificate Manager)

In-memory ACM emulator. `RequestCertificate` generates a real self-signed certificate using Go's `crypto/x509` — RSA 2048-bit, valid for 365 days, covering the requested domain and all SANs. Certificates are returned as `ISSUED` immediately with no DNS or email validation challenge. Nothing is forwarded to AWS.

**Detection:** `X-Amz-Target: CertificateManager.*`

## Supported operations

| Operation | Notable behaviour |
|-----------|-------------------|
| RequestCertificate | Generates a real self-signed cert; returns `CertificateArn` immediately |
| DescribeCertificate | Status always `ISSUED`; DomainValidationOptions show `SUCCESS` |
| GetCertificate | Returns cert PEM + chain (self-signed, so chain = cert) |
| ListCertificates | Returns all certificates |
| DeleteCertificate | Removes cert and its tags |
| AddTagsToCertificate | Stores tags |
| RemoveTagsFromCertificate | Removes tags by key |
| ListTagsForCertificate | Returns all tags for a certificate |

## Inspection endpoint

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/_nimbus/acm/certs/{arn}` | GET | Download the PEM-encoded certificate for local trust |

The `{arn}` can be the full ARN or just the UUID suffix (`certificate/<uuid>`).

## Example

```bash
# Request a certificate
nimbuslocal acm request-certificate \
  --domain-name myapp.nimbus.local \
  --subject-alternative-names "*.myapp.nimbus.local" \
  --validation-method DNS

# Describe it (status is always ISSUED)
nimbuslocal acm describe-certificate \
  --certificate-arn arn:aws:acm:us-east-1:000000000000:certificate/<uuid>

# Download PEM for local trust
curl http://localhost:5000/_nimbus/acm/certs/arn:aws:acm:us-east-1:000000000000:certificate/<uuid> \
  > cert.pem

# List all certificates
nimbuslocal acm list-certificates

# Delete a certificate
nimbuslocal acm delete-certificate \
  --certificate-arn arn:aws:acm:us-east-1:000000000000:certificate/<uuid>
```
