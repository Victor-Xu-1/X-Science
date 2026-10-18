package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	workspace "x-science/internal/persistence/workspace"
)

const (
	maxWebConversationInputFileBytes  int64 = 64 << 20
	maxWebConversationInputTotalBytes int64 = 256 << 20
)

// materializeWebConversationInputFiles makes server-selected files available
// inside an isolated Web task workspace. The browser's server-side file picker
// returns host-absolute paths, while scientific runtimes mount only the task
// workspace. Persisting the source path makes the attachment visible in the UI
// but unreadable by every runtime tool.
func (s *Server) materializeWebConversationInputFiles(
	ctx context.Context,
	r *http.Request,
	frame workspace.CompatibilityFrame,
	files []string,
) ([]string, error) {
	if len(files) == 0 {
		return nil, nil
	}
	if cause := context.Cause(ctx); cause != nil {
		return nil, errors.Join(ctx.Err(), cause)
	}
	references := make([]*hostGrantFileReference, len(files))
	hasGrantedReference := false
	for index, raw := range files {
		reference, matched, err := parseHostGrantFileReference(strings.TrimSpace(raw))
		if err != nil {
			return nil, err
		}
		if matched {
			references[index] = &reference
			hasGrantedReference = true
		}
	}
	access, found, err := s.workspaceStore.GetKernelFrameAccessContext(ctx, frame.ID)
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return nil, errors.Join(err, ctx.Err(), cause)
		}
		return nil, transcriptWebStorageError(err)
	}
	if !found {
		return nil, &webConversationRequestError{Status: http.StatusNotFound, Detail: "conversation not found"}
	}
	// Repository-backed conversations retain their existing explicit-path
	// contract. Managed Web workspaces require task-local materialization.
	taskRoot, managed, err := s.selectAgentWorkspace(access, access.ProjectPath)
	if err != nil {
		return nil, transcriptWebStorageError(err)
	}
	if !managed && !hasGrantedReference {
		return append([]string(nil), files...), nil
	}
	taskRoot, taskDirectory, inputRoot, destinationCheck, err := s.prepareWebConversationInputDirectory(access.UserID, access.Frame.ProjectID, taskRoot, managed)
	if err != nil {
		return nil, err
	}
	defer taskDirectory.Close()
	defer inputRoot.Close()

	materialized := make([]string, 0, len(files))
	seen := make(map[string]struct{}, len(files))
	var total int64
	for index, raw := range files {
		requested := strings.TrimSpace(raw)
		if requested == "" {
			continue
		}
		if reference := references[index]; reference != nil {
			relative, size, copyErr := s.materializeHostGrantFileReference(ctx, access.UserID, *reference, inputRoot, maxWebConversationInputTotalBytes-total, destinationCheck)
			if copyErr != nil {
				return nil, copyErr
			}
			total += size
			materialized = appendUniqueWebConversationFile(materialized, seen, relative)
			continue
		}
		if !managed {
			materialized = appendUniqueWebConversationFile(materialized, seen, requested)
			continue
		}
		if !filepath.IsAbs(requested) {
			cleaned := filepath.Clean(requested)
			if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
				return nil, invalidWebConversationInputFile()
			}
			materialized = appendUniqueWebConversationFile(materialized, seen, filepath.ToSlash(cleaned))
			continue
		}

		allowLocalRead := s.allowWebFSLocalRead(r)
		sourceAccess, err := s.resolveWebFSPath(access.UserID, requested, "", false, true, allowLocalRead)
		if err != nil {
			return nil, invalidWebConversationInputFile()
		}
		sourceFile, err := openWebFSRegularFile(sourceAccess)
		if err != nil {
			return nil, invalidWebConversationInputFile()
		}
		info, err := sourceFile.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maxWebConversationInputFileBytes {
			_ = sourceFile.Close()
			return nil, invalidWebConversationInputFile()
		}
		if info.Size() > maxWebConversationInputTotalBytes-total {
			_ = sourceFile.Close()
			return nil, &webConversationRequestError{Status: http.StatusRequestEntityTooLarge, Detail: "selected files exceed the task input limit"}
		}
		if relative, inside := relativePathWithin(taskRoot, sourceAccess.Target); inside {
			if err := sourceFile.Close(); err != nil {
				return nil, invalidWebConversationInputFile()
			}
			total += info.Size()
			materialized = appendUniqueWebConversationFile(materialized, seen, relative)
			continue
		}

		limit := min(maxWebConversationInputFileBytes, maxWebConversationInputTotalBytes-total)
		relative, size, copyErr := copyWebConversationInputFile(ctx, sourceFile, filepath.Base(sourceAccess.Target), inputRoot, limit)
		err = errors.Join(copyErr, sourceFile.Close())
		if err != nil {
			return nil, invalidWebConversationInputFile()
		}
		total += size
		materialized = appendUniqueWebConversationFile(materialized, seen, relative)
	}
	return materialized, nil
}

