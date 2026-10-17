package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestConversationSkillAuthoringServerOwnsManifestMetadata(t *testing.T) {
	const body = "# Reusable procedure\n\nInspect the provided input with the available tools."
	const description = "Inspect inputs: preserve facts and uncertainty.\nDo not execute during Save."
	cases := []struct {
		name, markdown, wantBody string
	}{
		{"body_only", body, body},
		{"valid_redundant_header", "---\nname: other-name\ndescription: other-description\nallowed-tools: [execute]\n---\n" + body, body},
		{"invalid_yaml", "---\nname: [unterminated\ndescription: Inspect: inputs\n---\n" + body, body},
		{"duplicate_metadata", "---\nname: first\nname: second\ndescription: first\ndescription: second\n---\n" + body, body},
		{"metadata_block_delimiter", "---\nname: ignored\ndescription: |-\n  A display line\n  ---\n  Another display line\n---\n" + body, body},
		{"nonmetadata_is_markdown", "---\n- Inspect the input\n---\n" + body, "---\n- Inspect the input\n---\n" + body},
		{"thematic_breaks_are_body", "---\n# Method introduction\n\nPreserve this observation.\n---\n" + body, "---\n# Method introduction\n\nPreserve this observation.\n---\n" + body},
		{"unclosed_thematic_break", "---\n" + body, "---\n" + body},
		{"delimiter_prefix_is_body", "---\nLiteral text\n---not-a-delimiter\n" + body, "---\nLiteral text\n---not-a-delimiter\n" + body},
		{"body_separators", body + "\n\n---\n\n## Next step", body + "\n\n---\n\n## Next step"},
		{"crlf", strings.ReplaceAll(body, "\n", "\r\n"), body},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			manifest, err := normalizeConversationSkillManifest(test.markdown, "owned-workflow", description)
			if err != nil {
				t.Fatalf("server-owned metadata must not parse model-authored YAML: %v", err)
			}
			parts := strings.SplitN(strings.TrimPrefix(manifest, "---\n"), "\n---\n", 2)
			if len(parts) != 2 {
				t.Fatal("missing canonical server-owned header")
			}
			var metadata map[string]string
			if err := yaml.Unmarshal([]byte(parts[0]), &metadata); err != nil {
				t.Fatalf("server emitted invalid metadata: %v", err)
			}
			want := map[string]string{"name": "owned-workflow", "description": description}
			if !reflect.DeepEqual(metadata, want) {
				t.Fatalf("model-authored metadata became authoritative: %#v", metadata)
			}
			if strings.TrimSpace(parts[1]) != test.wantBody {
				t.Fatalf("method body changed: got=%q want=%q", parts[1], test.wantBody)
			}
			repeated, err := normalizeConversationSkillManifest(manifest, "owned-workflow", description)
			if err != nil || repeated != manifest {
				t.Fatal("the single metadata writer must be idempotent across draft/bundle boundaries")
			}
		})
	}
	for _, markdown := range []string{"", "  \n\t", "---\nname: ignored\n---\n"} {
		if _, err := normalizeConversationSkillManifest(markdown, "owned-workflow", description); err == nil {
			t.Fatal("a header or whitespace alone must not install an empty procedure")
		}
	}
}

func TestConversationSkillAuthoringEscapesMetadataDelimiterText(t *testing.T) {
	const description = "A display line\n---\nname: must-not-become-metadata\nUnicode: 中文 / English."
	arguments := conversationSkillDraftArgumentsForTest(t, func(fields map[string]any) { fields["description"] = description })
	fixture := newConversationSkillAuthoringFixture(t, []conversationSkillAuthoringReply{conversationSkillToolReplyForTest(arguments)})
	if response := fixture.save(context.Background()); response.Code != http.StatusCreated {
		t.Fatalf("quoted structured metadata did not produce an importable Skill: %d", response.Code)
	}
	skill, found := findCatalogSkill(fixture.server.skillCatalog, "typed-fixture-workflow")
	if !found || skill.Description != description || !strings.Contains(skill.Body, "# Fixture workflow") {
		t.Fatal("description text escaped into the body/header boundary or changed the method")
	}
}

func TestSkillBundleImportKeepsExternalManifestValidation(t *testing.T) {
	for _, markdown := range []string{
		"---\nname: [unterminated\n---\nExternal procedure.",
		"---\nname: external-workflow\nname: duplicate-workflow\n---\nExternal procedure.",
	} {
		server := New(Options{FileRoot: t.TempDir()})
		postSkillBundle(t, server.Handler(), "/api/skills/import", "local", "external.md", []byte(markdown), http.StatusBadRequest)
		for _, name := range []string{"external-workflow", "duplicate-workflow", "external"} {
			if _, found := findCatalogSkill(server.skillCatalog, name); found {
				t.Fatal("external import acquired the authoring-only metadata path")
			}
		}
	}
}

func TestConversationSkillAuthoringInstallsWithServerOwnedManifest(t *testing.T) {
	for _, textCarrier := range []bool{false, true} {
		name := "named_return"
		if textCarrier {
			name = "whole_json"
		}
		t.Run(name, func(t *testing.T) {
			arguments := conversationSkillDraftArgumentsForTest(t, func(fields map[string]any) {
				fields["description"] = "Reusable method: validate observations."
				fields["skill_markdown"] = "---\nname: [unterminated\nallowed-tools: [unapproved]\n---\n# Reusable procedure\n\nInspect the supplied inputs."
			})
			reply := conversationSkillToolReplyForTest(arguments)
			if textCarrier {
				reply = conversationSkillAuthoringReply{message: map[string]any{"role": "assistant", "content": arguments}}
			}
			fixture := newConversationSkillAuthoringFixture(t, []conversationSkillAuthoringReply{reply})
			response := fixture.save(context.Background())
			if response.Code != http.StatusCreated || len(fixture.capturedRequests()) != 1 {
				t.Fatalf("save status=%d requests=%d want201/1", response.Code, len(fixture.capturedRequests()))
			}
			manifest, err := os.ReadFile(filepath.Join(fixture.root, "skills", "typed-fixture-workflow", "SKILL.md"))
			if err != nil || strings.Contains(string(manifest), "unapproved") ||
				!strings.Contains(string(manifest), "# Reusable procedure") {
				t.Fatalf("canonical installed manifest missing method or retained model metadata: %v", err)
			}
			if _, found := findCatalogSkill(fixture.server.skillCatalog, "typed-fixture-workflow"); !found {
				t.Fatal("server-authored manifest was not added to the existing catalog")
			}
		})
	}
}
