package function_crud

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// resolveImageURI returns the digest-pinned form of a container image
// reference. AWS resolves the tag to an immutable manifest digest when the
// function is created and reports both forms. Nimbus never pulls the image, so
// the digest is derived from the reference itself: stable across calls and
// unique per reference, but plainly synthetic rather than a real manifest hash.
func resolveImageURI(imageURI string) string {
	if imageURI == "" {
		return ""
	}
	if strings.Contains(imageURI, "@") {
		return imageURI // already digest-pinned
	}
	repo := imageURI
	// Strip the tag. A colon that precedes the last slash belongs to a registry
	// port (`host:5000/repo`), not to a tag, and must be left alone.
	if i := strings.LastIndex(imageURI, ":"); i > strings.LastIndex(imageURI, "/") {
		repo = imageURI[:i]
	}
	return repo + "@sha256:" + imageDigest(imageURI)
}

// imageDigest is the synthetic content hash reported as CodeSha256 for
// container-image functions and as the digest half of ResolvedImageUri.
func imageDigest(imageURI string) string {
	sum := sha256.Sum256([]byte(imageURI))
	return hex.EncodeToString(sum[:])
}
