package productidentity

import "testing"

func TestCurrentUsesRootProductIdentityAuthority(t *testing.T) {
	identity := Current()
	if identity.Schema != authoritySchema || identity.DisplayName != "X-Science" || !semanticVersionPattern.MatchString(identity.Version) || identity.MachineSlug != "x-science" {
		t.Fatalf("identity = %#v", identity)
	}
	if identity.FullDisplay() != "X-Science v"+identity.Version || identity.SourcePackage() != "x-science-v"+identity.Version || identity.UserAgent() != "x-science/"+identity.Version {
		t.Fatalf("derived identity is inconsistent: %#v", identity)
	}
}

func TestDecodeProductIdentityFailsClosed(t *testing.T) {
	for _, raw := range []string{
		`{"schema":"synon.product-identity.v1","schema":"synon.product-identity.v1","display_name":"Synon Biomed","version":"0.1.1","machine_slug":"synon-biomed"}`,
		`{"schema":"synon.product-identity.v1","display_name":"Synon Biomed","version":"0.1.1","machine_slug":"synon-biomed","unknown":true}`,
		`{"schema":"synon.product-identity.v1","display_name":" Synon Biomed","version":"0.1.1","machine_slug":"synon-biomed"}`,
		`{"schema":"synon.product-identity.v1","display_name":"Synon Biomed","version":"01.0.0","machine_slug":"synon-biomed"}`,
		`{"schema":"synon.product-identity.v1","display_name":"Synon Biomed","version":"0.1.1","machine_slug":"Synon Biomed"}`,
		`{"schema":"synon.product-identity.v1","display_name":"Synon Biomed","version":"0.1.1","machine_slug":"synon-biomed"} true`,
	} {
		if _, err := decode([]byte(raw)); err == nil {
			t.Fatalf("decode(%s) succeeded", raw)
		}
	}
}
