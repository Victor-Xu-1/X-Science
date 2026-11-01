package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/uuid"
	"x-science/internal/agentruntime"
	"x-science/internal/harnesscontract"
	kernelruntime "x-science/internal/kernel"
	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/persistence/workspace"
)

func TestAgentPDFAnnotationListHasReadOnlyGovernedExposure(t *testing.T) {
	f, _, gateway := pdfAnnotationGatewayForTest(t)
	const name = "list_pdf_annotations"
	ctx := withTranscriptRunnerChatRun(t.Context(), gateway.taskRun)
	schemas := f.server.agentRuntimeToolSchemasWithContextOptions(ctx, []string{name}, true, f.stream.FrameID)
	if !agentToolSchemasContain(schemas, name) {
		t.Fatal("admitted PDF annotation reader is absent from the native catalog")
	}
	if f.server.kernelManager != nil || agentToolSchemasContain(f.server.agentKernelToolSchemas(nil, map[string]struct{}{name: {}}), name) {
		t.Fatal("read-only admission became dependent on, or leaked from, the language kernel catalog")
	}
	if agentToolSchemasContain(f.server.agentRuntimeToolSchemasWithContextOptions(ctx, []string{"read_file"}, true, f.stream.FrameID), name) {
		t.Fatal("PDF annotation reader escaped the original allowed-tools intersection")
	}
	if !harnesscontract.ModelToolAllowed(name) || agentRuntimeToolNeedsApproval(name) ||
		!slices.Contains(readOnlyAgentAllowedTools([]string{name}), name) ||
		slices.Contains(readOnlyAgentAllowedTools([]string{"read_file"}), name) {
		t.Fatal("read-only tool is disconnected from its canonical exposure or permission owner")
	}
	active := partitionAgentRuntimeToolSchemasForModel(schemas)
	if !agentToolSchemasContain(active, name) {
		t.Fatal("native PDF reader is hidden from the actual model snapshot")
	}
	for _, schema := range active {
		if schema.Name == name && !agentRuntimeToolSchemaHasCapability(schema, "read-only") {
			t.Fatal("PDF metadata reader lacks trusted read-only capability")
		}
	}
}

func TestAgentPDFAnnotationListFactoryRequiresActualAdmittedReading(t *testing.T) {
	f, reading, _ := newPDFReadingFixture(t)
	gateway := admitPDFAnnotationGatewayForTest(t, f, nil)
	ctx := withTranscriptRunnerChatRun(t.Context(), gateway.taskRun)
	const name = "list_pdf_annotations"
	visible := func(ctx context.Context) bool {
		return agentToolSchemasContain(partitionAgentRuntimeToolSchemasForModel(
			f.server.agentRuntimeToolSchemasWithContextOptions(ctx, []string{name}, true, f.stream.FrameID)), name)
	}
	if visible(t.Context()) || visible(ctx) {
		t.Fatal("live UI bindings or missing run context advertised an unadmitted PDF capability")
	}
	response := p3JSONRequest(t, f.server, http.MethodPost, "/api/conversations/"+f.stream.FrameID+"/messages", webConversationSendInput{
		Content: "Read this explicitly selected synthetic PDF on the next input.", LoadingID: uuid.NewString(), ReadingContext: readingSelectionForTest(reading),
	}, f.stream.OwnerID)
	if response.Code != http.StatusAccepted || mapValue(p3DecodeObject(t, response)["input_receipt"])["state"] != "queued" {
		t.Fatalf("real Reading queue HTTP%d %s", response.Code, response.Body.String())
	}
	if visible(ctx) {
		t.Fatal("a newer queued Reading selection changed the active task's available tools")
	}
	finishSkillControlInput(t, f)
	if delivered, err := f.server.recoverCompatibilityQueuedMessages(); err != nil || delivered != 1 {
		t.Fatalf("deliver accepted Reading input=%d err=%v", delivered, err)
	}
	next := claimSkillControlInput(t, f)
	if !visible(withTranscriptRunnerChatRun(t.Context(), next)) {
		t.Fatal("the same Reading input stayed hidden after actual admission")
	}
}

