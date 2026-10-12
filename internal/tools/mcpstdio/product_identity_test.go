package mcpstdio

import (
	"testing"

	"x-science/internal/buildinfo"
)

func TestProductClientInfoUsesXScienceIdentity(t *testing.T) {
	info := productClientInfo()
	if info["name"] != "x-science" || info["version"] != buildinfo.Release().Version {
		t.Fatalf("clientInfo = %#v, want root-authority identity", info)
	}
}
