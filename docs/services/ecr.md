# ECR

ECR emulator with two interfaces on the same port:

- **Management plane** — AWS JSON API (`X-Amz-Target: AmazonEC2ContainerRegistry_V20150921.*`) for repository and image metadata
- **Docker V2 registry** — standard registry protocol (`/v2/...`) for `docker push` and `docker pull`

All state is in-memory. Blobs are deduped globally by digest. Pushing a manifest to a repository that does not exist auto-creates it.

Detection: `X-Amz-Target: AmazonEC2ContainerRegistry_V20150921.*` or path prefix `/v2/` (excluding `/v2/email/` which belongs to SES).

## Supported operations

### Management plane

| Operation | Notable behaviour |
|-----------|-------------------|
| CreateRepository | Returns ARN, URI (`localhost:4566/<name>`), and standard metadata |
| DeleteRepository | Removes repository and all stored manifests |
| DescribeRepositories | Optionally filtered by `repositoryNames` list |
| ListImages | Returns image IDs (tag + digest) for a repository |
| DescribeImages | Returns digest, tags, size, pushed-at; filterable by tag or digest |
| BatchDeleteImage | Deletes by tag or digest; cascades tag removal |
| BatchGetImage | Returns raw manifest JSON by tag or digest |
| GetAuthorizationToken | Returns a fake token valid for 12 hours; `proxyEndpoint` is `http://localhost:4566` |
| TagResource / UntagResource / ListTagsForResource | Repository tags |

### Docker V2 registry

| Endpoint | Method | Notable behaviour |
|----------|--------|-------------------|
| `/v2/` | GET | Version check — always 200 |
| `/v2/<name>/blobs/uploads/` | POST | Initiates chunked upload; also accepts monolithic upload when `?digest=` is provided |
| `/v2/<name>/blobs/uploads/<uuid>` | PATCH | Appends chunk to upload session |
| `/v2/<name>/blobs/uploads/<uuid>?digest=` | PUT | Finalises upload; verifies SHA-256 digest |
| `/v2/<name>/blobs/<digest>` | GET/HEAD | Downloads blob; 404 if not found |
| `/v2/<name>/blobs/<digest>` | DELETE | Removes blob from global store |
| `/v2/<name>/manifests/<ref>` | PUT | Stores manifest; `ref` can be a tag or digest |
| `/v2/<name>/manifests/<ref>` | GET/HEAD | Returns manifest and `Docker-Content-Digest` header |
| `/v2/<name>/manifests/<ref>` | DELETE | Removes manifest and all pointing tags |
| `/v2/<name>/tags/list` | GET | Lists all tags in the repository |

## Using with Docker

Configure Docker to use Nimbus as an insecure registry, then push/pull directly:

```bash
# Add to /etc/docker/daemon.json (or Docker Desktop → Settings → Docker Engine)
{
  "insecure-registries": ["localhost:4566"]
}

# Tag and push
docker tag myimage:latest localhost:4566/my-repo:latest
docker push localhost:4566/my-repo:latest

# Pull
docker pull localhost:4566/my-repo:latest
```

## Example

```bash
# Create a repository
nimbuslocal ecr create-repository --repository-name my-app

# Get auth token (for tooling that requires it)
nimbuslocal ecr get-authorization-token

# List repositories
nimbuslocal ecr describe-repositories

# List images in a repository
nimbuslocal ecr list-images --repository-name my-app

# Delete an image by tag
nimbuslocal ecr batch-delete-image \
  --repository-name my-app \
  --image-ids imageTag=old-tag
```