func TestAgentPDFAnnotationListReadsOnlyAdmittedVersionWithoutLanguageKernel(t *testing.T) {
	f, reading, gateway := pdfAnnotationGatewayForTest(t)
	gateway.kernel = nil
	binding := reading.Bindings[0]
	annotation, err := f.store.CreateAnnotation(workspace.CreateAnnotationInput{
		ProjectID: f.stream.ProjectID, TargetKind: "artifact", TargetKey: "av:" + binding.VersionID,
		ContentChecksum: binding.Checksum,
		Body:            map[string]any{"type": "text_selection", "text": "Synthetic note", "selection_text": "Selected words", "page_number": 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	value, err := gateway.executeAgentToolResponseUncached(context.Background(), agentruntime.ToolCall{ID: "list-reading-annotations"}, "list_pdf_annotations", map[string]any{})
	if err != nil {
		t.Fatalf("admitted read-only annotation tool failed: %v", err)
	}
	result := pdfAnnotationResultForTest(t, value)
	items, ok := result["annotations"].([]any)
	if !ok || len(items) != 1 || result["documentId"] != binding.BindingID || result["total"] != float64(1) {
		t.Fatalf("annotation result=%#v", result)
	}
	item := items[0].(map[string]any)
	if item["id"] != annotation.ID || item["type"] != "text_selection" || item["note"] != "Synthetic note" || item["quote"] != "Selected words" || item["pageNumber"] != float64(2) {
		t.Fatalf("public annotation projection=%#v", item)
	}
	if f.server.kernelManager != nil {
		t.Fatal("synthetic test unexpectedly required a language execution environment")
	}
}

func pdfAnnotationGatewayForTest(t *testing.T) (*agentSaveArtifactsFixture, transcriptstore.PDFReadingContext, serverAgentRuntimeToolGateway) {
	t.Helper()
	f, reading, _ := newPDFReadingFixture(t)
	return f, reading, admitPDFAnnotationGatewayForTest(t, f, readingSelectionForTest(reading))
}

func admitPDFAnnotationGatewayForTest(t *testing.T, f *agentSaveArtifactsFixture, selection *transcriptstore.PDFReadingSelection) serverAgentRuntimeToolGateway {
	t.Helper()
	response := p3JSONRequest(t, f.server, http.MethodPost, "/api/conversations/"+f.stream.FrameID+"/messages", webConversationSendInput{
		Content: "Read the notes on this explicitly selected synthetic PDF.", LoadingID: uuid.NewString(), ReadingContext: selection,
	}, f.stream.OwnerID)
	if response.Code != http.StatusAccepted {
		t.Fatalf("reading admission HTTP %d: %s", response.Code, response.Body.String())
	}
	run := claimSkillControlInput(t, f)
	return serverAgentRuntimeToolGateway{server: f.server, kernel: f.identity, taskRun: run, sessionID: f.stream.SessionID}
}

func TestAgentPDFAnnotationListBoundsPreviewAndCursorToClaim(t *testing.T) {
	f, reading, gateway := pdfAnnotationGatewayForTest(t)
	binding := reading.Bindings[0]
	ids := []string{}
	for index := 0; index < 21; index++ {
		row, err := f.store.CreateAnnotation(workspace.CreateAnnotationInput{ProjectID: f.stream.ProjectID, TargetKind: "artifact", TargetKey: "av:" + binding.VersionID,
			ContentChecksum: binding.Checksum, Body: map[string]any{"type": "text_selection", "text": strings.Repeat("😀", 1001), "selection_text": strings.Repeat("界", 501)}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, row.ID)
	}
	value, err := gateway.executeAgentToolResponseUncached(t.Context(), agentruntime.ToolCall{ID: "first-annotations"}, "list_pdf_annotations", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	first := pdfAnnotationResultForTest(t, value)
	items := first["annotations"].([]any)
	if len(items) != 20 || first["total"] != float64(21) {
		t.Fatalf("bounded page=%#v", first)
	}
	item := items[0].(map[string]any)
	if !utf8.ValidString(item["note"].(string)) || len([]rune(item["note"].(string))) != 1000 || len([]rune(item["quote"].(string))) != 500 || item["noteTruncated"] != true || item["quoteTruncated"] != true {
		t.Fatalf("unsafe or unbounded preview=%#v", item)
	}
	cursor, ok := first["nextCursor"].(string)
	if !ok || cursor == "" {
		t.Fatal("first page omitted its continuation")
	}
	value, err = gateway.executeAgentToolResponseUncached(t.Context(), agentruntime.ToolCall{ID: "next-annotations"}, "list_pdf_annotations", map[string]any{"documentId": binding.BindingID, "cursor": cursor})
	if err != nil {
		t.Fatal(err)
	}
	second := pdfAnnotationResultForTest(t, value)
	if len(second["annotations"].([]any)) != 1 || second["annotations"].([]any)[0].(map[string]any)["id"] != ids[20] || second["nextCursor"] != nil {
		t.Fatalf("cursor duplicated/skipped annotation rows: %#v", second)
	}
	finishSkillControlInput(t, f)
	next := admitPDFAnnotationGatewayForTest(t, f, readingSelectionForTest(reading))
	_, err = next.executeAgentToolResponseUncached(t.Context(), agentruntime.ToolCall{ID: "foreign-claim-cursor"}, "list_pdf_annotations", map[string]any{"cursor": cursor})
	assertPDFAnnotationErrorForTest(t, err, "invalid_arguments")
}

func TestAgentPDFAnnotationListRejectsUnadmittedAndStaleAuthority(t *testing.T) {
	f, _, _ := newPDFReadingFixture(t)
	noSelection := admitPDFAnnotationGatewayForTest(t, f, nil)
	_, err := noSelection.executeAgentToolResponseUncached(t.Context(), agentruntime.ToolCall{ID: "no-selection"}, "list_pdf_annotations", map[string]any{})
	assertPDFAnnotationErrorForTest(t, err, "reading_context_unavailable")

	f, reading, gateway := pdfAnnotationGatewayForTest(t)
	for _, input := range []map[string]any{{"owner": f.stream.OwnerID}, {"projectId": f.stream.ProjectID}, {"path": "arbitrary.pdf"}, {"documentId": "outside-admitted-snapshot"}, {"documentId": nil}, {"cursor": "invalid-cursor"}} {
		if _, err := gateway.executeAgentToolResponseUncached(t.Context(), agentruntime.ToolCall{ID: "closed-scope"}, "list_pdf_annotations", input); err == nil {
			t.Fatalf("untrusted scope accepted: %#v", input)
		}
	}
	badHost := *gateway.kernel
	badHost.access.Frame.IncarnationID = "retired-incarnation"
	stale := gateway
	stale.kernel = &badHost
	if _, err := stale.executeAgentToolResponseUncached(t.Context(), agentruntime.ToolCall{ID: "stale-host"}, "list_pdf_annotations", map[string]any{}); err == nil {
		t.Fatal("retired host returned annotations")
	}
	badAuthority := *gateway.taskRun.Transcript
	badAuthority.Stream.OwnerID = "another-account"
	badRun := &sessionRunnerChatRun{SessionID: gateway.taskRun.SessionID, Attempt: gateway.taskRun.Attempt, Transcript: &badAuthority}
	stale = gateway
	stale.taskRun = badRun
	if _, err := stale.executeAgentToolResponseUncached(t.Context(), agentruntime.ToolCall{ID: "foreign-account"}, "list_pdf_annotations", map[string]any{}); err == nil {
		t.Fatal("foreign account returned annotations")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := gateway.executeAgentToolResponseUncached(ctx, agentruntime.ToolCall{ID: "cancelled"}, "list_pdf_annotations", map[string]any{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled reader=%v", err)
	}
	unlinkPDFReadingForTest(t, f, reading)
	// Removing a later mutable UI binding cannot rewrite the already admitted input.
	value, err := gateway.executeAgentToolResponseUncached(t.Context(), agentruntime.ToolCall{ID: "frozen-input"}, "list_pdf_annotations", map[string]any{})
	if err != nil || pdfAnnotationResultForTest(t, value)["documentId"] != reading.Bindings[0].BindingID {
		t.Fatalf("mutable Reading state replaced admitted source: %v", err)
	}
	finishSkillControlInput(t, f)
	if _, err := gateway.executeAgentToolResponseUncached(t.Context(), agentruntime.ToolCall{ID: "retired-claim"}, "list_pdf_annotations", map[string]any{}); err == nil {
		t.Fatal("retired claim still returned annotations")
	}
}

func TestAgentPDFAnnotationListRequiresDocumentForMultipleBindings(t *testing.T) {
	f, reading, _ := newPDFReadingFixture(t)
	artifact, version, err := f.store.SaveArtifactVersion(workspace.SaveArtifactVersionInput{ArtifactID: "second-annotation-pdf", ProjectID: f.stream.ProjectID, Name: "second.pdf", Kind: "pdf", Content: pdfReadingContextFixture()})
	if err != nil {
		t.Fatal(err)
	}
	response := p3JSONRequest(t, f.server, http.MethodPost, readingPathForTest(f)+"/link", pdfReadingMutationBody{Schema: "x-science.pdf-reading-link.v1", OperationID: uuid.NewString(), PDFReadingExpected: readingExpectedForTest(reading), Sources: []transcriptstore.PDFReadingSource{{ArtifactID: artifact.ID, VersionID: version.ID}}}, f.stream.OwnerID)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &reading) != nil {
		t.Fatalf("second binding HTTP%d %s", response.Code, response.Body.String())
	}
	gateway := admitPDFAnnotationGatewayForTest(t, f, readingSelectionForTest(reading))
	ctx := withTranscriptRunnerChatRun(t.Context(), gateway.taskRun)
	if !agentToolSchemasContain(f.server.agentRuntimeToolSchemasWithContextOptions(ctx, []string{"list_pdf_annotations"}, true, f.stream.FrameID), "list_pdf_annotations") {
		t.Fatal("multiple admitted artifact PDFs hid their supported reader")
	}
	_, err = gateway.executeAgentToolResponseUncached(t.Context(), agentruntime.ToolCall{ID: "ambiguous"}, "list_pdf_annotations", map[string]any{})
	assertPDFAnnotationErrorForTest(t, err, "invalid_arguments")
	value, err := gateway.executeAgentToolResponseUncached(t.Context(), agentruntime.ToolCall{ID: "explicit-binding"}, "list_pdf_annotations", map[string]any{"documentId": reading.Bindings[0].BindingID})
	if err != nil || pdfAnnotationResultForTest(t, value)["documentId"] != reading.Bindings[0].BindingID {
		t.Fatalf("explicit admitted binding was not selected: %v", err)
	}
}

func TestAgentPDFAnnotationListLibrarySourceIsTypedUnsupported(t *testing.T) {
	f, reading, _ := newPDFReadingFixture(t)
	bound := reading.Bindings[0]
	response := p3JSONRequest(t, f.server, http.MethodPost, "/api/literature/import-artifact-version", workspace.LiteratureImport{OperationID: uuid.NewString(), ArtifactID: bound.ArtifactID, VersionID: bound.VersionID, Title: "Synthetic Library PDF", ProjectID: f.stream.ProjectID}, f.stream.OwnerID)
	var receipt workspace.LiteratureReceipt
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &receipt) != nil {
		t.Fatalf("Library fixture HTTP%d %s", response.Code, response.Body.String())
	}
	reading = unlinkPDFReadingForTest(t, f, reading)
	response = p3JSONRequest(t, f.server, http.MethodPost, readingPathForTest(f)+"/link", pdfReadingMutationBody{Schema: "x-science.pdf-reading-link.v1", OperationID: uuid.NewString(), PDFReadingExpected: readingExpectedForTest(reading), Sources: []transcriptstore.PDFReadingSource{{SourceKind: "literature-attachment-version", ItemID: receipt.Item.ID, AttachmentID: receipt.AttachmentID, VersionID: receipt.VersionID}}}, f.stream.OwnerID)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &reading) != nil {
		t.Fatalf("Library Reading binding HTTP%d %s", response.Code, response.Body.String())
	}
	gateway := admitPDFAnnotationGatewayForTest(t, f, readingSelectionForTest(reading))
	ctx := withTranscriptRunnerChatRun(t.Context(), gateway.taskRun)
	if agentToolSchemasContain(f.server.agentRuntimeToolSchemasWithContextOptions(ctx, []string{"list_pdf_annotations"}, true, f.stream.FrameID), "list_pdf_annotations") {
		t.Fatal("an unsupported-only source was advertised as an available annotation reader")
	}
	_, err := gateway.executeAgentToolResponseUncached(t.Context(), agentruntime.ToolCall{ID: "unsupported-library-source"}, "list_pdf_annotations", map[string]any{})
	assertPDFAnnotationErrorForTest(t, err, "unsupported_source")
	finishSkillControlInput(t, f)
	response = p3JSONRequest(t, f.server, http.MethodPost, readingPathForTest(f)+"/link", pdfReadingMutationBody{Schema: "x-science.pdf-reading-link.v1", OperationID: uuid.NewString(), PDFReadingExpected: readingExpectedForTest(reading), Sources: []transcriptstore.PDFReadingSource{{ArtifactID: bound.ArtifactID, VersionID: bound.VersionID}}}, f.stream.OwnerID)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &reading) != nil {
		t.Fatalf("mixed Reading binding HTTP%d %s", response.Code, response.Body.String())
	}
	mixed := admitPDFAnnotationGatewayForTest(t, f, readingSelectionForTest(reading))
	ctx = withTranscriptRunnerChatRun(t.Context(), mixed.taskRun)
	if !agentToolSchemasContain(f.server.agentRuntimeToolSchemasWithContextOptions(ctx, []string{"list_pdf_annotations"}, true, f.stream.FrameID), "list_pdf_annotations") {
		t.Fatal("an unsupported first binding hid another admitted artifact PDF")
	}
	_, err = mixed.executeAgentToolResponseUncached(t.Context(), agentruntime.ToolCall{ID: "mixed-source-ambiguous"}, "list_pdf_annotations", map[string]any{})
	assertPDFAnnotationErrorForTest(t, err, "invalid_arguments")
}

func TestAgentPDFAnnotationListRejectsLateReadAfterClaimRetirementOrCancel(t *testing.T) {
	for _, retire := range []string{"claim", "cancel"} {
		t.Run(retire, func(t *testing.T) {
			f, _, gateway := pdfAnnotationGatewayForTest(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			reads := 0
			value, err := gateway.readPDFAnnotationList(ctx, map[string]any{}, func(ctx context.Context, source workspace.ArtifactAnnotationSource, cursor *workspace.AnnotationPageCursor) (workspace.AnnotationReadPage, error) {
				page, err := f.store.ListArtifactAnnotationPageOwned(ctx, source, cursor)
				if err != nil {
					return page, err
				}
				reads++
				if retire == "claim" {
					finishSkillControlInput(t, f)
				} else {
					cancel()
				}
				return page, nil
			})
			if reads != 1 || err == nil || value != nil {
				t.Fatalf("late result escaped its original authority: reads=%d value=%#v error=%v", reads, value, err)
			}
			if retire == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("late cancellation was reclassified: %v", err)
			}
		})
	}
}

func assertPDFAnnotationErrorForTest(t *testing.T, err error, code string) {
	t.Helper()
	var typed *kernelruntime.HostCallError
	if !errors.As(err, &typed) || typed.Code != code {
		t.Fatalf("want typed %s, got %v", code, err)
	}
}

func pdfAnnotationResultForTest(t *testing.T, value any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
