package buildinfo

import (
	"testing"

	productidentity "synon-go"
)

func TestReleaseInfoUsesSynonBiomedIdentity(t *testing.T) {
	info := Release()
	if info.Name != "X-Science" {
		t.Fatalf("Name = %q, want X-Science", info.Name)
	}
	if info.Version != productidentity.Current().Version {
		t.Fatalf("Version = %q, want root-authority version", info.Version)
	}
	if info.MachineSlug != "x-science" {
		t.Fatalf("MachineSlug = %q, want x-science", info.MachineSlug)
	}
	if info.SourcePackage != "x-science-v"+productidentity.Current().Version {
		t.Fatalf("SourcePackage = %q, want root-authority package", info.SourcePackage)
	}
}
