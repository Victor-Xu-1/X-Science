package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"unicode/utf16"

	"x-science/internal/kernelcontract"
	transcriptstore "x-science/internal/persistence/transcript"
)

const (
	completionAdvisoryStructurePreview   = "structure_preview_unverified"
	completionAdvisoryResultQuality      = "result_quality_unverified"
	completionAdvisoryTaskExpectations   = "task_expectations_unverified"
	completionAdvisoryChemicalValidation = "chemical_validation_unavailable"
	completionAdvisoryChemicalHistory    = "chemical_validation_history_unverified"
)

// Completion advisories are host findings, not another completion obligation.
// The atomic finalizer retains compatibility text and an optional typed
// projection; no separate event, model repair, or mutable UI status is needed.
type sessionRunnerCompletionAdvisory struct {
	Code     string
	Findings []string
}

type completionAdvisorySource struct {
	Stage           string `json:"stage"`
	Attempt         int64  `json:"attempt"`
	InputRevision   int64  `json:"input_revision"`
	CandidateSHA256 string `json:"candidate_sha256"`
	FindingIndexes  []int  `json:"finding_indexes"`
}

type completionAdvisoryItem struct {
	Code     string                     `json:"code"`
	Findings []string                   `json:"findings"`
	Sources  []completionAdvisorySource `json:"sources"`
}

type completionAdvisorySpan struct {
	Start int    `json:"start"`
	End   int    `json:"end"`
	Text  string `json:"text"`
}

type completionAdvisoryEnvelope struct {
	Schema        string                   `json:"schema"`
	Attempt       int64                    `json:"attempt"`
	InputRevision int64                    `json:"input_revision"`
	TextSHA256    string                   `json:"text_sha256"`
	TextLength    int                      `json:"text_length"`
	Items         []completionAdvisoryItem `json:"items"`
	Spans         []completionAdvisorySpan `json:"spans"`
}

// Private append receipts distinguish host additions from identical-looking
// model prose. They never come from arguments or persisted free-form text.
type completionAdvisoryAppend struct {
	start, end         int
	text, beforeSHA256 string
}

type sessionRunnerCompletionAdvisoryPresentation struct {
	items   []completionAdvisoryItem
	appends []completionAdvisoryAppend
}

func completionAdvisorySHA(text string) string {
	digest := sha256.Sum256([]byte(text))
	return hex.EncodeToString(digest[:])
}

// Keep the compatibility text API while recording only actual host operations.
// The same finalization transaction will persist their typed provenance.
func appendSessionRunnerCompletionAdvisoriesForRun(content, language string, run *sessionRunnerChatRun, stage string, advisories ...*sessionRunnerCompletionAdvisory) string {
	if run == nil || run.Transcript == nil || strings.TrimSpace(content) == "" {
		return appendSessionRunnerCompletionAdvisories(content, language, advisories...)
	}
	run.completionAdvisoryMu.Lock()
	defer run.completionAdvisoryMu.Unlock()
	if run.completionAdvisories == nil {
		run.completionAdvisories = &sessionRunnerCompletionAdvisoryPresentation{}
	}
	for _, advisory := range advisories {
		if advisory == nil {
			continue
		}
		english, _ := sessionRunnerCompletionAdvisoryText(advisory.Code)
		if english == "" {
			continue
		}
		origin := completionAdvisorySource{Stage: stage, Attempt: run.Transcript.Claim.Attempt,
			InputRevision: run.Transcript.Claim.ClaimedInputRevision, CandidateSHA256: completionAdvisorySHA(content), FindingIndexes: []int{}}
		position := -1
		for index := range run.completionAdvisories.items {
			if run.completionAdvisories.items[index].Code == advisory.Code {
				position = index
				break
			}
		}
		if position < 0 {
			run.completionAdvisories.items = append(run.completionAdvisories.items, completionAdvisoryItem{Code: advisory.Code, Findings: []string{}, Sources: []completionAdvisorySource{}})
			position = len(run.completionAdvisories.items) - 1
		}
		item := &run.completionAdvisories.items[position]
		for _, finding := range advisory.Findings {
			found := -1
			for index, existing := range item.Findings {
				if existing == finding {
					found = index
					break
				}
			}
			if found < 0 {
				found = len(item.Findings)
				item.Findings = append(item.Findings, finding)
			}
			present := false
			for _, index := range origin.FindingIndexes {
				if index == found {
					present = true
					break
				}
			}
			if !present {
				origin.FindingIndexes = append(origin.FindingIndexes, found)
			}
		}
		found := false
		for index, existing := range item.Sources {
			if existing.Stage == origin.Stage && existing.Attempt == origin.Attempt && existing.InputRevision == origin.InputRevision && existing.CandidateSHA256 == origin.CandidateSHA256 {
				for _, finding := range origin.FindingIndexes {
					present := false
					for _, prior := range item.Sources[index].FindingIndexes {
						if prior == finding {
							present = true
							break
						}
					}
					if !present {
						item.Sources[index].FindingIndexes = append(item.Sources[index].FindingIndexes, finding)
					}
				}
				found = true
				break
			}
		}
		if !found {
			item.Sources = append(item.Sources, origin)
		}
		next := appendSessionRunnerCompletionAdvisories(content, language, advisory)
		if len(next) > len(content) && strings.HasPrefix(next, content) {
			run.completionAdvisories.appends = append(run.completionAdvisories.appends,
				completionAdvisoryAppend{start: len(content), end: len(next), text: next[len(content):], beforeSHA256: origin.CandidateSHA256})
		}
		content = next
	}
	return content
}

