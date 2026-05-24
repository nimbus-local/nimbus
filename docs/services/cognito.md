# Cognito (User Pools)

In-memory Cognito User Pools emulator. Supports the full infrastructure lifecycle needed for `terraform apply` — user pool and client CRUD with tags. Nothing is forwarded to AWS. Authentication flows (JWT issuance, sign-in, user management) are added in later phases.

**Detection:** `X-Amz-Target: AWSCognitoIdentityProviderService.*`

## Supported operations

| Operation | Notable behaviour |
|-----------|-------------------|
| CreateUserPool | Stores pool in-memory; ID format `{region}_{8-char-hex}`; returns `Active` immediately |
| DescribeUserPool | Returns full pool detail including ARN, tags, MFA config |
| UpdateUserPool | Updates tags, MFA config, auto-verified attributes |
| DeleteUserPool | Removes pool and cascade-deletes all its clients |
| ListUserPools | Returns all pools; pagination tokens accepted but not enforced |
| CreateUserPoolClient | Creates client scoped to a pool; `GenerateSecret=true` generates a random secret |
| DescribeUserPoolClient | Returns client detail; 400 if pool/client mismatch |
| UpdateUserPoolClient | Updates name, auth flows, callback/logout URLs, OAuth config |
| DeleteUserPoolClient | Removes client |
| ListUserPoolClients | Returns clients for a given pool ID |
| ListTagsForResource | Returns tags for a user pool by ARN |
| TagResource | Adds/updates tags on a user pool |
| UntagResource | Removes tags by key |

## Example

```bash
# Create a user pool
nimbuslocal cognito-idp create-user-pool \
  --pool-name my-app-pool

# Create a client for the pool
nimbuslocal cognito-idp create-user-pool-client \
  --user-pool-id us-east-1_abc12345 \
  --client-name web-client \
  --explicit-auth-flows ALLOW_USER_PASSWORD_AUTH ALLOW_REFRESH_TOKEN_AUTH

# Describe the pool
nimbuslocal cognito-idp describe-user-pool \
  --user-pool-id us-east-1_abc12345

# List all pools
nimbuslocal cognito-idp list-user-pools --max-results 10

# Delete the pool (also deletes all its clients)
nimbuslocal cognito-idp delete-user-pool \
  --user-pool-id us-east-1_abc12345
```
