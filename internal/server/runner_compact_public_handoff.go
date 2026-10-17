package server

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	eventjournal "x-science/internal/persistence/journal"
)

const compactPublicHandoffField = "publicHandoff"
const maxCompactPublicHandoffBytes = 128 << 10

// This is a bounded projection of public messages, not execution evidence or
// task memory. Decimal strings preserve source IDs through generic JSON maps.
type compactPublicHandoff struct {
	Version         int                    `json:"version"`
	TaskRootEventID int64                  `json:"taskRootEventId,string"`
	ThroughEventID  int64                  `json:"throughEventId,string"`
	Entries         []compactPublicMessage `json:"entries"`
}

type compactPublicMessage struct {
	SourceEventID int64  `json:"sourceEventId,string"`
	Role          string `json:"role"`
	Text          string `json:"text"`
	Truncated     bool   `json:"truncated"`
}

func compactPublicTaskRoot(entries []eventjournal.Entry) eventjournal.Entry {
	var root eventjournal.Entry
	for _, entry := range entries {
		if runnerEntryStartsNewLogicalTask(entry) {
			root = entry
		}
	}
	return root
}

func buildCompactPublicHandoff(entries []eventjournal.Entry) compactPublicHandoff {
	return compactPublicHandoff{
		Version: 1, TaskRootEventID: compactPublicTaskRoot(entries).EventID,
		ThroughEventID: maxEntryIDFromEntries(entries),
		Entries:        compactPublicConversation(entries, maxCompactSummaryConversationEntries, maxCompactSummaryLineRunes),
	}
}

func addCompactPublicHandoff(modelContext map[string]any, entries []eventjournal.Entry) {
	root := compactPublicTaskRoot(entries)
	if role, public := compactPublicEntryRole(root); !public || role != "user" || root.EventID <= 0 {
		// Manual/legacy sessions can have current public text without a task
		// anchor. Keep their current summary, but do not claim scoped continuity.
		return
	}
	handoff := buildCompactPublicHandoff(entries)
	if _, valid := decodeCompactPublicHandoff(handoff); valid {
		modelContext[compactPublicHandoffField] = handoff
	}
}

// Current canonical messages consume the shared budget first. Imported entries
// keep their original IDs and never gain recency by passing through a compact.
// The manual prompt uses its existing, richer current-message limits here.
func compactPublicConversation(entries []eventjournal.Entry, limit, lineRunes int) []compactPublicMessage {
	if limit <= 0 || lineRunes <= 0 {
		return nil
	}
	root := compactPublicTaskRoot(entries)
	remaining := limit * lineRunes
	selected := make([]compactPublicMessage, 0, limit)
	seen := make(map[int64]bool)
	add := func(message compactPublicMessage) {
		if len(selected) >= limit || remaining <= 0 {
			return
		}
		text, truncated := compactPublicTextPrefix(message.Text, min(lineRunes, remaining))
		if text == "" {
			return
		}
		message.Text, message.Truncated = text, message.Truncated || truncated
		remaining -= utf8.RuneCountInString(text)
		selected = append(selected, message)
	}
	for index := len(entries) - 1; index >= 0; index-- {
		entry := entries[index]
		if entry.EventID <= 0 || seen[entry.EventID] {
			continue
		}
		seen[entry.EventID] = true
		if root.EventID > 0 && entry.EventID < root.EventID ||
			root.SessionID != "" && entry.SessionID != "" && root.SessionID != entry.SessionID {
			continue
		}
		role, public := compactPublicEntryRole(entry)
		if !public {
			continue
		}
		text, truncated := compactPublicMessageText(entry.Message, lineRunes)
		add(compactPublicMessage{SourceEventID: entry.EventID, Role: role, Text: text, Truncated: truncated})
	}
	for index := len(entries) - 1; index >= 0; index-- {
		entry := entries[index]
		if !isCompactJournalEntry(entry) {
			continue
		}
		if prior, valid := compactPublicHandoffForTask(entry, root); valid {
			for index := len(prior.Entries) - 1; index >= 0; index-- {
				message := prior.Entries[index]
				if !seen[message.SourceEventID] {
					add(message)
				}
			}
		}
		break // Never fall back past a newer legacy, unknown or invalid compact.
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].SourceEventID < selected[j].SourceEventID })
	return selected
}

