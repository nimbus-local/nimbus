# Route 53

In-memory Route 53 emulator. Hosted zones and resource record sets are accepted and stored verbatim — no DNS resolution or validation is performed. `GetChange` always returns `INSYNC` immediately. Nothing is forwarded to AWS.

**Detection:** URL path prefix `/2013-04-01/`

## Supported operations

| Operation | Notable behaviour |
|-----------|-------------------|
| CreateHostedZone | Returns zone ID in `/hostedzone/Z{id}` format; `Location` header set |
| GetHostedZone | Returns zone config + stub delegation set (`ns1/ns2.nimbus.local`) |
| ListHostedZones | Returns all zones |
| DeleteHostedZone | Removes zone and its tags |
| GetChange | Always returns `INSYNC` |
| ChangeResourceRecordSets | `CREATE`, `UPSERT`, `DELETE` actions; alias targets stored verbatim |
| ListResourceRecordSets | Returns all record sets for the zone |
| ListTagsForResource | Returns tags for a hosted zone |
| ChangeTagsForResource | Add and remove tags |
| GetHostedZoneCount | Returns total zone count |

## Example

```bash
# Create a hosted zone
nimbuslocal route53 create-hosted-zone \
  --name myapp.nimbus.local \
  --caller-reference ref-$(date +%s)

# List zones
nimbuslocal route53 list-hosted-zones

# Create an A record
nimbuslocal route53 change-resource-record-sets \
  --hosted-zone-id /hostedzone/Z1A2B3C4D5E6 \
  --change-batch '{
    "Changes": [{
      "Action": "UPSERT",
      "ResourceRecordSet": {
        "Name": "myapp.nimbus.local",
        "Type": "A",
        "TTL": 300,
        "ResourceRecords": [{"Value": "127.0.0.1"}]
      }
    }]
  }'

# List record sets
nimbuslocal route53 list-resource-record-sets \
  --hosted-zone-id /hostedzone/Z1A2B3C4D5E6

# Get change status (always INSYNC)
nimbuslocal route53 get-change --id /change/C1A2B3C4D5E6
```
