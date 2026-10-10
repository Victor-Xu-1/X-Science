package toolgateway

import (
	"context"
	"reflect"
	"testing"
)

func TestNewInvocationDefaultsContextAndContinuation(t *testing.T) {
	invocation := NewInvocation(nil, "call-1", "web_search", nil, "ext")
	if invocation.Context == nil {
		t.Fatal("nil parent context must become a non-nil background context")
	}
	if invocation.CallID != "call-1" || invocation.RequestedName != "web_search" || invocation.Extension != "ext" {
		t.Fatalf("invocation=%#v", invocation)
	}
	if invocation.ContinuationState() != Continue {
		t.Fatalf("fresh continuation=%d want Continue", invocation.ContinuationState())
	}
}

func TestCompleteForAuditShortCircuitsDirectlyToAudit(t *testing.T) {
	var observed []Stage
	steps := make([]Step, 0, len(OrderedStages))
	for _, stage := range OrderedStages {
		stage := stage
		steps = append(steps, Step{Stage: stage, Run: func(invocation *Invocation) {
			observed = append(observed, stage)
			if stage == StagePreflight {
				invocation.CompleteForAudit(map[string]any{"ok": false, "preflight": true}, "rejected", "admission rejected", nil)
			}
		}})
	}
	pipeline := MustPipeline(steps...)
	invocation := NewInvocation(context.Background(), "call-audit", "python", nil, nil)
	pipeline.Run(invocation)
	want := []Stage{StageNormalize, StageAdmit, StagePreflight, StageAudit}
	if !reflect.DeepEqual(observed, want) {
		t.Fatalf("audit-only stages=%v want=%v", observed, want)
	}
	if invocation.Status != "rejected" || invocation.ErrorMessage != "admission rejected" {
		t.Fatalf("terminal invocation=%#v", invocation)
	}
}

func TestNewPipelineRejectsDuplicateStageHandlers(t *testing.T) {
	steps := make([]Step, 0, len(OrderedStages))
	for _, stage := range OrderedStages {
		steps = append(steps, Step{Stage: stage, Run: func(*Invocation) {}})
	}
	steps[0] = Step{Stage: StageNormalize, Run: func(*Invocation) {}}
	steps[1] = Step{Stage: StageNormalize, Run: func(*Invocation) {}}
	if _, err := NewPipeline(steps...); err == nil {
		t.Fatal("duplicate stage handler was accepted")
	}
}
