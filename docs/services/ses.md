# SES

SES emulator that captures emails in memory — nothing is ever sent.

## Supported operations

| Operation |
|-----------|
| SendEmail (v1 + v2) |
| SendRawEmail |
| VerifyEmailIdentity |
| ListIdentities |
| DeleteIdentity |
| GetSendQuota |

Detection: `X-Amz-Target: AmazonSimpleEmailService.*` or `/v2/email/` path prefix.

## Inspecting captured emails

Emails are available via Nimbus-specific endpoints for use in integration tests.

**List captured emails:**
```bash
curl http://localhost:4566/_nimbus/ses/messages
```

**Clear between tests:**
```bash
curl -X DELETE http://localhost:4566/_nimbus/ses/messages
```

**Example response:**
```json
[
  {
    "MessageId": "abc123@nimbus.local",
    "From": "no-reply@myapp.com",
    "To": ["user@example.com"],
    "Subject": "Welcome to MyApp",
    "Body": {
      "Text": "Welcome!",
      "HTML": "<p>Welcome!</p>"
    },
    "SentAt": "2026-04-03T21:00:00Z"
  }
]
```

## Example

```bash
nimbuslocal ses verify-email-identity --email-address sender@example.com

nimbuslocal ses send-email \
  --from sender@example.com \
  --to user@example.com \
  --subject "Hello" \
  --text "Hello from Nimbus"
```
