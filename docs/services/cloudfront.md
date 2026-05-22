# CloudFront

CloudFront distribution control-plane emulator. All state is in-memory. No content is actually proxied — `DomainName` is a `localhost`-based stub and `Status` is always `Deployed`. ETags are generated per distribution and returned on create/get/update; `If-Match` headers on update and delete are accepted but not enforced.

Detection: path prefix `/2020-05-31/`

## Supported operations

| Operation | Notable behaviour |
|-----------|-------------------|
| CreateDistribution | Returns 201 + `Location` + `ETag`; `DomainName` is `{id}.cloudfront.localhost` |
| GetDistribution | Returns distribution with `Status: Deployed`; includes `ETag` header |
| UpdateDistribution | Replaces `Enabled` and `Comment` from body; returns new `ETag` |
| DeleteDistribution | Returns 204; `If-Match` accepted but not validated |
| ListDistributions | Returns all distributions wrapped in `DistributionList` |

## Inspection endpoint

| Endpoint | Description |
|----------|-------------|
| `GET /_nimbus/cloudfront/distributions` | JSON list of all distributions with ID, domain, enabled flag, and comment |

## Example

```bash
# Create a distribution
nimbuslocal cloudfront create-distribution \
  --distribution-config '{
    "CallerReference":"ref1",
    "Comment":"my-dist",
    "Enabled":true,
    "Origins":{"Quantity":1,"Items":[{"Id":"o1","DomainName":"myapp.localhost","CustomOriginConfig":{"HTTPPort":80,"HTTPSPort":443,"OriginProtocolPolicy":"http-only","OriginSSLProtocols":{"Quantity":1,"Items":["TLSv1.2"]}}}]},
    "DefaultCacheBehavior":{"TargetOriginId":"o1","ViewerProtocolPolicy":"allow-all","ForwardedValues":{"QueryString":false,"Cookies":{"Forward":"none"}},"TrustedSigners":{"Enabled":false,"Quantity":0},"MinTTL":0},
    "CacheBehaviors":{"Quantity":0},
    "CustomErrorResponses":{"Quantity":0},
    "Restrictions":{"GeoRestriction":{"RestrictionType":"none","Quantity":0}},
    "ViewerCertificate":{"CloudFrontDefaultCertificate":true}
  }'

# Get distribution
nimbuslocal cloudfront get-distribution --id <id>

# List distributions
nimbuslocal cloudfront list-distributions

# Update distribution
nimbuslocal cloudfront update-distribution --id <id> --if-match <etag> \
  --distribution-config '...'

# Delete distribution
nimbuslocal cloudfront delete-distribution --id <id> --if-match <etag>

# Inspect via Nimbus endpoint
curl http://localhost:4566/_nimbus/cloudfront/distributions
```
