# IAM

In-memory IAM + STS emulator. Roles, policies, and instance profiles are stored locally — nothing is sent to AWS. No policy enforcement: any `AssumeRole` call succeeds and returns fake credentials. The goal is Terraform `plan`/`apply` succeeds and ARNs are returned correctly.

**Detection:** `Content-Type: application/x-www-form-urlencoded` with `Version=2010-05-08` (IAM) or `Version=2011-06-15` (STS) in the request body.

## Supported operations

| Operation | Notes |
|-----------|-------|
| `CreateRole` | Stores role with trust policy document |
| `GetRole` | Returns role by name |
| `DeleteRole` | Removes role from store |
| `ListRoles` | Returns all roles |
| `AttachRolePolicy` | Records policy ARN on role; accepts AWS-managed ARNs |
| `DetachRolePolicy` | Removes attachment |
| `ListAttachedRolePolicies` | Returns recorded attachments |
| `PutRolePolicy` | Stores inline policy document on role |
| `GetRolePolicy` | Returns inline policy document |
| `DeleteRolePolicy` | Removes inline policy |
| `ListRolePolicies` | Returns inline policy names |
| `CreatePolicy` | Creates customer-managed policy |
| `GetPolicy` | Returns policy; stubs AWS-managed ARNs |
| `GetPolicyVersion` | Returns stored policy document as v1 |
| `DeletePolicy` | Removes policy |
| `ListPolicies` | Returns customer-managed policies |
| `CreateInstanceProfile` | Creates instance profile |
| `GetInstanceProfile` | Returns profile with attached role |
| `DeleteInstanceProfile` | Removes instance profile |
| `ListInstanceProfiles` | Returns all instance profiles |
| `AddRoleToInstanceProfile` | Attaches role to profile |
| `RemoveRoleFromInstanceProfile` | Detaches role from profile |
| `AssumeRole` | Returns fake credentials — no enforcement |
| `GetCallerIdentity` (STS) | Returns account `000000000000` |

## Example

```bash
nimbuslocal iam create-role \
  --role-name my-role \
  --assume-role-policy-document '{"Version":"2012-10-17","Statement":[]}'

nimbuslocal iam get-role --role-name my-role

nimbuslocal sts get-caller-identity

nimbuslocal sts assume-role \
  --role-arn arn:aws:iam::000000000000:role/my-role \
  --role-session-name dev
```