func (run *sessionRunnerChatRun) completionAdvisorySnapshot() *sessionRunnerCompletionAdvisoryPresentation {
	if run == nil {
		return nil
	}
	run.completionAdvisoryMu.Lock()
	defer run.completionAdvisoryMu.Unlock()
	if run.completionAdvisories == nil || len(run.completionAdvisories.items) == 0 {
		return nil
	}
	copy := &sessionRunnerCompletionAdvisoryPresentation{appends: append([]completionAdvisoryAppend(nil), run.completionAdvisories.appends...)}
	for _, item := range run.completionAdvisories.items {
		cloned := completionAdvisoryItem{Code: item.Code, Findings: append([]string{}, item.Findings...), Sources: append([]completionAdvisorySource{}, item.Sources...)}
		for index := range cloned.Sources {
			cloned.Sources[index].FindingIndexes = append([]int{}, item.Sources[index].FindingIndexes...)
		}
		copy.items = append(copy.items, cloned)
	}
	return copy
}

func (presentation *sessionRunnerCompletionAdvisoryPresentation) envelope(content string, claim transcriptstore.RunnerClaim) *completionAdvisoryEnvelope {
	if presentation == nil || len(presentation.items) == 0 || claim.Attempt <= 0 || claim.ClaimedInputRevision <= 0 {
		return nil
	}
	wire := &completionAdvisoryEnvelope{Schema: "x-science.completion-advisories.v1", Attempt: claim.Attempt,
		InputRevision: claim.ClaimedInputRevision, TextSHA256: completionAdvisorySHA(content), TextLength: len(utf16.Encode([]rune(content))),
		Items: presentation.items, Spans: []completionAdvisorySpan{}}
	after := 0
	for _, receipt := range presentation.appends {
		// A later rewrite cannot inherit an old ownership claim. Preserve all
		// its text instead of guessing at a notice by wording or prefix.
		if receipt.start < after || receipt.end > len(content) || receipt.start < 0 || receipt.end <= receipt.start ||
			content[receipt.start:receipt.end] != receipt.text || completionAdvisorySHA(content[:receipt.start]) != receipt.beforeSHA256 {
			continue
		}
		wire.Spans = append(wire.Spans, completionAdvisorySpan{Start: len(utf16.Encode([]rune(content[:receipt.start]))),
			End: len(utf16.Encode([]rune(content[:receipt.end]))), Text: receipt.text})
		after = receipt.end
	}
	return wire
}

// The optional projection never manufactures an answer or changes completion.
// Invalid/legacy metadata leaves its compatibility text untouched.
func sessionRunnerCompletionAdvisoriesFromPayload(payload map[string]any, attempt int64) *completionAdvisoryEnvelope {
	value, present := payload["completion_advisories"]
	if !present {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var wire completionAdvisoryEnvelope
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&wire) != nil || decoder.Decode(new(any)) != io.EOF || wire.Schema != "x-science.completion-advisories.v1" ||
		wire.Attempt != attempt || wire.Attempt <= 0 || wire.InputRevision <= 0 || len(wire.Items) == 0 || len(wire.Items) > 5 || len(wire.Spans) > 16 {
		return nil
	}
	text := stringValue(payload["text"])
	units := utf16.Encode([]rune(text))
	if wire.TextSHA256 != completionAdvisorySHA(text) || wire.TextLength != len(units) {
		return nil
	}
	if !sessionRunnerCompletionAdvisoryItemsValid(wire.Items, wire.Attempt, wire.InputRevision) {
		return nil
	}
	if !sessionRunnerCompletionAdvisorySpansValid(units, wire.Spans) {
		return nil
	}
	return &wire
}