func compactPublicEntryRole(entry eventjournal.Entry) (string, bool) {
	role := stringValue(entry.Message["role"])
	if (role != "user" && role != "assistant") || boolValue(entry.Message["partial"], false) {
		return "", false
	}
	if visibility := stringValue(entry.Message["visibility"]); visibility != "" && visibility != "public" {
		return "", false
	}
	switch entry.SourceEventType {
	case "user_message", "history_user_message", "user_input_response":
		return role, role == "user"
	case "assistant_message", "history_assistant_message":
		return role, role == "assistant"
	case "": // Journal compatibility: these roles and message types are public.
		kind := stringValue(entry.Message["type"])
		return role, kind == "" || kind == "message"
	default:
		return "", false
	}
}

func compactPublicMessageText(message eventjournal.Message, limit int) (string, bool) {
	text, truncated := compactPublicBodyText(message, limit)
	if text == "" {
		// Historical journal messages may wrap the same public body once.
		if nested, ok := message["message"].(map[string]any); ok {
			return compactPublicBodyText(eventjournal.Message(nested), limit)
		}
	}
	return text, truncated
}

func compactPublicBodyText(message eventjournal.Message, limit int) (string, bool) {
	for _, key := range []string{"text", "content"} {
		if text, ok := message[key].(string); ok && strings.TrimSpace(text) != "" {
			return compactPublicTextPrefix(text, limit)
		}
	}
	var text strings.Builder
	remaining, truncated := limit, false
	var blocks []any
	for _, key := range []string{"content", "parts"} {
		switch value := message[key].(type) {
		case []any:
			blocks = value
		case map[string]any:
			blocks = []any{value}
		default:
			continue
		}
		for _, raw := range blocks {
			block, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			switch stringValue(block["type"]) {
			case "text", "plain_text", "input_text", "output_text", "markdown", "mrkdwn":
			default:
				continue // In particular, never read reasoning/thinking or tool blocks.
			}
			part := strings.TrimSpace(firstNonEmpty(stringValue(block["text"]), stringValue(block["content"])))
			if part == "" {
				continue
			}
			if text.Len() > 0 && remaining > 0 {
				text.WriteByte('\n')
				remaining--
			}
			prefix, cut := compactPublicTextPrefix(part, remaining)
			text.WriteString(prefix)
			remaining -= utf8.RuneCountInString(prefix)
			truncated = truncated || cut
		}
		if text.Len() > 0 {
			break
		}
	}
	return strings.TrimSpace(text.String()), truncated
}

func compactPublicExcerptRole(message compactPublicMessage) string {
	if message.Truncated {
		return message.Role + " (truncated)"
	}
	return message.Role
}

func compactPublicTextPrefix(text string, limit int) (string, bool) {
	text = strings.TrimSpace(text)
	if !utf8.ValidString(text) {
		return "", false
	}
	count := 0
	for offset := range text {
		if count >= limit {
			return strings.TrimSpace(text[:offset]), true
		}
		count++
	}
	return text, false
}

func compactPublicHandoffValue(message eventjournal.Message) (any, bool) {
	if modelContext, ok := message["modelContext"].(map[string]any); ok {
		if raw, present := modelContext[compactPublicHandoffField]; present {
			return raw, true
		}
	}
	if checkpoint, ok := message["checkpoint"].(map[string]any); ok {
		if modelContext, ok := checkpoint["modelContext"].(map[string]any); ok {
			raw, present := modelContext[compactPublicHandoffField]
			return raw, present
		}
	}
	return nil, false
}

