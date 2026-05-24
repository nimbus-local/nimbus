# S3 Control

Stub emulator for the AWS S3 Control management plane. S3 Control handles account-level S3 operations that use `/v20180820/` paths and the `x-amz-account-id` request header — these are distinct from the regular S3 API. The Pulumi AWS provider v7 calls `s3control:PutTags` / `DeleteTags` after every S3 bucket create/update to reconcile resource tags; this stub accepts those calls as no-ops so the provider does not error.

**Detection:** `x-amz-account-id` request header present, or path prefix `/v20180820/`

## Supported operations

| Operation | Notes |
|-----------|-------|
| `ListTagsForResource` | GET `/v20180820/tags/{arn}` — returns empty tag list |
| `PutTags` | PUT `/v20180820/tags/{arn}` — accepted; no-op |
| `DeleteTags` | DELETE `/v20180820/tags/{arn}` — accepted; returns 204 |
| `GetPublicAccessBlockConfiguration` | GET `/v20180820/configuration/publicAccessBlock` — returns all-blocked defaults |
| `PutPublicAccessBlockConfiguration` | PUT `/v20180820/configuration/publicAccessBlock` — accepted; no-op |
| `DeletePublicAccessBlockConfiguration` | DELETE `/v20180820/configuration/publicAccessBlock` — accepted; no-op |

Any other `/v20180820/` path returns 200 OK.

## Example

```bash
# List tags on an S3 bucket via S3 Control
nimbuslocal s3control list-tags-for-resource \
  --account-id 000000000000 \
  --resource-arn arn:aws:s3:::my-bucket

# Get account-level public access block configuration
nimbuslocal s3control get-public-access-block \
  --account-id 000000000000
```
