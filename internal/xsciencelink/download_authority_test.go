package xsciencelink

import (
	"context"
	"testing"
)

func TestBrowserDownloadCommandRequiresOwnerAndApproval(t *testing.T) {
	service := NewService()
	if err := service.Register(Client{ID: "owned-browser", UserID: "owner", Kind: "browser", Capabilities: []string{"downloads"}, SupportedActions: []string{"download_file"}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const privateResource = "http://127.0.0.1:8123/intended-local-resource.csv"
	for _, test := range []struct {
		user     string
		approved bool
	}{
		{"other-user", true}, {"owner", false},
	} {
		if _, _, err := service.SendCommand(ctx, test.user, "owned-browser", "download_file", map[string]any{"url": privateResource, "approved": test.approved}); err == nil {
			t.Fatalf("unauthorized download admitted: %+v", test)
		}
		if pending := service.PendingCommands("owned-browser"); len(pending) != 0 {
			t.Fatalf("rejected download queued: %v", pending)
		}
	}
	command, result, err := service.SendCommand(ctx, "owner", "owned-browser", "download_file", map[string]any{"url": privateResource, "approved": true})
	if err != nil || command.Payload["url"] != privateResource {
		t.Fatalf("intentional approved local download changed: %v %v", command, err)
	}
	if err := service.CompleteCommand("owned-browser", command.ID, map[string]any{"completed": true}); err != nil {
		t.Fatal(err)
	}
	<-result
}