func (s *Server) prepareWebConversationInputDirectory(ownerID, projectID, taskRoot string, managed bool) (string, *os.Root, *os.Root, func() error, error) {
	if !managed {
		// Selection is not a write lease. Revalidate and prepare the first
		// namespace under the existing grant-mutation lock, before any writes.
		s.hostGrantKernelMu.Lock()
		defer s.hostGrantKernelMu.Unlock()
		if err := s.validateProjectPathAuthority(ownerID, projectID, taskRoot); err != nil {
			return "", nil, nil, nil, invalidWebConversationInputFile()
		}
	}
	var err error
	if managed {
		taskRoot, err = canonicalOrCreateAgentWorkspaceDirectory(taskRoot, 0o700)
	} else {
		// External workspaces were selected from existing authorized roots;
		// disappearance does not authorize recreating a host directory.
		taskRoot, err = canonicalHostDirectory(taskRoot)
		if err == nil {
			err = s.validateProjectPathAuthority(ownerID, projectID, taskRoot)
		}
	}
	if err != nil {
		return "", nil, nil, nil, transcriptWebStorageError(err)
	}
	taskDirectory, err := os.OpenRoot(taskRoot)
	if err != nil {
		return "", nil, nil, nil, transcriptWebStorageError(err)
	}
	if !managed {
		grants, err := s.loadHostGrants(ownerID)
		writable := make([]hostGrant, 0, len(grants))
		for _, grant := range grants {
			if grant.Mode == "read_write" {
				writable = append(writable, grant)
			}
		}
		if err != nil || verifyOpenedGrantedDirectory(taskDirectory, writable) != nil {
			_ = taskDirectory.Close()
			return "", nil, nil, nil, invalidWebConversationInputFile()
		}
	}
	if err := taskDirectory.Mkdir("inputs", 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		_ = taskDirectory.Close()
		return "", nil, nil, nil, transcriptWebStorageError(err)
	}
	inputRoot, err := taskDirectory.OpenRoot("inputs")
	if err != nil {
		_ = taskDirectory.Close()
		return "", nil, nil, nil, invalidWebConversationInputFile()
	}
	var destinationCheck func() error
	if !managed {
		destinationCheck, err = s.hostFileDestinationCheck(ownerID, projectID, taskRoot, taskDirectory, inputRoot)
		if err != nil {
			_ = inputRoot.Close()
			_ = taskDirectory.Close()
			return "", nil, nil, nil, err
		}
	}
	return taskRoot, taskDirectory, inputRoot, destinationCheck, nil
}

func invalidWebConversationInputFile() error {
	return &webConversationRequestError{
		Status: http.StatusBadRequest,
		Detail: "a selected file could not be imported into the task workspace",
	}
}

func appendUniqueWebConversationFile(target []string, seen map[string]struct{}, value string) []string {
	value = filepath.ToSlash(strings.TrimSpace(value))
	if value == "" {
		return target
	}
	if _, exists := seen[value]; exists {
		return target
	}
	seen[value] = struct{}{}
	return append(target, value)
}

