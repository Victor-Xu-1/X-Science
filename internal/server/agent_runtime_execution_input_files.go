package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"synon-go/internal/sciencecapability"
)

var (
	errExecutionInputLineageMismatch = errors.New("input lineage digest mismatch")
	errExecutionInputMaterialization = errors.New("input materialization unavailable")
)

func executionInputLineageFailureReason(err error) string {
	if errors.Is(err, errExecutionInputMaterialization) {
		return "materialization_unavailable"
	}
	if errors.Is(err, errExecutionInputLineageMismatch) {
		return "input_lineage_mismatch"
	}
	return "invalid_lineage"
}

// Parse the exact static argv already admitted by the canonical pack router.
// Guarded flags must be explicit and unique; CLI abbreviation or a duplicate
// must not make the host and the actual parser consume different values.
func executionEvidenceArguments(pack sciencecapability.ExecutionPack, command string) ([]string, map[string]string, error) {
	args, ok := managedExecutionSingleShellCommandTokens(command)
	if !ok || len(args) < 2 {
		return nil, nil, errors.New("non-static execution arguments")
	}
	guarded := map[string]bool{}
	for _, parameter := range pack.Parameters {
		if parameter.InputEvidence != nil {
			guarded[parameter.Argument] = true
		}
	}
	for _, lineage := range pack.InputLineage {
		for _, input := range pack.Inputs {
			if input.Kind == lineage.InputKind {
				guarded[input.Argument] = true
			}
		}
	}
	for _, route := range pack.DocumentedInputs {
		guarded[route.Argument] = true
		for _, parameter := range pack.Parameters {
			if parameter.EvidenceGroup == route.EvidenceGroup {
				guarded[parameter.Argument] = true
			}
		}
		for _, input := range pack.Inputs {
			if input.Kind == route.InputKind {
				guarded[input.Argument] = true
			}
		}
	}
	values := map[string]string{}
	normalized := append([]string(nil), args[:2]...)
	for i := 2; i < len(args); i++ {
		flag, value, equals := strings.Cut(args[i], "=")
		for exact := range guarded {
			if strings.HasPrefix(flag, "--") && flag != exact && strings.HasPrefix(exact, flag) {
				return nil, nil, errors.New("abbreviated evidence argument")
			}
		}
		if !guarded[flag] {
			normalized = append(normalized, args[i])
			continue
		}
		if _, duplicate := values[flag]; duplicate {
			return nil, nil, errors.New("duplicate evidence argument")
		}
		if !equals {
			i++
			if i >= len(args) {
				return nil, nil, errors.New("missing evidence argument")
			}
			value = args[i]
		}
		if value == "" || strings.HasPrefix(value, "--") {
			return nil, nil, errors.New("invalid evidence argument")
		}
		values[flag] = value
		normalized = append(normalized, flag, value)
	}
	return normalized, values, nil
}

func openExecutionEvidenceFile(root, workingDir, value string) (*os.File, string, error) {
	if value == "" {
		return nil, "", errors.New("missing evidence file")
	}
	path := value
	if workingDir == "" {
		workingDir = root
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(workingDir, path)
	}
	// Existing output aliases may be absolute symlinks into the task's stable
	// snapshot root. Resolve once, then open descriptor-relative within root;
	// a concurrent redirection still cannot escape that filesystem authority.
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, "", err
	}
	relative, err := filepath.Rel(root, filepath.Clean(path))
	if err != nil {
		return nil, "", err
	}
	source, found, err := agentSavedArtifactSourceWithinRoot(root, relative)
	if err != nil {
		return nil, "", err
	}
	if !found {
		return nil, "", os.ErrNotExist
	}
	defer source.close()
	file, err := source.rootHandle.Open(source.relativePath)
	if err != nil {
		return nil, "", err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, "", errors.New("evidence file is not regular")
	}
	return file, source.absolutePath, nil
}

func executionEvidenceFile(ctx context.Context, root, workingDir, value string) (string, string, error) {
	file, path, err := openExecutionEvidenceFile(root, workingDir, value)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	digest, err := executionEvidenceFileDigest(ctx, file)
	return path, digest, err
}

func executionEvidenceFileDigest(ctx context.Context, file *os.File) (string, error) {
	before, err := file.Stat()
	if err != nil {
		return "", err
	}
	hasher := sha256.New()
	n, err := io.Copy(hasher, io.LimitReader(&contextKernelReader{ctx: ctx, reader: file}, before.Size()+1))
	if err != nil {
		return "", err
	}
	after, err := file.Stat()
	if err != nil || n != before.Size() || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return "", errAgentSavedArtifactSourceChanged
	}
	_, err = file.Seek(0, io.SeekStart)
	return hex.EncodeToString(hasher.Sum(nil)), err
}

func executionEvidenceOutput(root string, authority managedExecutionOutputAuthority, producer sciencecapability.ExecutionPack, kind string) (string, string) {
	for _, output := range producer.Outputs {
		if output.Kind != kind {
			continue
		}
		logical, ok := managedExecutionBundleOutputLocation(root, authority.Root, output.Path)
		if !ok || authority.Digests[logical] == "" {
			return "", ""
		}
		readable, ok := managedExecutionBundleOutputLocation(root, managedExecutionAuthorityReadableRoot(authority), output.Path)
		if !ok {
			return "", ""
		}
		return filepath.Join(root, filepath.FromSlash(readable)), authority.Digests[logical]
	}
	return "", ""
}

func (s *Server) bindExecutionInputLineage(
	ctx context.Context, root, workingDir string, pack sciencecapability.ExecutionPack, group string,
	values map[string]string, authority managedExecutionOutputAuthority, producer sciencecapability.ExecutionPack,
) (map[string]string, error) {
	bound := map[string]string{}
	for _, lineage := range pack.InputLineage {
		if lineage.EvidenceGroup != group {
			continue
		}
		path, _ := executionEvidenceOutput(root, authority, producer, lineage.OutputKind)
		file, _, err := openExecutionEvidenceFile(root, root, path)
		if err != nil {
			return nil, err
		}
		// Lineage is a bounded machine receipt, never an unbounded result body.
		raw, err := selectAgentWorkspaceJSONValue(ctx, io.LimitReader(file, (1<<20)+1), lineage.SHA256Pointer)
		info, statErr := file.Stat()
		_ = file.Close()
		var digest string
		if err != nil || statErr != nil || info.Size() > 1<<20 || json.Unmarshal(raw, &digest) != nil || len(digest) != sha256.Size*2 {
			return nil, errors.New("invalid receipt-bound input lineage")
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return nil, errors.New("invalid lineage digest")
		}
		argument := ""
		for _, input := range pack.Inputs {
			if input.Kind == lineage.InputKind {
				argument = input.Argument
				break
			}
		}
		// Materialization verifies the exact digest while copying through the
		// existing descriptor-relative immutable artifact boundary. The source
		// cannot be swapped after validation to change the consumer's input.
		path, err = s.materializeExecutionInputFile(ctx, root, workingDir, values[argument], digest)
		if err != nil {
			return nil, err
		}
		bound[argument] = path
	}
	return bound, nil
}
