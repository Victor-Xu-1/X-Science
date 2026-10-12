package main

import (
	"bytes"
	"strings"
	"testing"

	"synon-go/internal/buildinfo"
)

func TestCurrentCLIHelpUsesProductDisplayIdentity(t *testing.T) {
	var output bytes.Buffer
	_, help, err := parseServeCommandFlags([]string{"--help"}, &output)
	if err != nil || !help || !strings.HasPrefix(output.String(), "Usage of "+buildinfo.Release().Name+" serve:") {
		t.Fatalf("native help label=%q help=%t err=%v", output.String(), help, err)
	}
	if !strings.Contains(modelSmokePrompt, buildinfo.Release().Name+" model protocol") {
		t.Fatal("model smoke prompt retains historical product identity")
	}
}