// References reuse this item codec without gaining host provenance. Zero
// bounds validate only shape; quoted sources remain untrusted claims.
func sessionRunnerCompletionAdvisoryItemsValid(items []completionAdvisoryItem, attempt, inputRevision int64) bool {
	codes := map[string]bool{}
	for _, item := range items {
		english, _ := sessionRunnerCompletionAdvisoryText(item.Code)
		if english == "" || codes[item.Code] || item.Findings == nil || len(item.Findings) > 4096 || len(item.Sources) == 0 || len(item.Sources) > 64 {
			return false
		}
		codes[item.Code] = true
		covered := map[int]bool{}
		for _, finding := range item.Findings {
			if len(finding) > 65536 {
				return false
			}
		}
		for _, source := range item.Sources {
			if source.Stage != "artifact_completion" && source.Stage != "task_expectations" && source.Stage != "completion_recovery" {
				return false
			}
			digest, err := hex.DecodeString(source.CandidateSHA256)
			if err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != source.CandidateSHA256 ||
				source.Attempt <= 0 || (attempt > 0 && source.Attempt > attempt) || source.InputRevision <= 0 || (inputRevision > 0 && source.InputRevision > inputRevision) {
				return false
			}
			if source.FindingIndexes == nil || len(source.FindingIndexes) > len(item.Findings) {
				return false
			}
			seen := map[int]bool{}
			for _, index := range source.FindingIndexes {
				if index < 0 || index >= len(item.Findings) || seen[index] {
					return false
				}
				seen[index] = true
				covered[index] = true
			}
		}
		if len(covered) != len(item.Findings) {
			return false
		}
	}
	return true
}

// Decode one complete JSON value so public projection skips nested objects and
// strings as a unit. Only the closed machine-reference shape may be omitted;
// its contents never become a host finding or completion authority.
func sessionRunnerCompletionObservationReference(content string) (int, bool) {
	decoder := json.NewDecoder(strings.NewReader(content))
	var raw json.RawMessage
	if decoder.Decode(&raw) != nil {
		return 0, false
	}
	consumed := int(decoder.InputOffset())
	var reference struct {
		Items []completionAdvisoryItem `json:"host_completion_observations"`
	}
	typed := json.NewDecoder(bytes.NewReader(raw))
	typed.DisallowUnknownFields()
	if typed.Decode(&reference) != nil || len(reference.Items) == 0 || len(reference.Items) > 5 ||
		kernelcontract.ValidateJSON(raw) != nil || !sessionRunnerCompletionAdvisoryItemsValid(reference.Items, 0, 0) {
		return consumed, false
	}
	// encoding/json permits case-insensitive struct aliases. A reference must
	// have exactly the existing codec's keys, not a future or ambiguous shape.
	canonical, err := json.Marshal(reference)
	var actual, expected any
	if err != nil || json.Unmarshal(raw, &actual) != nil || json.Unmarshal(canonical, &expected) != nil || !reflect.DeepEqual(actual, expected) {
		return consumed, false
	}
	return consumed, true
}

func sessionRunnerCompletionAdvisorySpansValid(units []uint16, spans []completionAdvisorySpan) bool {
	boundary := func(index int) bool {
		return index == 0 || index == len(units) ||
			units[index-1] < 0xd800 || units[index-1] > 0xdbff || units[index] < 0xdc00 || units[index] > 0xdfff
	}
	after := 0
	for _, span := range spans {
		if span.Start < after || span.End <= span.Start || span.End > len(units) ||
			!boundary(span.Start) || !boundary(span.End) || string(utf16.Decode(units[span.Start:span.End])) != span.Text {
			return false
		}
		after = span.End
	}
	return true
}

