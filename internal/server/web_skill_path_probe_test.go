package server

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWebSkillSourceRejectsUngrantedPathBeforeExistenceProbe(t *testing.T) {
	srv := New(Options{FileRoot: t.TempDir()})
	outside := filepath.Join(t.TempDir(), "not-granted")
	_, _, missingErr := srv.loadGrantedWebSkills("skill-owner", outside)
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	_, _, presentErr := srv.loadGrantedWebSkills("skill-owner", outside)
	if !errors.Is(missingErr, errWebSkillPathForbidden) || !errors.Is(presentErr, errWebSkillPathForbidden) {
		t.Fatalf("ungranted host metadata exposed: missing=%v present=%v", missingErr, presentErr)
	}
}
