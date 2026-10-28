package bashsource

import (
	"strings"
	"testing"
)

func TestBashWrapperDecodesImmutableHistoricalEnvelopeWithoutGeneratingIt(t *testing.T) {
	command := "printf 'historical evidence\\n'"
	legacy, err := bashPythonWrapper(command, bashLegacyPump)
	if err != nil {
		t.Fatal(err)
	}
	current, err := BashPythonWrapper(command)
	if err != nil || current == legacy {
		t.Fatalf("current wrapper did not change: %v", err)
	}
	decoded, err := BashCommandFromWrapper(legacy)
	if err != nil || decoded != command {
		t.Fatalf("historical source=%q err=%v", decoded, err)
	}
	if _, err := BashCommandFromWrapper(strings.Replace(legacy, "read(8192)", "read(1)", 1)); err == nil {
		t.Fatal("modified historical execution envelope was accepted")
	}
}
