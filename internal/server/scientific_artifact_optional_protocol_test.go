package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/persistence/workspace"
)

func controlledScientificValidator(t *testing.T, payload scientificArtifactValidation, exit int) scientificArtifactValidator {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("controlled validator protocol requires an installed Python interpreter")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(t.TempDir(), "validator.py")
	source := fmt.Sprintf("import sys\nsys.stdin.buffer.read()\nprint(%q)\nsys.exit(%d)\n", string(raw), exit)
	if err := os.WriteFile(program, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return func(ctx context.Context, reader io.Reader, format string) (scientificArtifactValidation, error) {
		return runScientificArtifactValidator(ctx, reader, format, python, program, "controlled-protocol", "test-parser")
	}
}

func scientificProtocolFixture(code string) scientificArtifactValidation {
	result := scientificArtifactValidation{SchemaVersion: 2, Format: "smi", Code: code, TerminalDelimiter: true,
		DelimiterCount: 1, SupplierRecordCount: 1, AtomCountRecords: 1}
	if code != "parser_unavailable" {
		result.RDKitVersion = "test-parser"
	}
	if code == "invalid_smiles_records" {
		result.InvalidRecordCount = 1
	}
	if code == "valid_smiles" {
		result.OK, result.ParsedCount = true, 1
	}
	return result
}

func TestOptionalScientificValidatorProtocolDoesNotWaiveMalformedOrFailedValidation(t *testing.T) {
	for _, test := range []struct {
		name, code string
		exit       int
		optional   bool
		invalid    bool
	}{
		{"missing parser", "parser_unavailable", 3, true, false},
		{"failed parser", "parser_failed", 3, false, false},
		{"input read failure", "input_read_failed", 3, false, false},
		{"wrong unavailable exit", "parser_unavailable", 0, false, false},
		{"wrong invalid exit", "invalid_smiles_records", 0, false, false},
		{"unknown status", "unrecognized_status", 2, false, false},
		{"confirmed invalid", "invalid_smiles_records", 2, false, true},
		{"valid", "valid_smiles", 0, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			validate := controlledScientificValidator(t, scientificProtocolFixture(test.code), test.exit)
			result, err := validate(context.Background(), strings.NewReader("CCO\n"), "smi")
			_, optional := optionalScientificValidationReason(err)
			if optional != test.optional || errors.Is(err, errInvalidScientificArtifact) != test.invalid || (err == nil) != (test.code == "valid_smiles") {
				t.Fatalf("protocol failure misclassified: result=%#v err=%v optional=%t", result, err, optional)
			}
		})
	}
	for _, err := range []error{errScientificArtifactValidationUnavailable, scientificArtifactValidatorContractError(), errors.New("parser_unavailable")} {
		if _, optional := optionalScientificValidationReason(err); optional {
			t.Fatalf("untyped/contract failure acquired optional authority: %v", err)
		}
	}
}

func TestOptionalScientificValidatorDoesNotHideLaterInvalidArtifacts(t *testing.T) {
	store := openRunnerArtifactCompletionStore(t)
	var commits []transcriptstore.ArtifactReferenceInput
	for index := 0; index < 2; index++ {
		artifact, version, err := store.SaveArtifactVersion(workspace.SaveArtifactVersionInput{
			ArtifactID: fmt.Sprintf("molecular-%d", index), ProjectID: "project-a", Name: fmt.Sprintf("library-%d.smi", index),
			Kind: "chemical/x-daylight-smiles", Content: []byte("CCO\n"), CreatedBy: "runner",
		})
		if err != nil {
			t.Fatal(err)
		}
		commits = append(commits, transcriptstore.ArtifactReferenceInput{ArtifactID: artifact.ID, VersionID: version.ID, Relation: transcriptstore.ArtifactRelationProduced})
	}
	unavailable := controlledScientificValidator(t, scientificProtocolFixture("parser_unavailable"), 3)
	invalid := controlledScientificValidator(t, scientificProtocolFixture("invalid_smiles_records"), 2)
	calls := 0
	result, err := inspectScientificArtifactSelection(context.Background(), store, "project-a", commits, "", func(ctx context.Context, reader io.Reader, format string) (scientificArtifactValidation, error) {
		calls++
		if calls == 1 {
			return unavailable(ctx, reader, format)
		}
		return invalid(ctx, reader, format)
	})
	if err != nil || calls != 2 || len(result.Unavailable) != 1 || len(result.Failures) != 1 || result.Failures[0] != "invalid_smiles_records" {
		t.Fatalf("earlier optional failure hid later invalid bytes: calls=%d result=%#v err=%v", calls, result, err)
	}
}

type scientificReadFailure struct{ err error }

func (reader scientificReadFailure) Read([]byte) (int, error) { return 0, reader.err }

func TestScientificArtifactIntegrityIsHardBeforeOptionalNotice(t *testing.T) {
	const content = "CCO\n"
	digest := sha256.Sum256([]byte(content))
	version := workspace.ArtifactVersion{SizeBytes: int64(len(content)), ContentSHA256: hex.EncodeToString(digest[:])}
	optional := func(context.Context, io.Reader, string) (scientificArtifactValidation, error) {
		return scientificArtifactValidation{}, &scientificArtifactCapabilityUnavailable{reason: "not_configured"}
	}
	readFailure := errors.New("controlled input read failed")
	for _, test := range []struct {
		name   string
		reader io.Reader
	}{
		{"same-size mutation", strings.NewReader("CCN\n")},
		{"short input", strings.NewReader("CCO")},
		{"unread bad tail", io.MultiReader(strings.NewReader(content), scientificReadFailure{readFailure})},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := validateScientificArtifactBytes(context.Background(), test.reader, version, "smi", optional)
			if _, allowed := optionalScientificValidationReason(err); err == nil || allowed {
				t.Fatalf("input failure softened: %v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := validateScientificArtifactBytes(ctx, strings.NewReader(content), version, "smi", optional); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation softened: %v", err)
	}
}

func TestScientificUnavailableNoticeUsesOneLocalizedFinalWithoutInventingPass(t *testing.T) {
	advice := (scientificArtifactCompletionValidation{Unavailable: []scientificArtifactUnavailableObservation{{VersionID: "version", SHA256: "digest", Reason: "not_configured"}}}).advisory()
	for _, language := range []string{"", "en", "zh-CN"} {
		text := appendSessionRunnerCompletionAdvisories("Original result: 12.5", language, advice, advice)
		want := "Chemical validation was not performed"
		if sessionRunnerRequiresChinese(language) {
			want = "化学结构校验未执行"
		}
		if strings.Count(text, want) != 1 || appendSessionRunnerCompletionAdvisories(text, language, advice) != text || !strings.HasPrefix(text, "Original result: 12.5") {
			t.Fatalf("notice missing or duplicated: %q", text)
		}
		if got := appendSessionRunnerCompletionAdvisories("", language, advice); got != "" {
			t.Fatal("unavailability notice fabricated a final answer")
		}
	}
}