func sessionRunnerCompletionAdvisoryModelBody(text string, wire *completionAdvisoryEnvelope) string {
	if wire == nil {
		return text
	}
	units := utf16.Encode([]rune(text))
	if !sessionRunnerCompletionAdvisorySpansValid(units, wire.Spans) {
		return text
	}
	for index := len(wire.Spans) - 1; index >= 0; index-- {
		span := wire.Spans[index]
		units = append(units[:span.Start], units[span.End:]...)
	}
	return string(utf16.Decode(units))
}

// All model-facing Transcript readers use this same read-only projection.
// Legacy/imported answers retain their original text. Findings from a real
// canonical assistant event remain quoted facts, never system instructions or
// execution authority; the immutable public answer is not rewritten.
func projectSessionRunnerCompletionAdvisoryReplay(message map[string]any, event transcriptstore.Event) {
	if event.Type != "assistant_message" || event.Source != transcriptstore.EventSourcePayload || event.RunnerAttempt == nil {
		return
	}
	advisory := sessionRunnerCompletionAdvisoriesFromPayload(message, *event.RunnerAttempt)
	if advisory == nil {
		return
	}
	facts, _ := json.Marshal(map[string]any{"host_completion_observations": advisory.Items})
	message["text"] = sessionRunnerCompletionAdvisoryModelBody(stringValue(message["text"]), advisory) + "\n\n" + string(facts)
}

func appendSessionRunnerCompletionAdvisories(content, language string, advisories ...*sessionRunnerCompletionAdvisory) string {
	// A notice must not turn a missing model answer into a completed response.
	if strings.TrimSpace(content) == "" {
		return content
	}
	for _, advisory := range advisories {
		if advisory == nil {
			continue
		}
		english, chinese := sessionRunnerCompletionAdvisoryText(advisory.Code)
		if english == "" || strings.Contains(content, english) || (chinese != "" && strings.Contains(content, chinese)) {
			continue
		}
		notice := english
		if sessionRunnerRequiresChinese(language) {
			notice = chinese
		}
		content += "\n\n> " + notice
	}
	return content
}

func sessionRunnerCompletionAdvisoryText(code string) (english, chinese string) {
	switch code {
	case completionAdvisoryStructurePreview:
		return "Structure preview is unverified. Existing results are delivered without a validated combined structure preview. This does not certify scientific correctness or fulfil any missing preview deliverable.",
			"结构预览尚未验证。现有结果已交付，但不包含经过验证的组合结构预览；这不代表科学结论已验证，也不代表缺少的预览交付项已经完成。"
	case completionAdvisoryResultQuality:
		return "Result quality is unverified. Some consistency or evidence checks remain unresolved. Existing outputs are preserved; task completion does not certify their accuracy.",
			"结果质量尚未验证。部分一致性或证据检查仍存在问题。现有输出已保留；任务完成不代表这些结果的准确性已经得到验证。"
	case completionAdvisoryTaskExpectations:
		return "Execution details are unverified. Some actions or reporting details mentioned in the task text are not confirmed by the recorded results; this does not mean they were required or completed.",
			"执行细节尚未核实。任务文字中提到的部分操作或报告细节未获已有记录确认；这并不表示这些操作必须执行或已经完成。"
	case completionAdvisoryChemicalValidation:
		return "Chemical validation was not performed because an optional validator is unavailable. Existing files are preserved without certifying molecular validity.",
			"化学结构校验未执行：可选校验器当前不可用。现有文件已保留，但其分子结构有效性尚未验证。"
	case completionAdvisoryChemicalHistory:
		return "Earlier chemical-validation failures could not be matched to the current immutable selection and have not been revalidated.",
			"先前的化学结构校验失败无法与当前不可变产物集合准确对应，且尚未重新验证；不能据此认为旧问题已解决。"
	default:
		return "", ""
	}
}

func sessionRunnerVisualCorrectionIsAdvisory(correction recoveredRunnerCorrection) bool {
	condition := correction.Condition
	if correction.ReasonCode != sessionRunnerVisualArtifactValidationReasonCode || condition == nil ||
		condition.ReasonCode != correction.ReasonCode || condition.Validate() != nil || condition.Visual == nil {
		return false
	}
	// Typed historical image producers always supplied exact Targets, including
	// invalid_digest, missing_validation and invalid_image_integrity. Only the
	// auxiliary scene producer used UnboundNames alone. Untyped historical text,
	// empty conditions and unknown image failures cannot establish this policy.
	return len(condition.Visual.Artifacts) == 0 && strings.TrimSpace(strings.Join(condition.Visual.UnboundNames, "")) != ""
}