func rewriteWebConversationAttachedFilePaths(content string, original, materialized []string) string {
	for index := 0; index < len(original) && index < len(materialized); index++ {
		source := strings.TrimSpace(original[index])
		target := strings.TrimSpace(materialized[index])
		if source == "" || target == "" || source == target {
			continue
		}
		content = strings.ReplaceAll(content, source, target)
	}
	return content
}

func relativePathWithin(root, target string) (string, bool) {
	relative, err := filepath.Rel(root, target)
	if err != nil || filepath.IsAbs(relative) || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(relative), true
}

func copyWebConversationInputFile(ctx context.Context, source *os.File, name string, inputRoot *os.Root, maxBytes int64) (relative string, size int64, err error) {
	return copyWebConversationInputFileWithCommit(ctx, source, name, inputRoot, maxBytes, nil)
}

func copyWebConversationInputFileWithCommit(ctx context.Context, source *os.File, name string, inputRoot *os.Root, maxBytes int64, commit workspaceFileCommit) (relative string, size int64, err error) {
	if name != filepath.Base(name) || name == "." || name == ".." || strings.TrimSpace(name) == "" {
		return "", 0, errors.New("invalid input filename")
	}
	before, err := source.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() > maxBytes {
		return "", 0, invalidWebConversationInputFile()
	}
	stage, err := stageWorkspaceFileWithCommit(ctx, inputRoot, source, maxBytes, commit)
	if err != nil {
		return "", 0, err
	}
	defer func() { err = errors.Join(err, stage.close()) }()
	after, err := source.Stat()
	if err != nil || before.Size() != stage.size || after.Size() != stage.size || !before.ModTime().Equal(after.ModTime()) {
		return "", 0, errWorkspaceFileConflict
	}
	err = stage.publishWithCommit(ctx, name, commit)
	if errors.Is(err, errWorkspaceFileConflict) {
		// A conflicting regular basename gets a deterministic alternative, but
		// neither that alternative nor a symlink may be replaced or followed.
		info, statErr := inputRoot.Lstat(name)
		if statErr != nil || !info.Mode().IsRegular() {
			return "", 0, err
		}
		extension := filepath.Ext(name)
		name = fmt.Sprintf("%s-%s%s", strings.TrimSuffix(name, extension), stage.digest[:12], extension)
		err = stage.publishWithCommit(ctx, name, commit)
	}
	if err != nil {
		return "", 0, err
	}
	return filepath.ToSlash(filepath.Join("inputs", name)), stage.size, nil
}

func (s *Server) materializeHostGrantFileReference(ctx context.Context, ownerID string, reference hostGrantFileReference, inputRoot *os.Root, remaining int64, destinationCheck func() error) (relative string, size int64, err error) {
	snapshot, err := s.openHostGrantFileSnapshot(ctx, ownerID, reference)
	if err != nil {
		return "", 0, err
	}
	defer func() {
		err = errors.Join(err, snapshot.close())
		if cause := context.Cause(ctx); cause != nil {
			err = errors.Join(err, ctx.Err(), cause)
		}
	}()
	info, err := snapshot.file.Stat()
	if err != nil || info.Size() < 0 || info.Size() > maxWebConversationInputFileBytes {
		return "", 0, invalidWebConversationInputFile()
	}
	if info.Size() > remaining {
		return "", 0, &webConversationRequestError{Status: http.StatusRequestEntityTooLarge, Detail: "selected files exceed the task input limit"}
	}
	relative, size, err = copyWebConversationInputFileWithCommit(ctx, snapshot.file, filepath.Base(reference.path), inputRoot,
		min(maxWebConversationInputFileBytes, remaining), func(action func() error) error {
			return snapshot.commit(ctx, func() error {
				if destinationCheck != nil {
					if err := destinationCheck(); err != nil {
						return err
					}
				}
				return action()
			})
		})
	if err != nil && context.Cause(ctx) == nil {
		return "", 0, invalidWebConversationInputFile()
	}
	return relative, size, err
}
