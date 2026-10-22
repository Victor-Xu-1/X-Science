package server

import (
	"path/filepath"
	"strings"

	"x-science/internal/agentruntime"
	eventjournal "x-science/internal/persistence/journal"
)

type sessionRunnerInputAttachmentReader struct {
	VersionID          string
	Filename           string
	FormatID           string
	Reader             string
	EquivalentPackages []string
	Read               bool
}

func inputAttachmentReaderStatesFromRunnerEntries(entries []eventjournal.Entry) []sessionRunnerInputAttachmentReader {
	states := make([]sessionRunnerInputAttachmentReader, 0)
	byVersion := map[string]int{}
	for _, entry := range entries {
		if role := strings.ToLower(strings.TrimSpace(stringValue(entry.Message["role"]))); role != "" && role != "user" {
			continue
		}
		refs, err := decodeUserArtifactReferences(entry.Message["artifactRefs"])
		if err != nil {
			continue
		}
		for _, ref := range refs {
			versionID := strings.TrimSpace(ref.VersionID)
			if versionID == "" {
				continue
			}
			if _, duplicate := byVersion[versionID]; duplicate {
				continue
			}
			format, _, _ := agentWorkspaceFormatHint(ref.Filename, ref.ContentType)
			byVersion[versionID] = len(states)
			states = append(states, sessionRunnerInputAttachmentReader{
				VersionID: versionID, Filename: ref.Filename, FormatID: format.ID, Reader: format.Reader,
				EquivalentPackages: append([]string(nil), format.EquivalentPackages...),
			})
		}
	}
	for _, entry := range entries {
		if role := strings.ToLower(strings.TrimSpace(stringValue(entry.Message["role"]))); role != "" && role != "user" {
			continue
		}
		bindings, err := runnerLibraryPDFBindings(entry.Message)
		if err != nil {
			continue
		}
		for _, binding := range bindings {
			if _, found := byVersion[binding.VersionID]; found {
				continue
			}
			format, _, _ := agentWorkspaceFormatHint(binding.Name, binding.MIMEType)
			byVersion[binding.VersionID] = len(states)
			states = append(states, sessionRunnerInputAttachmentReader{VersionID: binding.VersionID, Filename: binding.Name, FormatID: format.ID, Reader: format.Reader, EquivalentPackages: append([]string(nil), format.EquivalentPackages...)})
		}
	}
	if len(states) == 0 {
		return nil
	}
	for _, entry := range entries {
		message := entry.Message
		if !strings.EqualFold(strings.TrimSpace(stringValue(message["type"])), "runner_checkpoint") ||
			!strings.EqualFold(strings.TrimSpace(stringValue(message["toolName"])), "read_file") ||
			!strings.EqualFold(strings.TrimSpace(stringValue(message["toolPhase"])), "completed") ||
			agentruntime.ClassifyToolResult(message["toolResult"]) != agentruntime.ToolResultSucceeded {
			continue
		}
		input := decodeReadReuseMap(message["toolInput"])
		markInputAttachmentReaderState(states, input)
	}
	return states
}

func (run *sessionRunnerChatRun) setInputAttachmentReaders(states []sessionRunnerInputAttachmentReader) {
	if run == nil {
		return
	}
	copyStates := make([]sessionRunnerInputAttachmentReader, len(states))
	for index, state := range states {
		copyStates[index] = state
		copyStates[index].EquivalentPackages = append([]string(nil), state.EquivalentPackages...)
	}
	lock := run.scientificCapabilityLock()
	lock.Lock()
	defer lock.Unlock()
	run.InputAttachmentReaders = copyStates
}

func (run *sessionRunnerChatRun) recordInputAttachmentRead(input map[string]any) {
	if run == nil || input == nil {
		return
	}
	lock := run.scientificCapabilityLock()
	lock.Lock()
	defer lock.Unlock()
	markInputAttachmentReaderState(run.InputAttachmentReaders, input)
}

func markInputAttachmentReaderState(states []sessionRunnerInputAttachmentReader, input map[string]any) {
	versionID := strings.TrimSpace(stringValue(input["version_id"]))
	if versionID == "" {
		return
	}
	for index := range states {
		if versionID == states[index].VersionID {
			states[index].Read = true
		}
	}
}

func (run *sessionRunnerChatRun) unreadBuiltinAttachmentReadersForPackages(packages []string) []sessionRunnerInputAttachmentReader {
	if run == nil || len(packages) == 0 {
		return nil
	}
	requested := make(map[string]bool, len(packages))
	for _, value := range packages {
		if name := packageSpecName(value); name != "" {
			requested[name] = true
		}
	}
	lock := run.scientificCapabilityLock()
	lock.Lock()
	defer lock.Unlock()
	result := make([]sessionRunnerInputAttachmentReader, 0)
	for _, state := range run.InputAttachmentReaders {
		if state.Read || !strings.HasPrefix(state.Reader, "builtin_") {
			continue
		}
		overlaps := false
		for _, value := range state.EquivalentPackages {
			if requested[packageSpecName(value)] {
				overlaps = true
				break
			}
		}
		if overlaps {
			copyState := state
			copyState.EquivalentPackages = append([]string(nil), state.EquivalentPackages...)
			result = append(result, copyState)
		}
	}
	return result
}

func (run *sessionRunnerChatRun) unreadBuiltinAttachmentReadersReferencedBy(content string) []sessionRunnerInputAttachmentReader {
	if run == nil || strings.TrimSpace(content) == "" {
		return nil
	}
	normalized := filepath.ToSlash(content)
	lock := run.scientificCapabilityLock()
	lock.Lock()
	defer lock.Unlock()
	result := make([]sessionRunnerInputAttachmentReader, 0)
	for _, state := range run.InputAttachmentReaders {
		if state.Read || !strings.HasPrefix(state.Reader, "builtin_") ||
			!strings.Contains(normalized, state.VersionID+"-") {
			continue
		}
		copyState := state
		copyState.EquivalentPackages = append([]string(nil), state.EquivalentPackages...)
		result = append(result, copyState)
	}
	return result
}

// Available built-in readers are an efficiency observation, not operation
// admission. Neither an unread attachment nor a tool name in task prose makes
// a different valid reader or package operation unauthorized.
func (g serverAgentRuntimeToolGateway) builtinAttachmentReaderSuggestions(publicName string, input map[string]any) []sessionRunnerInputAttachmentReader {
	if g.taskRun == nil {
		return nil
	}
	switch publicName {
	case "manage_packages":
		if !strings.EqualFold(strings.TrimSpace(stringValue(input["mode"])), "install") {
			return nil
		}
		return g.taskRun.unreadBuiltinAttachmentReadersForPackages(skillStringValues(input["packages"]))
	case "bash", "python", "r", "repl", "powershell":
		content := strings.Join([]string{
			stringValue(input["command"]), stringValue(input["code"]), stringValue(input["script"]),
		}, "\n")
		return g.taskRun.unreadBuiltinAttachmentReadersReferencedBy(content)
	default:
		return nil
	}
}
