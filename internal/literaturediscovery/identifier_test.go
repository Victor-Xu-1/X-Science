package literaturediscovery

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNormalizeIdentifierKnownSchemesAndBounds(t *testing.T) {
	for _, test := range []struct{ scheme, value, want string }{
		{"DOI", " doi:https://dx.doi.org/10.1234/UPPER). ", "10.1234/upper"},
		{"doi", "10.1234/seed Copyright 2025 publisher", "10.1234/seed"},
		{"pmid", "PMID: https://pubmed.ncbi.nlm.nih.gov/12345/?source=x", "12345"},
		{"pmcid", "https://pmc.ncbi.nlm.nih.gov/articles/PMC123/?source=x", "PMC123"},
		{"pmcid", "https://www.ncbi.nlm.nih.gov/pmc/articles/PMC456/", "PMC456"},
		{"pmcid", "PMCID: 789", "PMC789"},
		{"arxiv", "https://arxiv.org/pdf/2401.12345v2.pdf?download=1", "2401.12345"},
		{"arxiv", "arXiv: hep-th/9901001v3", "hep-th/9901001"},
	} {
		t.Run(test.scheme+"/"+test.want, func(t *testing.T) {
			identifier, valid := NormalizeIdentifier(Identifier{Scheme: test.scheme, Value: test.value})
			if !valid || identifier.Scheme != strings.ToLower(test.scheme) || identifier.Value != test.want {
				t.Fatalf("got %#v, %v", identifier, valid)
			}
		})
	}
	for _, identifier := range []Identifier{
		{Scheme: "url", Value: "https://127.0.0.1/private"}, {Scheme: "isbn", Value: "123456789"},
		{Scheme: "doi", Value: "../../secret"}, {Scheme: "doi", Value: "10.1234/a b"},
		{Scheme: "doi", Value: "10.1234/" + strings.Repeat("a", 4090)},
		{Scheme: "pmid", Value: "1/secret"}, {Scheme: "arxiv", Value: "https://attacker.test/2401.12345"},
	} {
		if value, valid := NormalizeIdentifier(identifier); valid || value != (Identifier{}) {
			t.Fatalf("accepted invalid identity %#v as %#v", identifier, value)
		}
	}
	boundary := "10.1234/" + strings.Repeat("a", 4096-len("10.1234/"))
	if value, valid := NormalizeIdentifier(Identifier{Scheme: "doi", Value: boundary}); !valid || len(value.Value) != 4096 {
		t.Fatal("valid maximum rejected")
	}
}

func TestExactProviderIDUsesUniqueNormalizedIdentityPriority(t *testing.T) {
	for _, test := range []struct {
		reference Reference
		want      string
	}{
		{Reference{Identifiers: []Identifier{{"doi", "10.1234/UPPER"}, {"doi", "https://doi.org/10.1234/upper"}, {"pmid", "123"}}}, "DOI:10.1234/upper"},
		{Reference{Identifiers: []Identifier{{"doi", "10.1234/one"}, {"doi", "10.1234/two"}, {"pmid", "123"}}}, "PMID:123"},
		{Reference{Identifiers: []Identifier{{"pmcid", "PMC2323736"}}}, "PMCID:2323736"},
		{Reference{Identifiers: []Identifier{{"doi", "bad"}}, SemanticScholarID: strings.ToUpper(testChildID)}, testChildID},
		{Reference{Identifiers: []Identifier{{"doi", "10.1234/one"}, {"doi", "10.1234/two"}}, SemanticScholarID: "../../secret"}, ""},
	} {
		if got := exactProviderID(test.reference); got != test.want {
			t.Fatalf("got %q want %q", got, test.want)
		}
	}
}

