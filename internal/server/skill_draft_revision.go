package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	kernelruntime "x-science/internal/kernel"
	"x-science/internal/skills"
)

var skillDraftRevisionPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func requireSkillDraftRevision(revision string) error {
	if !skillDraftRevisionPattern.MatchString(revision) {
		return kernelruntime.NewHostCallError("draft_revision_required", "Read and review the current Skill draft, then publish with its expected_draft_revision")
	}
	return nil
}

// A content precondition, never an authorization grant. Only this frozen byte
// set may be read, validated or promoted for its revision; timestamps are absent.
type skillDraftSnapshot struct {
	name      string
	revision  string
	paths     []string
	resources map[string][]byte
}

func captureSkillDraftSnapshot(draft skills.Skill) (skillDraftSnapshot, error) {
	snapshot := skillDraftSnapshot{name: draft.Name, resources: make(map[string][]byte)}
	root := filepath.Dir(draft.Path)
	if err := verifyWritableCompatibilitySkillRoot(root); err != nil {
		return snapshot, err
	}
	directory, err := os.OpenRoot(root)
	if err != nil {
		return snapshot, err
	}
	defer directory.Close()
	if err := verifyOpenedGrantedDirectory(directory, []hostGrant{{Path: root, Mode: "read"}}); err != nil {
		return snapshot, err
	}
	var capturedBytes int64
	_, err = inspectCompatibilitySkillResources(directory, func(file *os.File, relative string) error {
		relative = filepath.ToSlash(relative)
		if strings.EqualFold(relative, compatibilitySkillDraftMarker) {
			return nil
		}
		if kernelSkillReadControlResource(relative) {
			return skillValidationError("reserved_resource_path", "Skill draft contains reserved publication control resources", nil)
		}
		info, err := file.Stat()
		if err != nil {
			return err
		}
		raw, err := readSkillDraftResourceBytes(file, info.Size(), maxSkillArchiveBytes-capturedBytes)
		if err != nil {
			return err
		}
		capturedBytes += int64(len(raw))
		snapshot.paths = append(snapshot.paths, relative)
		snapshot.resources[relative] = raw
		return nil
	})
	if err != nil {
		return snapshot, err
	}
	sort.Strings(snapshot.paths)
	digest := sha256.New()
	_, _ = digest.Write([]byte("x-science.skill-draft.v1\x00" + snapshot.name + "\x00"))
	for _, path := range snapshot.paths {
		content := sha256.Sum256(snapshot.resources[path])
		_, _ = digest.Write([]byte(path))
		_, _ = digest.Write([]byte{0})
		_, _ = digest.Write(content[:])
	}
	snapshot.revision = hex.EncodeToString(digest.Sum(nil))
	return snapshot, nil
}

// The walker budget is not a lease on file length. An out-of-band writer can
// grow the same inode before this fresh stat or while its bytes are being read.
func readSkillDraftResourceBytes(source io.Reader, size, remaining int64) ([]byte, error) {
	if remaining < 0 || remaining > maxSkillArchiveBytes || size < 0 || size > remaining {
		return nil, skillValidationError("resource_budget_exceeded", "Skill package exceeds its total-byte budget", nil)
	}
	raw, err := io.ReadAll(io.LimitReader(source, remaining+1))
	if int64(len(raw)) > remaining {
		return nil, skillValidationError("resource_budget_exceeded", "Skill package exceeds its total-byte budget", nil)
	}
	if err != nil || int64(len(raw)) != size {
		return nil, skillValidationError("unreadable_resource", "Skill draft changed while its reviewed bytes were read", err)
	}
	return raw, nil
}

func (snapshot skillDraftSnapshot) read(requested string) (string, []string, error) {
	path, err := normalizeKernelSkillReadPath(requested)
	if err != nil {
		return "", nil, err
	}
	content, found := snapshot.resources[path]
	if !found {
		return "", nil, skillValidationError("missing_resource", "Skill draft has no such resource", os.ErrNotExist)
	}
	return string(content), append([]string(nil), snapshot.paths...), nil
}

// The caller owns skillMutationMu. Both identity and visible content come from
// one snapshot, not independent metadata and resource reads.
func (s *Server) currentSkillDraftSnapshot(name, expectedPath string) (skillDraftSnapshot, error) {
	draft, found, err := s.findCompatibilitySkillDraft(name)
	if err != nil {
		return skillDraftSnapshot{}, err
	}
	if !found || expectedPath != "" && !sameCleanPath(draft.Path, expectedPath) {
		return skillDraftSnapshot{}, os.ErrNotExist
	}
	return captureSkillDraftSnapshot(draft)
}

func skillDraftRevisionError(err error) (*kernelruntime.HostCallError, bool) {
	var failure *kernelruntime.HostCallError
	matched := errors.As(err, &failure) && (failure.Code == "draft_revision_required" || failure.Code == "draft_revision_conflict")
	return failure, matched
}
