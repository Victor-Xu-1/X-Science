package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	transcriptstore "synon-go/internal/persistence/transcript"
)

func TestRoundSummaryInactiveBranchRetainsCompletedReply(t *testing.T) {
	for _, readModel := range []bool{false, true} {
		t.Run(fmt.Sprintf("read_model_%t", readModel), func(t *testing.T) {
			testRoundSummaryInactiveBranch(t, readModel)
		})
	}
}

func testRoundSummaryInactiveBranch(t *testing.T, readModel bool) {
	f := newAgentSaveArtifactsFixture(t)
	if !readModel {
		f.server.transcriptWebReadModel = nil
	}
	finishRoundPresentation(t, f, "completed")
	base, err := f.repo.GetBranchState(context.Background(), f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/conversations/" + f.stream.FrameID + "/messages?limit=80&branch_id=" + url.QueryEscape(base.ActiveBranchID)
	before := p3JSONRequest(t, f.server, http.MethodGet, path, nil, f.stream.OwnerID)
	if before.Code != http.StatusOK || !strings.Contains(before.Body.String(), `"round_summary":`) {
		t.Fatalf("active branch must initially expose the completed summary: %d %s", before.Code, before.Body.String())
	}
	_, err = f.repo.ForkFrameUserMessageBranch(context.Background(), transcriptstore.ForkFrameUserMessageBranchInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, SourceBranchID: base.ActiveBranchID,
		ExpectedActiveBranchID: base.ActiveBranchID, ExpectedGeneration: base.Generation,
		ClientMutationID: "round-summary-sibling", SourceClientMessageID: "save-user", SourceMessageIndex: 0,
		ReplacementText: "Separate sibling input", Destinations: []string{"ws"},
	})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := f.repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, RunnerID: "round-summary-runner",
		TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claim.Claimed {
		t.Fatalf("claim: %+v %v", claim, err)
	}
	f.claim = claim.Claim
	finishRoundPresentation(t, f, "completed")
	after := p3JSONRequest(t, f.server, http.MethodGet, path, nil, f.stream.OwnerID)
	if after.Code != http.StatusOK {
		t.Fatalf("inactive branch fetch: %d %s", after.Code, after.Body.String())
	}
	if !strings.Contains(after.Body.String(), `"round_summary":`) {
		t.Fatal("same completed reply loses round_summary when its branch becomes inactive")
	}
	if _, err := f.repo.CompletedRoundUsageAuthorities(context.Background(), f.stream.UID, "foreign-owner", base.ActiveBranchID, []int64{1}); err == nil {
		t.Fatal("foreign owner obtained branch usage authority")
	}
	if _, err := f.repo.CompletedRoundUsageAuthorities(context.Background(), f.stream.UID, f.stream.OwnerID, "missing-branch", []int64{1}); err == nil {
		t.Fatal("unknown branch silently fell back to active branch")
	}
}
