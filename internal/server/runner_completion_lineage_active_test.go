package server

import (
	"reflect"
	"testing"

	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestSelectSessionRunnerActiveArtifactReferencesDropsSupersededContinuationOutputs(t *testing.T) {
	ref := func(artifactID, versionID string) transcriptstore.ArtifactReferenceInput {
		return transcriptstore.ArtifactReferenceInput{
			ArtifactID: artifactID, VersionID: versionID, Relation: transcriptstore.ArtifactRelationProduced,
		}
	}
	oldTable := ref("old-table", "old-table-v1")
	unchangedReport := ref("report", "report-v3")
	newTable := ref("new-table", "new-table-v2")
	commits := []transcriptstore.ArtifactReferenceInput{oldTable, unchangedReport, newTable}

	got := selectSessionRunnerActiveArtifactReferences(
		commits,
		map[string]struct{}{newTable.VersionID: {}},
		map[string]struct{}{unchangedReport.VersionID: {}},
		true,
		true,
	)
	want := []transcriptstore.ArtifactReferenceInput{unchangedReport, newTable}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("active refs=%#v, want %#v", got, want)
	}

	if independent := selectSessionRunnerActiveArtifactReferences(commits, nil, nil, false, false); len(independent) != 0 {
		t.Fatalf("independent no-output turn inherited refs=%#v", independent)
	}

	if continuation := selectSessionRunnerActiveArtifactReferences(commits, nil, nil, false, true); !reflect.DeepEqual(continuation, commits) {
		t.Fatalf("continuation no-signal refs=%#v, want full logical-task ledger", continuation)
	}
}

