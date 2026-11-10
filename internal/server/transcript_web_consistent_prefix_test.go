package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

// Reuse the existing real Store/projector and catch-up ownership fixtures.
// The flight is held at an explicit synchronization boundary, not by a fake
// slow history, artificial scientific job, or another projection implementation.
func TestTranscriptWebConsistentPrefixPageDoesNotJoinOwnedCatchup(t *testing.T) {
	for _, state := range []string{"ready", "building"} {
		t.Run(state, func(t *testing.T) {
			var f transcriptWebReadModelServingFixture
			if state == "building" {
				f = newTranscriptWebBuildingCatchupFixture(t, 2*transcriptWebIncrementalBatch+1)
			} else {
				f = newTranscriptWebReadModelServingFixture(t, 3)
				if _, _, created, err := f.repository.AppendFrameUserEvent(t.Context(), transcriptstore.AppendFrameUserEventInput{
					StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, ClientMessageID: "prefix-followup",
					FrameEventID: "prefix-followup-event", MessageUUID: "prefix-followup-message", Text: "new retained input",
				}); err != nil || !created {
					t.Fatalf("append-only source: created=%t err=%v", created, err)
				}
			}
			fence := requireTranscriptWebReadModelFence(t, f.readModel, f.stream, f.fence.BranchID)
			var dirty int
			if err := f.db.QueryRow(`SELECT COUNT(*) FROM transcript_web_projection_dirty WHERE stream_uid=? AND branch_id=?`,
				f.stream.UID, fence.BranchID).Scan(&dirty); err != nil {
				t.Fatal(err)
			}
			if fence.StateStatus != state || fence.StateProjectorVersion != transcriptstore.TranscriptWebProjectorVersion ||
				fence.StateBranchGeneration != fence.BranchGeneration || fence.StateSourceRevision != fence.SourceRevision ||
				fence.StateThroughPublicationSequence <= 0 || fence.StateThroughPublicationSequence >= fence.ThroughPublicationSequence ||
				fence.VisibleMessageCount == 0 || dirty != 0 {
				t.Fatalf("fixture is not a clean append-only canonical prefix: dirty=%d fence=%#v", dirty, fence)
			}
			var tailID string
			if err := f.db.QueryRow(`SELECT message_id FROM transcript_web_messages WHERE stream_uid=? AND branch_id=? AND visible=1 ORDER BY visible_index DESC LIMIT 1`,
				f.stream.UID, fence.BranchID).Scan(&tailID); err != nil {
				t.Fatal(err)
			}
			before := readTranscriptWebProjectionClock(t, f.db, f.stream.UID, fence.BranchID)
			key := transcriptWebCatchupKey{ownerID: f.stream.OwnerID, streamUID: f.stream.UID, branchID: fence.BranchID}
			flight, leader := f.server.startTranscriptWebCatchupFlight(key, transcriptWebCatchupBackground)
			if !leader {
				t.Fatal("fixture did not own the existing catch-up flight")
			}
			var release sync.Once
			finish := func() { release.Do(func() { f.server.finishTranscriptWebCatchupFlight(key, flight, nil) }) }
			t.Cleanup(finish)
			request := newLoopbackTestRequest(http.MethodGet, "/api/conversations/"+transcriptWebServingFrameID+"/messages?limit=1&consistency=prefix", nil)
			request.RemoteAddr = "127.0.0.1:12345"
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				response := httptest.NewRecorder()
				f.server.Handler().ServeHTTP(response, request)
				done <- response
			}()
			var response *httptest.ResponseRecorder
			select {
			case response = <-done:
			case <-time.After(time.Second):
				finish()
				select {
				case response = <-done:
				case <-time.After(time.Second):
					t.Fatal("read did not settle after releasing the owned flight")
				}
				t.Fatalf("progressive read joined catch-up despite an existing canonical prefix: HTTP%d body=%s", response.Code, response.Body.String())
			}
			if response.Code != http.StatusOK {
				t.Fatalf("clean prefix not served: HTTP%d body=%s", response.Code, response.Body.String())
			}
			page := p3DecodeObject(t, response)
			items := transcriptWebServingItems(t, page, "items")
			if len(items) != 1 || webString(items[0].(map[string]any)["id"]) != tailID {
				t.Fatalf("response is not the actual nonempty stored prefix: %#v", page)
			}
			sourceRevision, sourcePresent := page["source_revision"]
			if page["projection_pending"] != true || !sourcePresent || int64(numberValue(sourceRevision)) != fence.SourceRevision ||
				int64(numberValue(page["through_publication_sequence"])) != fence.StateThroughPublicationSequence ||
				int64(numberValue(page["canonical_through_publication_sequence"])) != fence.ThroughPublicationSequence {
				t.Fatalf("page manufactured complete-head coverage: %#v", page)
			}
			rawCursor, err := base64.RawURLEncoding.DecodeString(webString(page["newest_cursor"]))
			var cursor map[string]any
			if err != nil || json.Unmarshal(rawCursor, &cursor) != nil || numberValue(cursor["version"]) != 2 ||
				cursor["source_revision"] == nil || int64(numberValue(cursor["source_revision"])) != fence.SourceRevision ||
				int64(numberValue(cursor["through_publication_sequence"])) != fence.StateThroughPublicationSequence {
				t.Fatalf("prefix cursor is not source-revision bound: %s %v", rawCursor, err)
			}
			if after := readTranscriptWebProjectionClock(t, f.db, f.stream.UID, fence.BranchID); after != before {
				t.Fatalf("serving a prefix rewrote projection state: before=%#v after=%#v", before, after)
			}
			select {
			case <-flight.done:
				t.Fatal("page read settled another owner's projection flight")
			default:
			}
		})
	}
}

