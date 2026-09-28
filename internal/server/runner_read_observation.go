package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"synon-go/internal/agentruntime"
)

func withFileReadProgressReserve(ctx context.Context) context.Context {
	reserved, _ := ctx.Value(agentWorkspaceReadLocationReserveKey{}).(int)
	// Reserve the largest control envelope before the reader allocates text.
	// Adding flags afterwards must not externalize a page that was in budget.
	const metadata = `,"reused":true,"effect":{"schema":"synon.tool_effect.v1","state":"unchanged","categories":["evidence-read"]}`
	return context.WithValue(ctx, agentWorkspaceReadLocationReserveKey{}, reserved+len(metadata))
}

// observeFileRead compares completed observations, never cached file access.
// Every invocation still revalidates ownership and reads current bytes first:
// a mutable path, deleted file or revoked grant cannot be served stale. The
// bounded per-run cache stores only digests for these observations, and replay
// restores them through the same routine from authoritative completed receipts.
func (run *sessionRunnerChatRun) observeFileRead(input map[string]any, response any) any {
	if run == nil {
		return response
	}
	value, parts := unwrapAgentRuntimeRichToolResponse(response)
	object, ok := value.(map[string]any)
	if !ok || !sessionRunnerReadReuseResultEligible(object) {
		return response
	}
	selection := copyMapAny(normalizeAgentWorkspaceReadFileArguments(input))
	// Compare the effective returned window, not a larger requested limit at
	// EOF. Equivalent version aliases and cosmetic page sizes must not create
	// apparent new evidence; JSON selections still retain their own identity.
	if version := stringValue(object["source_version_id"]); version != "" {
		selection["version_id"] = version
		delete(selection, "file_path")
	} else if path := stringValue(object["file_path"]); path != "" {
		selection["file_path"] = path
	}
	if lines := stringValue(object["showing_lines"]); lines != "" {
		delete(selection, "offset")
		delete(selection, "limit")
		selection["observed_lines"] = lines
	}
	if _, present := object["byte_offset"]; present {
		delete(selection, "byte_limit")
		selection["observed_bytes"] = object["bytes_read"]
	}
	key, ok := sessionRunnerReadReuseKey("read_file", selection)
	if !ok {
		return response
	}
	observed := copyMapAny(object)
	delete(observed, "reused")
	delete(observed, "effect")
	// Authorized immutable copies can have different version/path handles
	// while carrying identical bytes. The reader's verified source digest plus
	// actual displayed text/window defines evidence, not those transport IDs.
	if digest := stringValue(object["source_sha256"]); len(digest) == 64 && len(parts) == 0 {
		if content, text := object["content"].(string); text {
			selection["version_id"] = "sha256:" + digest
			delete(selection, "file_path")
			key, ok = sessionRunnerReadReuseKey("read_file", selection)
			if !ok {
				return response
			}
			observed = map[string]any{"source_sha256": digest, "content": content, "view_format": object["view_format"], "encoding": object["encoding"]}
		}
	}
	raw, err := json.Marshal(struct {
		Value map[string]any `json:"value"`
		Parts any            `json:"parts,omitempty"`
	}{observed, parts})
	if err != nil {
		return response
	}
	digest := sha256.Sum256(raw)
	encoded := hex.EncodeToString(digest[:])
	cache := run.readReuseCache()
	key = "observed:" + key
	prior, found := cache.store(key, sessionRunnerReadReuseEntry{observationDigest: encoded, size: int64(len(key) + len(encoded))})
	state := agentruntime.ToolEffectChanged
	result := copyMapAny(object)
	if found && prior.observationDigest == encoded {
		state = agentruntime.ToolEffectUnchanged
		result["reused"] = true
	}
	result["effect"] = agentruntime.ToolEffectValue(state, "evidence-read")
	if _, rich := response.(agentRuntimeRichToolResponse); rich {
		return agentRuntimeRichToolResponse{value: result, parts: parts}
	}
	return result
}