func compactPublicHandoffForTask(entry, root eventjournal.Entry) (compactPublicHandoff, bool) {
	raw, present := compactPublicHandoffValue(entry.Message)
	if !present || root.EventID <= 0 || root.SessionID != "" && entry.SessionID != "" && root.SessionID != entry.SessionID {
		return compactPublicHandoff{}, false
	}
	if role, public := compactPublicEntryRole(root); !public || role != "user" {
		return compactPublicHandoff{}, false
	}
	// Transcript event metadata is host-derived. Legacy journal compacts have
	// no source type, but cannot masquerade as a public user/assistant message.
	if entry.SourceEventType != "" && entry.SourceEventType != "runner_checkpoint" ||
		stringValue(entry.Message["role"]) != "" {
		return compactPublicHandoff{}, false
	}
	handoff, valid := decodeCompactPublicHandoff(raw)
	return handoff, valid && handoff.TaskRootEventID == root.EventID && handoff.ThroughEventID < entry.EventID
}

func decodeCompactPublicHandoff(raw any) (compactPublicHandoff, bool) {
	handoff, typed := raw.(compactPublicHandoff)
	if !typed {
		object, ok := raw.(map[string]any)
		if !ok || len(object) != 4 {
			return handoff, false
		}
		version, ok := exactPositiveInt(object["version"])
		if !ok || version != 1 {
			return handoff, false
		}
		handoff.Version = version
		handoff.TaskRootEventID = compactPublicEventID(object["taskRootEventId"])
		handoff.ThroughEventID = compactPublicEventID(object["throughEventId"])
		items, ok := object["entries"].([]any)
		if !ok || len(items) > maxCompactSummaryConversationEntries {
			return handoff, false
		}
		handoff.Entries = make([]compactPublicMessage, 0, len(items))
		for _, rawItem := range items {
			item, ok := rawItem.(map[string]any)
			if !ok || len(item) != 4 {
				return handoff, false
			}
			role, roleOK := item["role"].(string)
			text, textOK := item["text"].(string)
			truncated, truncatedOK := item["truncated"].(bool)
			if !roleOK || !textOK || !truncatedOK {
				return handoff, false
			}
			handoff.Entries = append(handoff.Entries, compactPublicMessage{
				SourceEventID: compactPublicEventID(item["sourceEventId"]), Role: role, Text: text, Truncated: truncated,
			})
		}
	}
	if handoff.Version != 1 || handoff.TaskRootEventID <= 0 || handoff.ThroughEventID < handoff.TaskRootEventID || handoff.Entries == nil ||
		len(handoff.Entries) > maxCompactSummaryConversationEntries {
		return handoff, false
	}
	var previousID int64
	totalRunes := 0
	for _, item := range handoff.Entries {
		if item.SourceEventID < handoff.TaskRootEventID || item.SourceEventID > handoff.ThroughEventID || item.SourceEventID <= previousID ||
			(item.Role != "user" && item.Role != "assistant") || strings.TrimSpace(item.Text) == "" ||
			len(item.Text) > maxCompactSummaryLineRunes*utf8.UTFMax || !utf8.ValidString(item.Text) {
			return handoff, false
		}
		runes := utf8.RuneCountInString(item.Text)
		if runes > maxCompactSummaryLineRunes {
			return handoff, false
		}
		totalRunes += runes
		previousID = item.SourceEventID
	}
	if totalRunes > maxCompactSummaryConversationEntries*maxCompactSummaryLineRunes {
		return handoff, false
	}
	// Only the bounded typed shape reaches JSON serialization, never arbitrary
	// legacy fields, oversized input text, or recursively nested old summaries.
	encoded, err := json.Marshal(handoff)
	return handoff, err == nil && len(encoded) <= maxCompactPublicHandoffBytes
}

func compactPublicEventID(raw any) int64 {
	text, ok := raw.(string)
	if !ok || len(text) == 0 || len(text) > 19 {
		return 0
	}
	id, err := strconv.ParseInt(text, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != text {
		return 0
	}
	return id
}
