package server

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	runtimekv "x-science/internal/persistence/runtimekv"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestNotificationInboxReadDoesNotConsumeAgentDeliveryOrResolveQuestion(t *testing.T) {
	f := newNotificationInboxFixture(t)
	stream, claim := f.begin(t, "inbox-owner", "pending-question")
	// The typed canonical AskUser contract requires 2..4 distinct options.
	_, _, err := f.store.ParkAskUserWithTranscript(context.Background(), workspace.ParkAskUserInput{
		FrameID: stream.FrameID, ToolID: "inbox-question", ToolName: "ask_user",
		Questions: []any{map[string]any{"question": "Which option?", "header": "Choice", "options": []any{
			map[string]any{"label": "One", "description": "First option"},
			map[string]any{"label": "Two", "description": "Second option"},
		}}},
	}, transcriptstore.AppendRunnerCheckpointInput{Claim: claim, ClientMessageID: "pause-question",
		Phase: transcriptstore.RunnerPhaseWaitingUser, Resumable: true,
		PayloadJSON: []byte(`{"status":"awaiting_user_response","detail":"waiting for the user"}`), Destinations: []string{"ws"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.store.CreateNotification(context.Background(), workspace.CreateNotificationInput{
		ID: "agent-only-notification", SenderFrameID: stream.FrameID, RecipientFrameID: stream.FrameID,
		RootFrameID: stream.FrameID, OwnerUserID: stream.OwnerID, NotificationType: "agent_message", Payload: map[string]any{"text": "private agent delivery"},
	}); err != nil {
		t.Fatal(err)
	}
	before, found, err := f.store.GetFrameRuntimeMetadata(stream.FrameID)
	if err != nil || !found {
		t.Fatal("pending source missing")
	}
	// No Web delivery is required to discover an already-durable question.
	snapshot := readNotificationInbox(t, f, stream.OwnerID)
	if len(snapshot.items) != 1 || snapshot.items[0]["kind"] != "task.needs-attention" || snapshot.items[0]["action_state"] != "pending" {
		t.Fatalf("actual pending question not restored=%#v", snapshot)
	}
	memoryCompatJSON(t, f.app.Handler(), http.MethodPost, notificationInboxTestPath+"/read", map[string]any{"ids": []any{snapshot.items[0]["id"]}}, stream.OwnerID, http.StatusOK)
	after, _, err := f.store.GetFrameRuntimeMetadata(stream.FrameID)
	if err != nil || !reflect.DeepEqual(before.ContextData, after.ContextData) {
		t.Fatal("human read altered pending question/approval authority")
	}
	unread, err := f.store.CountUnreadNotifications(context.Background(), stream.FrameID, stream.FrameID, stream.OwnerID)
	if err != nil || unread != 1 {
		t.Fatalf("human inbox consumed agent delivery: unread=%d err=%v", unread, err)
	}
}

func TestNotificationInboxProjectionFailureDoesNotBlockCanonicalWebDelivery(t *testing.T) {
	f := newNotificationInboxFixture(t)
	if _, err := f.runtime.Set(notificationInboxTestNamespace("inbox-owner"), "snapshot", map[string]any{"schema": "broken-inbox-state"}); err != nil {
		t.Fatal(err)
	}
	f.complete(t, "inbox-owner", "delivery-before-read")
	memoryCompatJSON(t, f.app.Handler(), http.MethodGet, notificationInboxTestPath, nil, "inbox-owner", http.StatusServiceUnavailable)
	f.complete(t, "inbox-owner", "delivery-after-read")
	turns := 0
	for _, event := range transcriptWebEvents(t, f.store, "inbox-owner") {
		if event.Type == "turn.completed" {
			turns++
		}
	}
	if turns != 2 {
		t.Fatalf("derived inbox failure blocked real canonical completions: %d", turns)
	}
}

func TestNotificationInboxRuntimeNamespaceIsProtectedFromGenericTools(t *testing.T) {
	f := newNotificationInboxFixture(t)
	namespace := notificationInboxTestNamespace("inbox-owner")
	for _, tool := range []string{"runtime_get", "runtime_set", "runtime_delete", "runtime_list"} {
		t.Run(tool, func(t *testing.T) {
			if _, err := f.runtime.Set(namespace, "snapshot", map[string]any{"private": "human inbox payload"}); err != nil {
				t.Fatal(err)
			}
			input := map[string]any{"namespace": namespace, "key": "snapshot", "value": map[string]any{"private": "replaced"}}
			if _, err := f.app.executeRuntimeTool(tool, input); err == nil || !strings.Contains(err.Error(), "reserved") {
				t.Fatalf("generic tool accessed human inbox: tool=%s err=%v", tool, err)
			}
			for _, variant := range []string{"  " + namespace + "\t", strings.ToUpper(namespace)} {
				input["namespace"] = variant
				if _, err := f.app.executeRuntimeTool(tool, input); err == nil || !strings.Contains(err.Error(), "reserved") {
					t.Fatalf("normalized namespace bypass: tool=%s err=%v", tool, err)
				}
			}
		})
	}
	if _, err := f.runtime.Set(namespace, "snapshot", map[string]any{"private": "human inbox payload"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runtime.Set(strings.ToUpper(namespace), "snapshot", map[string]any{"private": "case variant"}); err != nil {
		t.Fatal(err)
	}
	listed, err := f.app.executeRuntimeTool("runtime_list", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range mapValue(listed)["entries"].([]runtimekv.Entry) {
		if strings.HasPrefix(strings.ToLower(entry.Namespace), "notification-inbox.") {
			t.Fatal("unscoped runtime enumeration leaked a human inbox")
		}
	}
}