func newTranscriptWebReadyPrefixFixture(t *testing.T) transcriptWebReadModelServingFixture {
	t.Helper()
	f := newTranscriptWebReadModelServingFixture(t, 3)
	if _, _, created, err := f.repository.AppendFrameUserEvent(t.Context(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, ClientMessageID: "prefix-control",
		FrameEventID: "prefix-control-event", MessageUUID: "prefix-control-message", Text: "append-only input",
	}); err != nil || !created {
		t.Fatalf("append: created=%t err=%v", created, err)
	}
	return f
}

// Exercise the repository's serving transaction after the preliminary server
// fence. A stale fence must not authorize rows or identity misses after change.
func TestTranscriptWebConsistentPrefixRepositoryRepeatsSourceFence(t *testing.T) {
	for _, change := range []string{"none", "exact", "dirty", "source_revision", "generation", "owner", "epoch", "quarantined", "projector", "coverage"} {
		t.Run(change, func(t *testing.T) {
			f := newTranscriptWebReadyPrefixFixture(t)
			fence := requireTranscriptWebReadModelFence(t, f.readModel, f.stream, f.fence.BranchID)
			input := transcriptstore.TranscriptWebMessagePageInput{OwnerID: f.stream.OwnerID, StreamUID: f.stream.UID,
				BranchID: fence.BranchID, BranchGeneration: fence.BranchGeneration,
				ThroughPublicationSequence: fence.StateThroughPublicationSequence, SourceRevision: fence.SourceRevision,
				Limit: 1, AllowConsistentPrefix: true}
			var query string
			switch change {
			case "exact":
				input.AllowConsistentPrefix = false
			case "dirty":
				query = `INSERT INTO transcript_web_projection_dirty(stream_uid,branch_id,source_revision,first_affected_ordinal,reason_mask) VALUES(?,?,1,1,1)`
			case "source_revision":
				// Repair a synthetic source event through the existing trigger,
				// then isolate the source-revision fence from the dirty marker.
				result, err := f.db.Exec(`UPDATE transcript_events SET event_type=event_type || '_repaired' WHERE stream_uid=? AND client_message_id='prefix-control'`, f.stream.UID)
				if err != nil {
					t.Fatal(err)
				}
				if count, err := result.RowsAffected(); err != nil || count != 1 {
					t.Fatalf("source repair count=%d err=%v", count, err)
				}
				query = `DELETE FROM transcript_web_projection_dirty WHERE stream_uid=? AND branch_id=?`
			case "generation":
				query = `UPDATE transcript_branch_state SET generation=generation+1 WHERE stream_uid=? AND active_branch_id=?`
			case "owner":
				input.OwnerID = "foreign-owner"
			case "epoch":
				// A real second-epoch stream exists but is not the active Frame
				// authority. Preserve all FK and transition guards.
				inactive, err := f.repository.CreateStream(t.Context(), transcriptstore.CreateStreamInput{
					UID: f.stream.UID + "-epoch2", OwnerID: f.stream.OwnerID, ExternalID: f.stream.ExternalID + "-epoch2",
					SessionID: f.stream.SessionID, Kind: f.stream.Kind, ProjectID: f.stream.ProjectID,
					RootFrameID: f.stream.RootFrameID, FrameID: f.stream.FrameID, Epoch: 2,
				})
				if err != nil || inactive.Epoch != 2 {
					t.Fatalf("inactive epoch fixture=%#v err=%v", inactive, err)
				}
				input.StreamUID = inactive.UID
			case "quarantined":
				query = `UPDATE transcript_web_projection_state SET status='quarantined',last_error_code='prefix-control-quarantine' WHERE stream_uid=? AND branch_id=?`
			case "projector":
				query = `UPDATE transcript_web_projection_state SET projector_version=projector_version-1 WHERE stream_uid=? AND branch_id=?`
			case "coverage":
				input.ThroughPublicationSequence++
			}
			if query != "" {
				if _, err := f.db.Exec(query, f.stream.UID, fence.BranchID); err != nil {
					t.Fatal(err)
				}
			}
			page, found, err := f.readModel.GetTranscriptWebMessageRange(t.Context(), input)
			identity := transcriptstore.TranscriptWebMessageIdentityInput{OwnerID: input.OwnerID, StreamUID: input.StreamUID,
				BranchID: input.BranchID, BranchGeneration: input.BranchGeneration, ThroughPublicationSequence: input.ThroughPublicationSequence,
				SourceRevision: input.SourceRevision, Identity: "not-yet-covered", AllowConsistentPrefix: input.AllowConsistentPrefix,
				ExpectedCanonicalThrough: &fence.ThroughPublicationSequence}
			_, _, locateErr := f.readModel.LocateTranscriptWebMessage(t.Context(), identity)
			if change == "none" {
				if err != nil || !found || len(page.Messages) != 1 || !page.ProjectionPending || page.CanonicalThroughPublicationSequence != fence.ThroughPublicationSequence || locateErr != nil {
					t.Fatalf("real clean prefix: found=%t page=%#v err=%v locate=%v", found, page, err, locateErr)
				}
			} else if change == "epoch" && (!errors.Is(err, transcriptstore.ErrBranchStateStale) || !errors.Is(locateErr, transcriptstore.ErrBranchStateStale)) {
				t.Fatalf("inactive epoch was not rejected at source authority: range=%v locate=%v", err, locateErr)
			} else if err == nil || locateErr == nil {
				t.Fatalf("old preliminary fence authorized %s: found=%t err=%v locate=%v", change, found, err, locateErr)
			}
		})
	}
	t.Run("identity miss rechecks canonical head", func(t *testing.T) {
		f := newTranscriptWebReadyPrefixFixture(t)
		fence := requireTranscriptWebReadModelFence(t, f.readModel, f.stream, f.fence.BranchID)
		if _, _, _, err := f.repository.AppendFrameUserEvent(t.Context(), transcriptstore.AppendFrameUserEventInput{
			StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, ClientMessageID: "prefix-race", FrameEventID: "prefix-race-event",
			MessageUUID: "prefix-race-message", Text: "new canonical head",
		}); err != nil {
			t.Fatal(err)
		}
		_, _, err := f.readModel.LocateTranscriptWebMessage(t.Context(), transcriptstore.TranscriptWebMessageIdentityInput{
			OwnerID: f.stream.OwnerID, StreamUID: f.stream.UID, BranchID: fence.BranchID, BranchGeneration: fence.BranchGeneration,
			ThroughPublicationSequence: fence.StateThroughPublicationSequence, SourceRevision: fence.SourceRevision,
			Identity: "unknown-anchor", AllowConsistentPrefix: true, ExpectedCanonicalThrough: &fence.ThroughPublicationSequence,
		})
		if !errors.Is(err, transcriptstore.ErrTranscriptWebProjectionStale) {
			t.Fatalf("old complete-head miss was definitive: %v", err)
		}
	})
}

