package kernel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
)

const namespaceRegisteredCondaKind = "registered-conda"

type namespaceEnvironmentReadOnlyProbeKey struct{}

func namespaceEnvironmentProbeArguments(ctx context.Context, arguments []string) []string {
	if readOnly, _ := ctx.Value(namespaceEnvironmentReadOnlyProbeKey{}).(bool); !readOnly || len(arguments) == 0 || arguments[0] != "-I" {
		return arguments
	}
	return append([]string{"-I", "-B"}, arguments[1:]...)
}

type managedEnvironmentNamespaceRegistrationSource struct {
	SourceRoot             string                                     `json:"sourceRoot"`
	SourceName             string                                     `json:"sourceName"`
	SourcePrefix           string                                     `json:"sourcePrefix"`
	SourceGeneration       string                                     `json:"sourceGeneration"`
	PointerTarget          string                                     `json:"pointerTarget"`
	MarkerJSON             string                                     `json:"markerJSON"`
	MarkerSHA256           string                                     `json:"markerSHA256"`
	InventorySHA256        string                                     `json:"inventorySHA256"`
	PayloadSHA256          string                                     `json:"payloadSHA256"`
	SourceSHA256           string                                     `json:"sourceSHA256"`
	NameOrigin             string                                     `json:"nameOrigin"`
	UnresolvedDependencies []namespaceEnvironmentUnresolvedDependency `json:"unresolvedDependencies,omitempty"`
}

// CurrentName is distinct from the immutable physical source name. Only an
// exact published reserved name or an explicitly authorized import-resource
// rename is mapped; arbitrary user names/text are untouched.
var namespaceUnknownPackageResourceName = regexp.MustCompile(`^synon-pkg-[a-f0-9]{24}$`)

func namespaceRegisteredCurrentName(sourceName string) string {
	if sourceName == "synon-biomed-mcp-python" {
		return "x-science-mcp-python"
	}
	if namespaceUnknownPackageResourceName.MatchString(sourceName) {
		return "x-science-pkg-" + strings.TrimPrefix(sourceName, "synon-pkg-")
	}
	return sourceName
}

func namespaceRegisteredNameOrigin(sourceName string) string {
	if sourceName == "synon-biomed-mcp-python" {
		return "published-reserved-name"
	}
	if namespaceUnknownPackageResourceName.MatchString(sourceName) {
		// The original factory tuple was not persisted. This scoped rename is
		// authorized branding cleanup, not proof of factory-created provenance.
		return "origin-unknown-resource-rename"
	}
	return "user-resource"
}

func namespaceSourceCoreName(name string) bool {
	return name == "synon-biomed-python" || name == "synon-biomed-r" || name == "x-science-python" || name == "x-science-r"
}

func managedMarkerUsesExternalRuntime(marker managedEnvironmentMarker) bool {
	return marker.Kind == "path-venv" || marker.Kind == namespaceRegisteredCondaKind
}

func managedMarkerRuntimePrefix(registryPrefix string, marker managedEnvironmentMarker) string {
	if managedMarkerUsesExternalRuntime(marker) {
		return marker.RuntimePath
	}
	return registryPrefix
}

func namespaceRegistrationSourceDigest(source managedEnvironmentNamespaceRegistrationSource) string {
	source.SourceSHA256 = ""
	raw, _ := json.Marshal(source)
	digest := sha256.Sum256(append([]byte("x-science.environment-namespace-registration.v1\x00"), raw...))
	return hex.EncodeToString(digest[:])
}

func decodeNamespaceRegistrationSource(source managedEnvironmentNamespaceRegistrationSource) (managedEnvironmentMarker, error) {
	if !validSHA256(source.InventorySHA256) || !validSHA256(source.PayloadSHA256) || source.NameOrigin != namespaceRegisteredNameOrigin(source.SourceName) || source.SourceSHA256 != namespaceRegistrationSourceDigest(source) {
		return managedEnvironmentMarker{}, errors.New("environment registration source receipt is invalid")
	}
	original, err := decodeNamespaceRegistrationOriginalMarker(source)
	if err != nil {
		return managedEnvironmentMarker{}, err
	}
	if err := validateNamespaceUnresolvedDependencies(source); err != nil {
		return managedEnvironmentMarker{}, err
	}
	return original, nil
}

