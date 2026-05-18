# Secrets Manager

In-memory Secrets Manager emulator.

## Supported operations

| Operation |
|-----------|
| CreateSecret |
| GetSecretValue |
| PutSecretValue |
| UpdateSecret |
| DeleteSecret |
| ListSecrets |
| DescribeSecret |
| RestoreSecret |

Detection: `X-Amz-Target: secretsmanager.*`.

## Example

```bash
nimbuslocal secretsmanager create-secret \
  --name /myapp/db-password \
  --secret-string "supersecret"

nimbuslocal secretsmanager get-secret-value \
  --secret-id /myapp/db-password

nimbuslocal secretsmanager list-secrets
```
