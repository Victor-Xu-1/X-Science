package server

import (
	"context"
	"net/http"
	"testing"
)

func TestConversationSkillAuthoringIgnoresRedundantModelMetadata(t *testing.T) {
	for _, field := range []string{"name", "description"} {
		t.Run(field, func(t *testing.T) {
			duplicate := conversationSkillDraftArgumentsForTest(t, func(fields map[string]any) {
				fields["skill_markdown"] = "---\nname: ignored\ndescription: Ignored metadata.\n" +
					field + ": Duplicate value.\n---\nValidate the provided inputs."
			})
			fixture := newConversationSkillAuthoringFixture(t, []conversationSkillAuthoringReply{
				conversationSkillToolReplyForTest(duplicate),
			})
			response := fixture.save(context.Background())
			if response.Code != http.StatusCreated || len(fixture.capturedRequests()) != 1 {
				t.Fatalf("redundant metadata save status=%d requests=%d", response.Code, len(fixture.capturedRequests()))
			}
			skill, found := findCatalogSkill(fixture.server.skillCatalog, "typed-fixture-workflow")
			if !found || skill.Description != "A reusable fixture workflow." {
				t.Fatal("structured metadata was not the sole installed authority")
			}
		})
	}
}