func decodeNamespaceRegistrationOriginalMarker(source managedEnvironmentNamespaceRegistrationSource) (managedEnvironmentMarker, error) {
	if !ValidEnvironmentName(source.SourceName) || !validSHA256(source.SourceGeneration) || !validSHA256(source.MarkerSHA256) || len(source.MarkerJSON) == 0 || len(source.MarkerJSON) > maxManagedEnvironmentInputBytes {
		return managedEnvironmentMarker{}, errors.New("environment registration original source is invalid")
	}
	root, err := canonicalManagedRegistrationPath(source.SourceRoot)
	if err != nil || root != source.SourceRoot {
		return managedEnvironmentMarker{}, errors.New("environment registration source root is unavailable or aliased")
	}
	prefix := filepath.Join(root, ".generations", source.SourceName, source.SourceGeneration)
	if prefix != source.SourcePrefix {
		return managedEnvironmentMarker{}, errors.New("environment registration source prefix is not bound to its generation")
	}
	canonical, err := canonicalManagedRegistrationPath(prefix)
	if err != nil || canonical != prefix {
		return managedEnvironmentMarker{}, errors.New("environment registration source prefix is unavailable or aliased")
	}
	digest := sha256.Sum256([]byte(source.MarkerJSON))
	if hex.EncodeToString(digest[:]) != source.MarkerSHA256 {
		return managedEnvironmentMarker{}, errors.New("environment registration original marker digest changed")
	}
	var marker managedEnvironmentMarker
	decoder := json.NewDecoder(strings.NewReader(source.MarkerJSON))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&marker) != nil || marker.NamespaceImport != nil || marker.PreviousNamespace != nil {
		return managedEnvironmentMarker{}, errors.New("environment registration original marker is malformed")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return managedEnvironmentMarker{}, errors.New("environment registration original marker has trailing content")
	}
	marker.PreviousNamespace = &managedEnvironmentNamespaceSource{SHA256: source.MarkerSHA256, MarkerJSON: source.MarkerJSON}
	verified, err := validateManagedEnvironmentMarker(marker)
	if err != nil || verified.Name != source.SourceName || verified.Generation != source.SourceGeneration || verified.Kind != "conda" {
		return managedEnvironmentMarker{}, errors.New("environment registration source is not a verified Conda generation")
	}
	return verified, nil
}

func validateNamespaceRegisteredEnvironmentMarker(marker managedEnvironmentMarker) (managedEnvironmentMarker, error) {
	if marker.NamespaceImport == nil || marker.PreviousNamespace != nil || marker.Kind != namespaceRegisteredCondaKind {
		return managedEnvironmentMarker{}, errors.New("environment registration marker authority is invalid")
	}
	source := *marker.NamespaceImport
	original, err := decodeNamespaceRegistrationSource(source)
	if err != nil {
		return managedEnvironmentMarker{}, err
	}
	projected := marker
	projected.NamespaceImport = nil
	projected.Name = source.SourceName
	projected.Kind = "conda"
	projected.SourcePath, projected.RuntimePath = original.SourcePath, original.RuntimePath
	projected.Operation, projected.OperationKey = original.Operation, original.OperationKey
	original.PreviousNamespace = nil
	if marker.Name != namespaceRegisteredCurrentName(source.SourceName) || marker.RuntimePath != source.SourcePrefix || marker.SourcePath != source.SourcePrefix ||
		marker.Operation != "namespace-register" || marker.OperationKey != source.SourceSHA256 || !reflect.DeepEqual(projected, original) {
		return managedEnvironmentMarker{}, errors.New("environment registration does not preserve its actual generation and recipe")
	}
	// Re-read only exact source identities during inventory. Payload chunks are
	// verified by the administrator's frozen inspect/adopt operation; normal
	// selected runtime health remains the existing manager's authority.
	raw, err := readBoundedJSON(filepath.Join(source.SourcePrefix, previousNamespaceEnvironmentMarkerName), new(managedEnvironmentMarker))
	if err != nil || string(raw) != source.MarkerJSON {
		return managedEnvironmentMarker{}, errors.New("environment registration original marker is no longer intact")
	}
	target, err := os.Readlink(filepath.Join(source.SourceRoot, source.SourceName))
	if err != nil || target != source.PointerTarget {
		return managedEnvironmentMarker{}, errors.New("environment registration original activation changed")
	}
	return marker, nil
}

func (m *Manager) verifyNamespaceRegisteredEnvironmentHealth(ctx context.Context, marker managedEnvironmentMarker) error {
	ctx = context.WithValue(ctx, namespaceEnvironmentReadOnlyProbeKey{}, true)
	prefix := managedMarkerRuntimePrefix("", marker)
	return m.validateManagedEnvironmentGeneration(ctx, marker.Generation, marker.Language, prefix, marker.Packages, marker.ImportNames)
}