func TestTranscriptWebConsistentPrefixCursorAndAnchorContracts(t *testing.T) {
	f := newTranscriptWebReadyPrefixFixture(t)
	request := func(query string) *httptest.ResponseRecorder {
		r := newLoopbackTestRequest(http.MethodGet, "/api/conversations/"+transcriptWebServingFrameID+"/messages?"+query, nil)
		r.RemoteAddr = "127.0.0.1:12345"
		w := httptest.NewRecorder()
		f.server.Handler().ServeHTTP(w, r)
		return w
	}
	pageResponse := request("consistency=prefix&limit=1")
	if pageResponse.Code != 200 {
		t.Fatalf("prefix: %d %s", pageResponse.Code, pageResponse.Body.String())
	}
	page := p3DecodeObject(t, pageResponse)
	cursor := webString(page["newest_cursor"])
	raw, _ := base64.RawURLEncoding.DecodeString(cursor)
	var decoded transcriptBranchMessageCursor
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.SourceRevision == nil || *decoded.SourceRevision != 0 {
		t.Fatalf("zero source revision missing: %s %v", raw, err)
	}
	t.Run("unknown anchor is pending", func(t *testing.T) {
		w := request("consistency=prefix&anchor_message_id=not-yet-covered")
		if w.Code != 503 || p3DecodeObject(t, w)["code"] != "HISTORY_NOT_READY" {
			t.Fatalf("unknown anchor: %d %s", w.Code, w.Body.String())
		}
	})
	t.Run("v1 cannot become prefix", func(t *testing.T) {
		decoded.Version = 1
		decoded.SourceRevision = nil
		raw, _ := json.Marshal(decoded)
		w := request("consistency=prefix&before=" + url.QueryEscape(base64.RawURLEncoding.EncodeToString(raw)))
		if w.Code != 409 {
			t.Fatalf("v1 prefix cursor: %d %s", w.Code, w.Body.String())
		}
	})
	t.Run("stale source cannot translate through old view", func(t *testing.T) {
		revision := int64(1)
		decoded.Version = 2
		decoded.SourceRevision = &revision
		raw, _ := json.Marshal(decoded)
		w := request("consistency=prefix&before=" + url.QueryEscape(base64.RawURLEncoding.EncodeToString(raw)))
		if w.Code != 409 {
			t.Fatalf("source cursor: %d %s", w.Code, w.Body.String())
		}
	})
	t.Run("bad mode", func(t *testing.T) {
		if w := request("consistency=unknown"); w.Code != 400 {
			t.Fatalf("mode: %d", w.Code)
		}
	})
	// Exact remains the original repository policy. After the same real projector
	// catches up, v1 normal cursors work and prefix reports complete, never pending.
	if err := f.server.catchUpTranscriptWebReadModel(t.Context(), f.stream.OwnerID, f.stream.UID, f.fence.BranchID); err != nil {
		t.Fatal(err)
	}
	exact := request("limit=1")
	if exact.Code != 200 {
		t.Fatalf("exact: %d %s", exact.Code, exact.Body.String())
	}
	exactPage := p3DecodeObject(t, exact)
	if exactPage["projection_pending"] != nil {
		t.Fatal("default exact contract changed")
	}
	if w := request("before=" + url.QueryEscape(webString(exactPage["newest_cursor"]))); w.Code != 200 {
		t.Fatalf("v1 normal: %d %s", w.Code, w.Body.String())
	}
	complete := request("consistency=prefix&limit=1")
	completePage := p3DecodeObject(t, complete)
	if complete.Code != 200 || completePage["projection_pending"] != false || completePage["through_publication_sequence"] != completePage["canonical_through_publication_sequence"] {
		t.Fatalf("caught up: %d %#v", complete.Code, completePage)
	}
}
