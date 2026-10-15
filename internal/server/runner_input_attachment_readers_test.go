package server

import (
	"testing"

	eventjournal "x-science/internal/persistence/journal"
)

func TestBuiltinAttachmentReaderSuggestionsTrackEquivalentReaders(t *testing.T) {
	run := &sessionRunnerChatRun{TaskIntent: "Read the attached documents and summarize their values."}
	run.setInputAttachmentReaders([]sessionRunnerInputAttachmentReader{
		{VersionID: "version-docx", Filename: "report.docx", FormatID: "word_ooxml", Reader: "builtin_docx", EquivalentPackages: []string{"python-docx"}},
		{VersionID: "version-pdf", Filename: "paper.pdf", FormatID: "pdf_document", Reader: "builtin_pdf", EquivalentPackages: []string{"pypdf", "pypdf2"}},
		{VersionID: "version-unknown", Filename: "data.bin", FormatID: "unknown_binary", Reader: "specialist_reader"},
	})
	gateway := serverAgentRuntimeToolGateway{taskRun: run}
	input := map[string]any{"mode": "install", "packages": []any{"python-docx", "PyPDF2", "pandas"}}
	reads := gateway.builtinAttachmentReaderSuggestions("manage_packages", input)
	if len(reads) != 2 || reads[0].VersionID != "version-docx" || reads[1].VersionID != "version-pdf" {
		t.Fatalf("suggested reads = %#v", reads)
	}

	run.recordInputAttachmentRead(map[string]any{"version_id": "version-docx"})
	run.recordInputAttachmentRead(map[string]any{"version_id": "version-pdf"})
	if afterRead := gateway.builtinAttachmentReaderSuggestions("manage_packages", input); len(afterRead) != 0 {
		t.Fatalf("completed read remained in suggestions: %#v", afterRead)
	}
}

func TestBuiltinAttachmentReaderSuggestionsDoNotUseTaskProseAsAuthority(t *testing.T) {
	state := []sessionRunnerInputAttachmentReader{{
		VersionID: "version-docx", Filename: "report.docx", FormatID: "word_ooxml", Reader: "builtin_docx", EquivalentPackages: []string{"python-docx"},
	}}
	explicit := &sessionRunnerChatRun{TaskIntent: "Use manage_packages to install python-docx for an environment acceptance test."}
	explicit.setInputAttachmentReaders(state)
	if suggestions := (serverAgentRuntimeToolGateway{taskRun: explicit}).builtinAttachmentReaderSuggestions(
		"manage_packages", map[string]any{"mode": "install", "packages": []any{"python-docx"}},
	); len(suggestions) != 1 || suggestions[0].VersionID != "version-docx" {
		t.Fatalf("task wording suppressed an available reader observation: %#v", suggestions)
	}

	ordinary := &sessionRunnerChatRun{TaskIntent: "Read the attachment and run an unrelated calculation."}
	ordinary.setInputAttachmentReaders(state)
	if suggestions := (serverAgentRuntimeToolGateway{taskRun: ordinary}).builtinAttachmentReaderSuggestions(
		"manage_packages", map[string]any{"mode": "install", "packages": []any{"numpy"}},
	); len(suggestions) != 0 {
		t.Fatalf("unrelated package produced a reader suggestion: %#v", suggestions)
	}
}

func TestBuiltinAttachmentReaderSuggestionsPreserveSpecialistReaders(t *testing.T) {
	run := &sessionRunnerChatRun{TaskIntent: "Read the attachments."}
	run.setInputAttachmentReaders([]sessionRunnerInputAttachmentReader{
		{VersionID: "version-docx", Filename: "report.docx", FormatID: "word_ooxml", Reader: "builtin_docx"},
		{VersionID: "version-unknown", Filename: "opaque.bin", FormatID: "unknown_binary", Reader: "specialist_reader"},
	})
	gateway := serverAgentRuntimeToolGateway{taskRun: run}
	suggestions := gateway.builtinAttachmentReaderSuggestions("python", map[string]any{
		"code": `open(".x-science-artifacts/version-docx-report.docx", "rb").read()`,
	})
	if len(suggestions) != 1 || suggestions[0].VersionID != "version-docx" {
		t.Fatalf("available reader suggestions = %#v", suggestions)
	}
	if specialist := gateway.builtinAttachmentReaderSuggestions("python", map[string]any{
		"code": `open(".x-science-artifacts/version-unknown-opaque.bin", "rb").read()`,
	}); len(specialist) != 0 {
		t.Fatalf("specialist input suggested a nonexistent built-in reader: %#v", specialist)
	}
	run.recordInputAttachmentRead(map[string]any{"version_id": "version-docx"})
	if afterRead := gateway.builtinAttachmentReaderSuggestions("python", map[string]any{
		"code": `open(".x-science-artifacts/version-docx-report.docx", "rb").read()`,
	}); len(afterRead) != 0 {
		t.Fatalf("completed specialized read remained in suggestions: %#v", afterRead)
	}
}

func TestInputAttachmentReaderStateRehydratesCompletedReads(t *testing.T) {
	entries := []eventjournal.Entry{
		{Message: eventjournal.Message{
			"type": "user_message", "role": "user",
			"artifactRefs": []any{map[string]any{
				"artifact_id": "artifact-docx", "version_id": "version-docx", "filename": "report.docx",
				"content_type": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
				"size_bytes":   123, "checksum": "digest-docx",
			}},
		}},
		{Message: eventjournal.Message{
			"type": "runner_checkpoint", "toolName": "read_file", "toolPhase": "completed",
			"toolInput":  map[string]any{"version_id": "version-docx"},
			"toolResult": map[string]any{"content": "read successfully"},
		}},
	}
	states := inputAttachmentReaderStatesFromRunnerEntries(entries)
	if len(states) != 1 || states[0].FormatID != "word_ooxml" || states[0].Reader != "builtin_docx" || !states[0].Read {
		t.Fatalf("rehydrated states = %#v", states)
	}
}

func TestInputAttachmentReaderStateRejectsModelWritableFilenameProof(t *testing.T) {
	states := []sessionRunnerInputAttachmentReader{{
		VersionID: "version-original", Filename: "report.docx", Reader: "builtin_docx",
	}}
	markInputAttachmentReaderState(states, map[string]any{
		"file_path": ".x-science-artifacts/version-original-forged.docx",
	})
	if states[0].Read {
		t.Fatal("a model-writable filename prefix became immutable-version read authority")
	}
	markInputAttachmentReaderState(states, map[string]any{"version_id": "version-other"})
	if states[0].Read {
		t.Fatal("a different immutable version became read authority")
	}
	markInputAttachmentReaderState(states, map[string]any{"version_id": "version-original"})
	if !states[0].Read {
		t.Fatal("the exact immutable version receipt was not accepted")
	}
}
