package kernelcontract

import (
	"strings"
	"testing"
)

func TestBashWrapperRoundTripRequiresCanonicalEnvelope(t *testing.T) {
	command := "printf 'result=%s\\n' ok\nprintf 'warning\\n' >&2"
	wrapper, err := BashPythonWrapper(command)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := BashCommandFromWrapper(wrapper)
	if err != nil || decoded != command {
		t.Fatalf("decoded=%q err=%v", decoded, err)
	}

	for _, invalid := range []string{
		strings.Replace(wrapper, "bufsize=1", "bufsize=2", 1),
		strings.Replace(wrapper, BashSourcePrefix, "# untrusted:", 1),
		BashSourcePrefix + "%%%\n",
	} {
		if _, err := BashCommandFromWrapper(invalid); err == nil {
			t.Fatalf("non-canonical Bash envelope was accepted: %q", invalid)
		}
	}
}

func TestBashWrapperRejectsEmptyNULAndOversizedCommands(t *testing.T) {
	for _, command := range []string{"", "\x00", strings.Repeat("x", MaxBashBytes+1)} {
		if _, err := BashPythonWrapper(command); err == nil {
			t.Fatalf("invalid Bash command was accepted: bytes=%d", len(command))
		}
	}
}
