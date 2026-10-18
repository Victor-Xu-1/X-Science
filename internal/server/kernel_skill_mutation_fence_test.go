package server

import (
	"context"
	"strings"
	"testing"
	"time"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestKernelSkillMutationsRejectStaleRunnerAndKernel(t *testing.T) {
	for _, method := range []string{"host.skills.edit", "host.skills.publish", "host.skills.delete"} {
		for _, stale := range []string{"runner_claim", "kernel_generation", "missing_binding"} {
			t.Run(strings.TrimPrefix(method, "host.skills.")+"/"+stale, func(t *testing.T) {
				fixture := newKernelSkillDraftFixture(t)
				const name = "fenced-draft-fixture"
				if _, err := fixture.call("host.skills.edit", name, "SKILL.md", kernelSkillManifestForTest(name, "Original draft workflow.")); err != nil {
					t.Fatal(err)
				}
				if method == "host.skills.delete" {
					if _, err := fixture.call("host.skills.publish", name, false); err != nil {
						t.Fatal(err)
					}
				}
				if stale == "kernel_generation" {
					fixture.generation++
				} else if stale == "missing_binding" {
					fixture.bound.transcriptExecution = kernelTranscriptExecutionBinding{}
				} else {
					interruption, err := fixture.repo.InterruptRunner(context.Background(), transcriptstore.InterruptRunnerInput{
						Claim: fixture.claim, ClientMessageID: "skill-runner-interruption", ReasonCode: "runtime_draining", Resumable: true,
					})
					if err != nil {
						t.Fatal(err)
					}
					reclaimed, err := fixture.repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{
						StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID, RunnerID: fixture.claim.RunnerID,
						TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceCheckpoint,
						ResumeCheckpoint: interruption.Checkpoint.Sequence,
					})
					if err != nil || !reclaimed.Claimed || reclaimed.Claim.ClaimToken == fixture.claim.ClaimToken {
						t.Fatalf("replacement claim=%#v err=%v", reclaimed, err)
					}
				}
				args := []any{name, false}
				if method == "host.skills.edit" {
					args = []any{name, "SKILL.md", "Unauthorized stale update.", "Original draft workflow."}
				}
				if method == "host.skills.delete" {
					args = []any{name}
				}
				if result, err := fixture.call(method, args...); err == nil {
					t.Fatalf("stale %s succeeded: %#v", method, result)
				}
				read, err := fixture.call("host.skills.read", name)
				if err != nil || !strings.Contains(stringValue(mapValue(read)["content"]), "Original draft workflow.") {
					t.Fatalf("stale mutation changed draft=%#v err=%v", read, err)
				}
				_, published := findCatalogSkill(fixture.server.skillCatalog, name)
				if published != (method == "host.skills.delete") {
					t.Fatal("stale mutation changed live publication")
				}
			})
		}
	}
}
