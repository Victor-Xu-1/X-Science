package server

import (
	"context"
	"testing"

	adaptercommon "x-science/internal/adapters/common"
)

func TestServerDisabledIMDispatcherRemainsAbsentThroughStartupRecovery(t *testing.T) {
	defer func() {
		if failure := recover(); failure != nil {
			t.Errorf("the real disabled-IM New/recovery path panicked: %v", failure)
		}
	}()
	// The native dispatcher factory returns this typed nil when no IM adapter
	// is enabled; main passes it through Options' existing interface field.
	var disabled *adaptercommon.SessionOutboundDispatcher
	app := New(Options{FileRoot: t.TempDir(), IMOutbound: disabled})
	t.Cleanup(func() { _ = app.Close(context.Background()) })
	if app.imOutbound != nil {
		t.Error("disabled dispatcher was not normalized at the shared constructor")
	}
	// A disabled channel must not even decode or mutate old persisted routes.
	if _, err := app.runtimeStore.Set(imOutboundRouteNamespace, "disabled-route", map[string]any{"schemaVersion": 999}); err != nil {
		t.Fatal(err)
	}
	report, err := app.RecoverIMOutbound(t.Context())
	if err != nil || report.Routes != 0 || report.Restored != 0 || report.Skipped != 0 || len(report.Warnings) != 0 {
		t.Errorf("disabled startup entered an IM recovery path: report=%+v err=%v", report, err)
	}
	if _, found, err := app.runtimeStore.Get(imOutboundRouteNamespace, "disabled-route"); err != nil || !found {
		t.Fatalf("disabled recovery changed retained route: found=%t err=%v", found, err)
	}
	disabled.SetRecoveryNoticeValidator(nil) // The exported setter is also nil-safe.
}
