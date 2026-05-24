# KMS

In-memory KMS emulator. Key material is generated locally using `crypto/rand`. `Encrypt`, `Decrypt`, `GenerateDataKey`, and `ReEncrypt` use real **AES-256-GCM** — ciphertexts produced by the emulator are genuinely decryptable within the same session. Nothing is forwarded to AWS.

Detection: `X-Amz-Target: TrentService.*`

## Supported operations

| Operation | Notable behaviour |
|-----------|-------------------|
| CreateKey | Generates a random 256-bit AES key; returns full KeyMetadata |
| DescribeKey | Resolves by key ID, ARN, or alias name |
| ListKeys | Returns all key IDs and ARNs |
| EnableKey | Sets state → Enabled |
| DisableKey | Sets state → Disabled; Encrypt/Decrypt will fail |
| ScheduleKeyDeletion | Sets state → PendingDeletion; `PendingWindowInDays` respected (default 30) |
| CancelKeyDeletion | Restores state → Disabled |
| CreateAlias | Alias name must start with `alias/` |
| DeleteAlias | Removes the alias |
| ListAliases | Optionally filtered by `KeyId` |
| UpdateAlias | Points an alias at a different key |
| TagResource | Adds/updates tags |
| UntagResource | Removes tags by key |
| ListResourceTags | Returns all tags for a key |
| Encrypt | AES-256-GCM; ciphertext embeds key ID so Decrypt needs no hint |
| Decrypt | Extracts key ID from ciphertext envelope; fails if key disabled or pending deletion |
| GenerateDataKey | Returns plaintext + encrypted data key; supports `AES_256`, `AES_128`, `NumberOfBytes` |
| GenerateDataKeyWithoutPlaintext | Encrypted data key only |
| ReEncrypt | Decrypts with source key, re-encrypts with destination key |
| GenerateRandom | Returns up to 1024 cryptographically random bytes |
| GetKeyPolicy | Returns a default allow-all policy stub |
| PutKeyPolicy | Accepted and ignored (no IAM enforcement) |
| EnableKeyRotation | Marks rotation enabled on the key; rejected if key is disabled or pending deletion |
| DisableKeyRotation | Marks rotation disabled |
| GetKeyRotationStatus | Returns `{ "KeyRotationEnabled": bool }` |
| CreateGrant | Stores a grant (grantee principal + operations); returns `GrantId` and `GrantToken` |
| ListGrants | Returns all grants for a key; optionally filtered by `GrantId` |
| RevokeGrant | Removes a grant by `GrantId` |
| RetireGrant | Removes a grant by `GrantToken` or `GrantId` |

## Ciphertext format

The `CiphertextBlob` is a base64-encoded JSON envelope:

```json
{"k":"<keyId>","n":"<base64-nonce>","d":"<base64-GCM-ciphertext>"}
```

The key ID (`k`) is used as AES-GCM additional authenticated data, binding the ciphertext to the key. Ciphertexts from one Nimbus session cannot be decrypted in another.

## Example

```bash
# Create a key
nimbuslocal kms create-key --description "my app key"

# Create an alias
nimbuslocal kms create-alias \
  --alias-name alias/my-app-key \
  --target-key-id <key-id>

# Encrypt
nimbuslocal kms encrypt \
  --key-id alias/my-app-key \
  --plaintext "hello world" \
  --output text --query CiphertextBlob

# Decrypt
nimbuslocal kms decrypt \
  --ciphertext-blob fileb://ciphertext.bin \
  --output text --query Plaintext | base64 --decode

# Generate a data key
nimbuslocal kms generate-data-key \
  --key-id alias/my-app-key \
  --key-spec AES_256
```
