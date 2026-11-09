package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
)

// Keep the original expression independent of the production serializer. It is
// both the byte/error oracle and the unchanged measurement control.
func legacyTranscriptWebRecordJSON(message map[string]any) ([]byte, error) {
	copied := cloneTranscriptWebProjectionMap(message)
	delete(copied, "artifact_refs")
	raw, err := json.Marshal(copied)
	if err != nil || !json.Valid(raw) {
		return nil, transcriptstore.ErrEventConflict
	}
	return raw, nil
}

func transcriptWebSerializationFixture(textBytes, referenceCount int) (map[string]any, transcriptWebProjectionSourceMetadata) {
	references := make([]map[string]any, 0, referenceCount)
	for index := 0; index < referenceCount; index++ {
		references = append(references, map[string]any{
			"artifact_id": fmt.Sprintf("artifact-%03d", index), "version_id": fmt.Sprintf("version-%03d", index),
			"relation": "attached", "filename": fmt.Sprintf("attachment-%03d.txt", index),
			"content_type": "text/plain", "size_bytes": int64(1024), "checksum": strings.Repeat("a", 64),
		})
	}
	message := map[string]any{
		"id": "serialization-message", "msg_id": "serialization-client", "created_at": int64(1700000000000),
		"type": "text", "status": "finish",
		"content": map[string]any{
			"text":     strings.Repeat("x", textBytes),
			"metadata": map[string]any{"values": []any{"synthetic", 1.25, true}},
		},
		"artifact_refs": references,
	}
	metadata := transcriptWebProjectionSourceMetadata{
		coordinates: []canonicalReadCursorCoordinate{{id: "serialization-message", messageID: "serialization-client"}},
		ranges:      []transcriptWebPublicationRange{{First: 1, Last: 1, FirstEventID: 1, LastEventID: 1}},
	}
	return message, metadata
}

var transcriptWebSerializationByteSink []byte
var transcriptWebSerializationRecordSink []transcriptstore.TranscriptWebMessageRecord
var transcriptWebSerializationValiditySink bool

func BenchmarkTranscriptWebRecordSerialization(b *testing.B) {
	for _, fixture := range []struct {
		name       string
		textBytes  int
		references int
	}{
		{"text_1KiB_no_refs", 1024, 0},
		{"text_1KiB_32_refs", 1024, 32},
		{"text_32KiB_no_refs", 32 * 1024, 0},
	} {
		b.Run(fixture.name, func(b *testing.B) {
			message, metadata := transcriptWebSerializationFixture(fixture.textBytes, fixture.references)
			messages := []map[string]any{message}
			encoded, err := legacyTranscriptWebRecordJSON(message)
			if err != nil {
				b.Fatal(err)
			}
			b.Run("builder", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					records, _, err := buildTranscriptWebMessageRecords(messages, metadata)
					if err != nil {
						b.Fatal(err)
					}
					transcriptWebSerializationRecordSink = records
				}
			})
			b.Run("legacy_serialize", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					raw, err := legacyTranscriptWebRecordJSON(message)
					if err != nil {
						b.Fatal(err)
					}
					transcriptWebSerializationByteSink = raw
				}
			})
			b.Run("encoded_validity", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					transcriptWebSerializationValiditySink = json.Valid(encoded)
				}
			})
		})
	}
}
