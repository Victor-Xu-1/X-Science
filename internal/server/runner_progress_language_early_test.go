package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"x-science/internal/agentruntime"
)

type earlyProgressLanguageModel struct {
	primary     earlyProgressModel
	translation string
	active      bool
	t           *testing.T
}

func (model *earlyProgressLanguageModel) Complete(ctx context.Context, request agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	return model.CompleteStream(ctx, request, nil)
}

func (model *earlyProgressLanguageModel) CompleteStream(ctx context.Context, request agentruntime.ModelRequest, emit func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, error) {
	if model.active {
		model.t.Fatal("recursive presentation call inside active provider callback")
	}
	if request.ReasoningMode == agentruntime.ReasoningModeDisabled {
		if len(request.Messages) != 2 || request.Messages[0].Content != simplifiedChineseTranslationInstruction {
			model.t.Fatal("translation acquired a competing progress instruction")
		}
		return agentruntime.ModelResponse{Message: agentruntime.Message{Content: model.translation}}, nil
	}
	model.active = true
	defer func() { model.active = false }()
	return model.primary.CompleteStream(ctx, request, emit)
}

func TestEarlyProgressLanguagePreservesOrderWithoutConcurrentTranslation(t *testing.T) {
	const english = "I will inspect the available source records before comparing their measurements."
	const translated = "我会先检查现有来源记录，然后比较它们的测量结果。"
	const chinese = "之后我会核对记录范围。"
	for _, first := range []string{english, chinese} {
		t.Run(first[:3], func(t *testing.T) {
			one, two := earlyProgressEnvelope("one", first), earlyProgressEnvelope("two", chinese)
			var public strings.Builder
			boundaries := 0
			model := &earlyProgressLanguageModel{t: t, translation: translated,
				primary: earlyProgressModel{chunks: []string{one, two}, response: earlyProgressToolResponse(one + two)}}
			model.primary.afterChunk = func(index int) {
				want := index + 1
				if first == english {
					want = 0
				}
				if boundaries != want {
					t.Fatalf("reordered/delayed ready prefix: boundaries=%d want=%d", boundaries, want)
				}
			}
			client := &sessionRunnerResponseLanguageModelClient{language: "zh", delegate: &sessionRunnerResponseContractClient{delegate: model}}
			response, err := client.CompleteStream(context.Background(), agentruntime.ModelRequest{}, func(event agentruntime.ModelStreamEvent) error {
				if event.Kind == agentruntime.ModelStreamEventPublicProgressDelta {
					public.WriteString(event.ContentDelta)
				}
				if event.Kind == agentruntime.ModelStreamEventPublicProgressBoundary {
					boundaries++
				}
				return nil
			})
			want := first + chinese
			if first == english {
				want = translated + chinese
			}
			if err != nil || boundaries != 2 || public.String() != want || response.Message.Content != want || model.primary.calls != 1 {
				t.Fatalf("public=%q terminal=%q boundaries=%d primary=%d err=%v", public.String(), response.Message.Content, boundaries, model.primary.calls, err)
			}
		})
	}
}

func TestEarlyProgressTimingSurvivesBeforeObserverPendingRecordExists(t *testing.T) {
	start := time.Date(2026, 10, 14, 8, 0, 0, 0, time.UTC)
	now := start
	raw := earlyProgressEnvelope("early", "我会先核对输入记录。")
	var record map[string]any
	published := 0
	observer := &sessionRunnerCommunicationObserver{now: func() time.Time { return now },
		publicationBytes: func() int { return published }, audit: func(value map[string]any) { record = value }}
	observer.delegate = communicationTimingModel{run: func(_ context.Context, emit func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, error) {
		now = start.Add(time.Second)
		if err := emit(agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventContentDelta, ContentDelta: raw}); err != nil {
			return agentruntime.ModelResponse{}, err
		}
		if observer.pending != nil || published == 0 {
			t.Fatal("progress waited for pending terminal record")
		}
		now = start.Add(40 * time.Second)
		return earlyProgressToolResponse(raw), nil
	}}
	contract := &sessionRunnerResponseContractClient{delegate: observer, progressReady: observer.noteProgressReady}
	client := &sessionRunnerResponseLanguageModelClient{delegate: contract, language: "zh", presentationReady: observer.notePresentationReady}
	var block string
	_, err := client.CompleteStream(context.Background(), agentruntime.ModelRequest{}, func(event agentruntime.ModelStreamEvent) error {
		if event.Kind == agentruntime.ModelStreamEventPublicProgressDelta {
			block += event.ContentDelta
		}
		if event.Kind == agentruntime.ModelStreamEventPublicProgressBoundary {
			published += len(block)
			observer.notePublication() // Same post-durable-boundary hook as production.
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	observer.recordBoundary(true)
	for _, name := range []string{"first_native_content_at", "first_progress_ready_at", "presentation_ready_at", "first_publication_at"} {
		assertCommunicationTime(t, record, name, start.Add(time.Second))
	}
	assertCommunicationTime(t, record, "response_finished_at", start.Add(40*time.Second))
}
