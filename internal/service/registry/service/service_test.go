package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseChallenge(t *testing.T) {
	got := parseChallenge(`Bearer realm="https://ghcr.io/token",service="ghcr.io",scope="repository:org/app:pull"`)
	assert.Equal(t, "https://ghcr.io/token", got["realm"])
	assert.Equal(t, "ghcr.io", got["service"])
	assert.Equal(t, "repository:org/app:pull", got["scope"])

	assert.Empty(t, parseChallenge(`Basic realm="x"`))
	assert.Empty(t, parseChallenge(""))
}

func TestPickPlatform(t *testing.T) {
	manifests := []manifestDescriptor{{Digest: "sha256:arm"}, {Digest: "sha256:amd"}}
	manifests[0].Platform.OS, manifests[0].Platform.Architecture = "linux", "arm64"
	manifests[1].Platform.OS, manifests[1].Platform.Architecture = "linux", "amd64"

	assert.Equal(t, "sha256:amd", pickPlatform(manifests))
	assert.Equal(t, "sha256:arm", pickPlatform(manifests[:1]))
}
