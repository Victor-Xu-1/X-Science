package transfer

import (
	"math"
	"testing"
)

func TestOutputSelectionPrecedesTransferBudget(t *testing.T) {
	policy := Policy{Outputs: []Output{{Glob: "**/*.csv"}}, Exclude: []string{"private/**"}, MaxFileBytes: 4096, MaxTotalBytes: 8192}
	for _, test := range []struct {
		path   string
		bytes  int64
		used   int64
		want   bool
		reason string
	}{
		{"out/trajectory.bin", 40 << 30, 0, false, "not_selected"},
		{"out/private/table.csv", 10, 0, false, "excluded"},
		{"out/results/table.csv", 1024, 0, true, ""},
		{"stdout.log", 10, 0, true, ""},
		{"stderr.log", 10, 0, true, ""},
		{"out/results/table.csv", 4097, 0, false, "max_file_bytes"},
		{"out/results/table.csv", 2048, 7000, false, "max_total_bytes"},
	} {
		t.Run(test.path+test.reason, func(t *testing.T) {
			include, reason := policy.Select(test.path, test.bytes, test.used)
			if include != test.want || reason != test.reason {
				t.Fatalf("selection = %v, %q; want %v, %q", include, reason, test.want, test.reason)
			}
		})
	}
}

func TestTransferBudgetHasNoImplicitDatasetCeiling(t *testing.T) {
	policy := Policy{}
	if include, reason := policy.Select("out/large-model.bin", 80<<30, 0); !include || reason != "" {
		t.Fatalf("unspecified transfer limit invented a size ceiling: %v %s", include, reason)
	}
	if include, reason := (Policy{MaxTotalBytes: 120 << 30}).Select("out/large-model.bin", 80<<30, 0); !include || reason != "" {
		t.Fatalf("explicit large budget was shortened: %v %s", include, reason)
	}
}

func TestTransferBudgetRejectsOverflowAndInvalidMeasurements(t *testing.T) {
	policy := Policy{MaxTotalBytes: math.MaxInt64}
	if include, reason := policy.Select("out/result.bin", 10, math.MaxInt64-5); include || reason != "max_total_bytes" {
		t.Fatalf("overflow admitted: %v %s", include, reason)
	}
	for _, values := range [][2]int64{{-1, 0}, {1, -1}} {
		if include, reason := policy.Select("out/result.bin", values[0], values[1]); include || reason != "invalid_size" {
			t.Fatalf("invalid measurement admitted: %v %s", include, reason)
		}
	}
}

func TestOutputGlobKeepsTheExistingPathContract(t *testing.T) {
	for _, test := range []struct {
		pattern, candidate string
		want               bool
	}{
		{"*.csv", "out/table.csv", true},
		{"*.csv", "out/nested/table.csv", false},
		{"**/*.csv", "out/table.csv", true},
		{"**/*.csv", "out/nested/table.csv", true},
		{"out/**/table?.csv", "out/nested/table1.csv", true},
		{"out/**/table?.csv", "out/table1.csv", true},
		{"../*.csv", "out/table.csv", false},
		{"/out/*.csv", "out/table.csv", false},
		{"[x].csv", "out/[x].csv", true},
	} {
		if got := GlobMatches(test.pattern, test.candidate); got != test.want {
			t.Errorf("GlobMatches(%q, %q) = %v; want %v", test.pattern, test.candidate, got, test.want)
		}
	}
}

func TestTransferPolicyRejectsUnsafeOrUnboundedControlFields(t *testing.T) {
	for _, policy := range []Policy{
		{Outputs: []Output{{Glob: "../secret"}}},
		{Outputs: []Output{{Glob: "*.csv", Visibility: "unknown"}}},
		{Exclude: []string{"/secret"}},
		{Exclude: []string{"a\x00b"}},
		{MaxFileBytes: -1},
		{MaxFileBytes: 10, MaxTotalBytes: 5},
	} {
		if err := policy.Validate(); err == nil {
			t.Fatalf("invalid policy admitted: %#v", policy)
		}
	}
	if err := (Policy{Outputs: []Output{{Glob: "**/*"}}}).Validate(); err != nil {
		t.Fatal(err)
	}
}