func TestNormalizeIdentifierPreservesBalancedDOISuffixes(t *testing.T) {
	for _, test := range []struct{ raw, want string }{
		{"10.1000/foo(bar)", "10.1000/foo(bar)"},
		{"https://doi.org/10.1000/FOO(bar(baz))", "10.1000/foo(bar(baz))"},
		{"doi:10.1000/foo[part]", "10.1000/foo[part]"},
		{"10.1000/foo{part}", "10.1000/foo{part}"},
		{"10.1000/foo(bar)).", "10.1000/foo(bar)"},
		{"10.1000/foo[part]];", "10.1000/foo[part]"},
		{"10.1000/foo(part)[other]", "10.1000/foo(part)[other]"},
		{"10.1000/plain).", "10.1000/plain"},
		{"10.1000/foo(bar) .", "10.1000/foo(bar)"},
		{"10.1000/plain .", "10.1000/plain"},
		{"10.1000/plain" + strings.Repeat(")", 90000), "10.1000/plain"},
	} {
		got, valid := NormalizeIdentifier(Identifier{Scheme: "doi", Value: test.raw})
		if !valid || got.Value != test.want {
			t.Errorf("DOI suffix changed: got %q, want %q", got.Value, test.want)
		}
	}
}

func TestFinderBalancedDOIIdentityRoundTrip(t *testing.T) {
	reference := testReference()
	reference.Identifiers = []Identifier{{Scheme: "doi", Value: "10.1000/SEED(part)"}}
	seed, child := testPaper(testSeedID), testPaper(testChildID)
	seed["externalIds"] = map[string]any{"DOI": "10.1000/SEED(part)"}
	child["externalIds"] = map[string]any{"DOI": "10.1000/CHILD[part]"}
	finder, calls := testFinder(t, reference, testResponse(testJSON(t, seed), 200),
		testResponse(testJSON(t, map[string]any{"offset": 0, "data": []any{map[string]any{"citedPaper": child}}}), 200))
	result, err := finder.Run(context.Background(), "owner", testRequest())
	if err != nil || result.State != "ready" || len(*calls) != 2 || result.Seed == nil || len(result.Records) != 1 {
		t.Fatalf("related lookup failed: %#v / %v", result, err)
	}
	if got := (*calls)[0].URL.Path; got != "/graph/v1/paper/DOI:10.1000/seed(part)" {
		t.Errorf("provider lookup used changed identity: %q", got)
	}
	for _, test := range []struct {
		record Record
		want   string
	}{{*result.Seed, "10.1000/seed(part)"}, {result.Records[0], "10.1000/child[part]"}} {
		if len(test.record.Identifiers) != 1 || test.record.Identifiers[0].Value != test.want {
			t.Errorf("provider result changed DOI: %#v, want %q", test.record.Identifiers, test.want)
		}
	}
}

func TestFinderRejectsMalformedCallerAndNonzeroUnresolvedPage(t *testing.T) {
	for _, change := range []func(*Request){
		func(request *Request) { request.ItemID = " " },
		func(request *Request) { request.ItemID = " local " },
		func(request *Request) { request.ItemID = strings.Repeat("a", 513) },
		func(request *Request) { request.ExpectedMetadataRevision = 0 },
		func(request *Request) { request.ExpectedMetadataRevision = 9007199254740992 },
		func(request *Request) { request.Direction = "https://attacker.test" },
		func(request *Request) { request.Offset = -1 },
		func(request *Request) { request.ResolutionToken = "../../secret" },
	} {
		finder, calls := testFinder(t, testReference())
		request := testRequest()
		change(&request)
		if _, err := finder.Run(context.Background(), "owner", request); !errors.Is(err, ErrInvalid) || len(*calls) != 0 {
			t.Fatalf("malformed request accepted: %#v / %v", request, err)
		}
	}
	finder, calls := testFinder(t, testReference())
	request := testRequest()
	request.Offset = 20
	if result, err := finder.Run(context.Background(), "owner", request); err != nil || result.Issue != "expired" || len(*calls) != 0 {
		t.Fatal("unresolved pagination performed provider query")
	}
}
