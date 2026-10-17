package server

import (
	"context"
	"net/http"
	"testing"
)

func TestConversationSkillAuthoringRejectsDuplicateFrontmatter(t *testing.T) {
	for _, field := range []string{"name", "description"} {
		for _, corrected := range []bool{true, false} {
			name := field + "_repeated"
			if corrected {
				name = field + "_corrected"
			}
			t.Run(name, func(t *testing.T) {
				duplicate := conversationSkillDraftArgumentsForTest(t, func(fields map[string]any) {
					fields["skill_markdown"] = "---\nname: typed-fixture-workflow\ndescription: Fixture workflow.\n" +
						field + ": Duplicate value.\n---\nValidate the provided inputs."
				})
				replies := []conversationSkillAuthoringReply{
					conversationSkillToolReplyForTest(duplicate),
					conversationSkillToolReplyForTest(duplicate),
				}
				wantStatus := http.StatusBadGateway
				if corrected {
					replies[1] = conversationSkillToolReplyForTest(conversationSkillDraftArgumentsForTest(t, nil))
					wantStatus = http.StatusCreated
				}
				fixture := newConversationSkillAuthoringFixture(t, replies)
				response := fixture.save(context.Background())
				if response.Code != wantStatus || len(fixture.capturedRequests()) != 2 {
					t.Fatalf("duplicate frontmatter save status=%d want=%d requests=%d body=%s",
						response.Code, wantStatus, len(fixture.capturedRequests()), response.Body.String())
				}
				if !corrected {
					fixture.assertNoInstallation(t)
					return
				}
				if _, found := findCatalogSkill(fixture.server.skillCatalog, "typed-fixture-workflow"); !found {
					t.Fatal("corrected manifest was not installed")
				}
			})
		}
	}
}
