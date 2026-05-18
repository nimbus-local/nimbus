# SSM Parameter Store

In-memory SSM Parameter Store emulator.

## Supported operations

| Operation | Notes |
|-----------|-------|
| PutParameter | String, StringList, SecureString |
| GetParameter | |
| GetParameters | Up to 10 names |
| GetParametersByPath | Supports `Recursive`, `WithDecryption` |
| DeleteParameter | |
| DeleteParameters | |
| DescribeParameters | |

Detection: `X-Amz-Target: AmazonSSM.*`.

Supports path hierarchy and versioning. SecureString values are stored in plaintext locally (no KMS).

## Example

```bash
nimbuslocal ssm put-parameter \
  --name /myapp/db-host \
  --value localhost \
  --type String

nimbuslocal ssm get-parameter --name /myapp/db-host

nimbuslocal ssm get-parameters-by-path \
  --path /myapp \
  --recursive
```
