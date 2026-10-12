package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"x-science/internal/kernel"
)

// This explicit administrator command registers immutable installed prefixes;
// it is not a model tool and cannot import user-supplied JSON as a permit.
type environmentNamespaceEntry struct {
	Name         string `json:"name"`
	Generation   string `json:"generation"`
	SourceSHA256 string `json:"source_sha256"`
	NameOrigin   string `json:"name_origin"`
}

type environmentNamespacePlan struct {
	SchemaVersion int                         `json:"schema_version"`
	SourceSHA256  string                      `json:"source_sha256"`
	Environments  []environmentNamespaceEntry `json:"environments"`
	Applied       int                         `json:"applied"`
}

func planEnvironmentNamespace(imports []kernel.NamespaceEnvironmentImport) (environmentNamespacePlan, error) {
	plan := environmentNamespacePlan{SchemaVersion: 1, Environments: []environmentNamespaceEntry{}}
	for _, item := range imports {
		plan.Environments = append(plan.Environments, environmentNamespaceEntry{
			Name: item.CurrentName, Generation: item.Generation, SourceSHA256: item.SourceSHA256, NameOrigin: item.NameOrigin,
		})
	}
	raw, err := json.Marshal(plan.Environments)
	if err != nil {
		return environmentNamespacePlan{}, err
	}
	digest := sha256.Sum256(append([]byte("x-science.environment-namespace-plan.v1\x00"), raw...))
	plan.SourceSHA256 = hex.EncodeToString(digest[:])
	return plan, nil
}

func runEnvironmentNamespaceCLI(ctx context.Context, args []string, output io.Writer) error {
	if ctx == nil || output == nil {
		return errors.New("environment namespace context and output are required")
	}
	if len(args) == 0 || (args[0] != "inspect" && args[0] != "apply") {
		return errors.New("environment-namespace requires inspect or apply")
	}
	flags := flag.NewFlagSet("environment-namespace "+args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	source := flags.String("source-root", "", "absolute previous managed environment root")
	target := flags.String("target-root", "", "independent canonical registry root (apply only)")
	expected := flags.String("expected-source-sha256", "", "exact frozen inspect digest (apply only)")
	if err := flags.Parse(args[1:]); err != nil {
		return errors.New("invalid environment namespace options")
	}
	if flags.NArg() != 0 || !filepath.IsAbs(*source) || filepath.Clean(*source) != *source {
		return errors.New("environment namespace requires a clean absolute source root")
	}
	apply := args[0] == "apply"
	if !apply && (*target != "" || *expected != "") {
		return errors.New("inspect does not accept apply options")
	}
	if apply {
		digest, err := hex.DecodeString(*expected)
		if !filepath.IsAbs(*target) || filepath.Clean(*target) != *target || err != nil || len(digest) != sha256.Size || strings.ToLower(*expected) != *expected {
			return errors.New("apply requires a clean absolute target root and exact inspect digest")
		}
		if environmentNamespacePathsOverlap(*source, *target) {
			return errors.New("environment namespace source and target roots must be independent")
		}
	}
	imports, err := kernel.InspectManagedEnvironmentNamespaceImports(ctx, *source)
	if err != nil {
		return errors.New("environment namespace source inspection failed; no registration was applied")
	}
	plan, err := planEnvironmentNamespace(imports)
	if err != nil {
		return err
	}
	if apply {
		if plan.SourceSHA256 != *expected {
			return errors.New("environment namespace source changed; no registration was applied")
		}
		manager := kernel.NewManager(kernel.Config{CondaEnvsPath: *target})
		if err := manager.PreflightManagedEnvironmentNamespaceImports(ctx, imports); err != nil {
			return errors.New("environment namespace target preflight failed; no registration was applied")
		}
		for index, item := range imports {
			if _, err := manager.AdoptManagedEnvironmentNamespaceImport(ctx, item); err != nil {
				return fmt.Errorf("environment namespace registration stopped after %d of %d; preserve both roots and resume with the same verified source", index, len(imports))
			}
			plan.Applied++
		}
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(plan)
}

func environmentNamespacePathsOverlap(source, target string) bool {
	for _, pair := range [][2]string{{source, target}, {target, source}} {
		relative, err := filepath.Rel(pair[0], pair[1])
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