func TestSessionRunnerActiveDeliverablesRecoverOnlyUnmetRequirements(t *testing.T) {
	store := openRunnerArtifactCompletionStore(t)
	server := &Server{workspaceStore: store}
	save := func(id, project, name string) transcriptstore.ArtifactReferenceInput {
		t.Helper()
		artifact, version, err := store.SaveArtifactVersion(workspace.SaveArtifactVersionInput{
			ArtifactID: id, ProjectID: project, Name: name, Kind: "fixture", Content: []byte(id), CreatedBy: "runner",
		})
		if err != nil {
			t.Fatal(err)
		}
		return transcriptstore.ArtifactReferenceInput{
			ArtifactID: artifact.ID, VersionID: version.ID, Relation: transcriptstore.ArtifactRelationProduced,
		}
	}
	oldScene := save("historical-scene", "project-a", "scene.json")
	oldMarkdown := save("old-markdown", "project-a", "other.md")
	oldTable := save("old-table", "project-a", "prior-table.csv")
	newerTable := save("newer-table", "project-a", "newer-table.csv")
	oldReport := save("old-report", "project-a", "report.md")
	newReport := save("current-report", "project-a", "report.md")
	newTable := save("current-table", "project-a", "current-table.csv")
	current := save("current-artifact", "project-a", "result.bin")
	foreignTable := save("foreign-table", "project-b", "foreign.csv")
	workingTable := save("working-table", "project-a", "working.csv")
	if err := store.SetArtifactRetentionMode(t.Context(), workingTable.ArtifactID, "project-a", "local", "working_data"); err != nil {
		t.Fatal(err)
	}
	wrongBinding := newerTable
	wrongBinding.ArtifactID = "unrelated-artifact-id"
	generic := "Continue the comparison and save the results as a downloadable file."
	specific := "Save report.md and deliver a CSV."
	for _, test := range []struct {
		name, intent, reply    string
		history, current, want []transcriptstore.ArtifactReferenceInput
		missing                []string
	}{
		{
			name: "generic requirement is already fulfilled", intent: generic,
			history: []transcriptstore.ArtifactReferenceInput{oldScene, oldReport},
			current: []transcriptstore.ArtifactReferenceInput{current},
			want:    []transcriptstore.ArtifactReferenceInput{current},
		},
		{
			name: "explicit prior link is still retained", intent: generic,
			reply:   "Reuse the earlier report: {{artifact:" + oldReport.VersionID + "}}",
			history: []transcriptstore.ArtifactReferenceInput{oldScene, oldReport},
			current: []transcriptstore.ArtifactReferenceInput{current},
			want:    []transcriptstore.ArtifactReferenceInput{oldReport, current},
		},
		{
			name: "explicit prior link alone covers generic delivery", intent: generic,
			reply:   "Reuse the earlier report: {{artifact:" + oldReport.VersionID + "}}",
			history: []transcriptstore.ArtifactReferenceInput{oldScene, oldReport},
			want:    []transcriptstore.ArtifactReferenceInput{oldReport},
		},
		{
			name: "covered name and format do not recruit duplicates", intent: specific,
			history: []transcriptstore.ArtifactReferenceInput{oldScene, oldReport, oldTable},
			current: []transcriptstore.ArtifactReferenceInput{newReport, newTable},
			want:    []transcriptstore.ArtifactReferenceInput{newReport, newTable},
		},
		{
			name: "recover exact name before the format and pick one eligible table", intent: specific,
			history: []transcriptstore.ArtifactReferenceInput{oldScene, oldReport, oldMarkdown, oldTable, newerTable, foreignTable, workingTable},
			current: []transcriptstore.ArtifactReferenceInput{current},
			want:    []transcriptstore.ArtifactReferenceInput{oldReport, newerTable, current},
		},
		{
			name: "wrong artifact identity does not fulfill a format", intent: specific,
			history: []transcriptstore.ArtifactReferenceInput{oldScene, oldReport, wrongBinding, foreignTable, workingTable},
			current: []transcriptstore.ArtifactReferenceInput{current},
			want:    []transcriptstore.ArtifactReferenceInput{oldReport, current},
			missing: []string{"a verified .csv artifact"},
		},
		{
			name: "genuine continuation without a new delivery signal retains its ledger", intent: generic,
			history: []transcriptstore.ArtifactReferenceInput{oldScene, oldReport},
			want:    []transcriptstore.ArtifactReferenceInput{oldScene, oldReport},
		},
		{
			name: "concrete requirements alone narrow a no-current continuation", intent: specific,
			history: []transcriptstore.ArtifactReferenceInput{oldScene, oldReport, oldTable, newerTable},
			want:    []transcriptstore.ArtifactReferenceInput{oldReport, newerTable},
		},
		{
			name: "missing concrete requirements fail closed without current output", intent: specific,
			history: []transcriptstore.ArtifactReferenceInput{oldScene},
			want:    []transcriptstore.ArtifactReferenceInput{oldScene},
			missing: []string{"artifact report.md", "a verified .csv artifact", "a verified .md artifact"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			run := &sessionRunnerChatRun{
				Transcript: &transcriptRunnerAuthority{Stream: transcriptstore.Stream{ProjectID: "project-a"}},
				TaskIntent: test.intent, ContinuationArtifactReferences: test.history,
			}
			commits := append(append([]transcriptstore.ArtifactReferenceInput{}, test.history...), test.current...)
			round := make([]transcriptstore.ArtifactReference, 0, len(test.current))
			for _, ref := range test.current {
				round = append(round, transcriptstore.ArtifactReference{
					ArtifactID: ref.ArtifactID, VersionID: ref.VersionID, Relation: ref.Relation,
				})
			}
			got, err := server.sessionRunnerArtifactReferencesForRound(run, commits, test.reply, round)
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("active delivery refs=%#v, want %#v, error=%v", got, test.want, err)
			}
			missing, err := server.sessionRunnerMissingRequiredDeliverables(
				sessionstore.Session{Project: &sessionstore.Project{ID: "project-a"}}, run, got,
			)
			if err != nil || len(missing) != len(test.missing) || (len(missing) > 0 && !reflect.DeepEqual(missing, test.missing)) {
				t.Fatalf("missing required deliverables=%v, want %v, error=%v", missing, test.missing, err)
			}
		})
	}
}
