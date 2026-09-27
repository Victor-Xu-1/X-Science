package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestOfficeArtifactFixtureUsesStoreImmutableVersions(t *testing.T) {
	store := fixtureStore(t, "completed")
	request := fixtureRequest{
		Action: "seed-office-artifact", FrameID: "frame",
		OfficeArtifact: &officeArtifactFixtureRequest{
			ArtifactID: "office artifact/中文 100%",
			Versions:   [][]byte{[]byte("%PDF-1.4\nfirst"), []byte("%PDF-1.4\nsecond")},
		},
	}
	response, err := seedOfficeArtifactFixture(store, request)
	if err != nil {
		t.Fatal(err)
	}
	firstID := response["versionId"].(string)
	latestID := response["latestVersionId"].(string)
	if firstID == "" || firstID == latestID {
		t.Fatalf("invalid immutable versions: %#v", response)
	}
	for index, versionID := range []string{firstID, latestID} {
		artifact, version, found, err := store.GetArtifactVersion(versionID)
		if err != nil || !found || artifact.ID != request.OfficeArtifact.ArtifactID ||
			version.VersionNumber != index+1 || !bytes.Equal(version.Content, request.OfficeArtifact.Versions[index]) {
			t.Fatalf("persisted version %d: artifact=%#v version=%#v found=%t err=%v", index, artifact, version, found, err)
		}
	}
	_, current, found, err := store.GetCurrentArtifactVersion(request.OfficeArtifact.ArtifactID)
	if err != nil || !found || current.ID != latestID || current.ParentID != firstID {
		t.Fatalf("latest version=%#v found=%t err=%v", current, found, err)
	}
	if _, err := seedOfficeArtifactFixture(store, request); err == nil {
		t.Fatal("existing artifact was overwritten")
	}
}

func TestOfficeArtifactFixtureRequestBoundaries(t *testing.T) {
	valid := fixtureRequest{Action: "seed-office-artifact", FrameID: "frame", OfficeArtifact: &officeArtifactFixtureRequest{
		ArtifactID: "artifact/中文 with space", Versions: [][]byte{[]byte("%PDF-1.4"), []byte("%PDF-1.4")},
	}}
	for _, mutation := range []struct {
		name   string
		change func(*fixtureRequest)
	}{
		{"missing frame", func(r *fixtureRequest) { r.FrameID = "" }},
		{"missing payload", func(r *fixtureRequest) { r.OfficeArtifact = nil }},
		{"wrong action", func(r *fixtureRequest) { r.Action = "seed-delegate" }},
		{"mixed fields", func(r *fixtureRequest) { r.HistoryCount = 6 }},
		{"non PDF", func(r *fixtureRequest) { r.OfficeArtifact.Versions[0] = []byte("not PDF") }},
		{"too large", func(r *fixtureRequest) { r.OfficeArtifact.Versions[0] = []byte("%PDF-" + strings.Repeat("a", 64*1024)) }},
		{"control ID", func(r *fixtureRequest) { r.OfficeArtifact.ArtifactID = "artifact\n" }},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			encoded, _ := json.Marshal(valid)
			var request fixtureRequest
			if err := json.Unmarshal(encoded, &request); err != nil {
				t.Fatal(err)
			}
			mutation.change(&request)
			encoded, _ = json.Marshal(request)
			if _, err := decodeRequest(bytes.NewReader(encoded)); err == nil {
				t.Fatal("invalid request accepted")
			}
		})
	}
	encoded, _ := json.Marshal(valid)
	if _, err := decodeRequest(bytes.NewReader(encoded)); err != nil {
		t.Fatal(err)
	}
}
