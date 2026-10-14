package providers

import (
	"errors"
	"io"
	"strings"
	"testing"
)

type generationProgressReader struct {
	io.Reader
	observations int
	bytes        int64
}

func (reader *generationProgressReader) recordProgress(growth int64) {
	reader.observations++
	reader.bytes += growth
}

func TestProviderRejectedStreamFieldsDoNotRecordDecoderGrowth(t *testing.T) {
	for _, fixture := range []struct {
		name, data string
		read       func(*generationProgressReader) error
	}{
		{"chat_tool_missing_index", `{"choices":[{"delta":{"tool_calls":[{"id":"call","type":"function","function":{"name":"lookup","arguments":"{}"}}]}}]}`,
			func(reader *generationProgressReader) error {
				_, _, _, _, err := readOpenAIChatStream(reader, 4096, nil)
				return err
			}},
		{"chat_unsupported_terminal", `{"choices":[{"delta":{},"finish_reason":"invalid"}]}`,
			func(reader *generationProgressReader) error {
				_, _, _, _, err := readOpenAIChatStream(reader, 4096, nil)
				return err
			}},
		{"responses_unknown_tool_item", `{"type":"response.function_call_arguments.delta","item_id":"unknown","delta":"{}"}`,
			func(reader *generationProgressReader) error {
				_, _, _, _, err := readOpenAIResponsesStream(reader, 4096, nil, nil)
				return err
			}},
		{"native_dispatch_rejected", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"private fixture"}}`,
			func(reader *generationProgressReader) error {
				return readNativeSSE(reader, 4096, func(string, []byte) error { return errors.New("rejected fixture") })
			}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			reader := &generationProgressReader{Reader: strings.NewReader("data: " + fixture.data + "\n\n")}
			if err := fixture.read(reader); err == nil || reader.observations != 0 || reader.bytes != 0 {
				t.Fatalf("rejected field became decoder activity: err=%v observations=%d bytes=%d", err, reader.observations, reader.bytes)
			}
		})
	}
}
