package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// PDF text uses the same serialized-budgeted line window as ordinary text.
// The extractor streams its complete output; it does not discard a prefix's
// tail before the reader can supply a continuation cursor.
func readAgentWorkspacePDFTextWindow(ctx context.Context, path, filename, contentType string, size int64, pages []int, input, metadata map[string]any) (map[string]any, error) {
	converter, err := exec.LookPath("pdftotext")
	if err != nil {
		return nil, errors.New("governed PDF text extractor is not installed")
	}
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	reader, finished, coverage := streamAgentWorkspacePDFText(streamCtx, converter, path, pages)
	defer func() {
		cancel()
		_ = reader.Close()
		<-finished
	}()
	counted := &agentWorkspacePDFTextCounter{Reader: reader}
	offset, limit := 1, agentWorkspaceReadDefaultRows
	if raw, found := input["offset"]; found {
		offset, _ = strictPositiveAgentWorkspaceInteger(raw)
	}
	if raw, found := input["limit"]; found {
		limit, _ = strictPositiveAgentWorkspaceInteger(raw)
	}
	reserved := copyMapAny(metadata)
	extraction := map[string]any{
		"status": "no_extractable_text", "method": "pdftotext", "pages": pages,
		"size_bytes": int64(math.MaxInt64), "total_bytes": int64(math.MaxInt64), "truncated": true,
	}
	if len(pages) > 0 {
		// Either category may contain every selected page. Reserve both maxima
		// before the original line window chooses its serialized byte budget.
		extraction["text_pages"] = pages
		extraction["no_text_pages"] = pages
	}
	reserved["text_extraction"] = extraction
	view, err := readAgentWorkspaceTextWindowWithMetadata(ctx, counted, filename, contentType, size, offset, limit, reserved)
	if err != nil {
		return nil, err
	}
	if view["_policy_skip"] == true || !counted.eof {
		// A rejected text window may stop before EOF. Leave cancellation and
		// the join to the original defer instead of waiting on a blocked pipe.
		return nil, errors.New("PDF text extractor did not provide a complete UTF-8 stream")
	}
	// EOF can precede the producer's successful-page observation and final
	// cleanup. Its original join publishes immutable coverage without a race.
	<-finished
	if coverage.hasText {
		extraction["status"] = "parsed"
	}
	if len(pages) > 0 {
		extraction["text_pages"] = coverage.textPages
		extraction["no_text_pages"] = coverage.noTextPages
	}
	extraction["size_bytes"] = len(stringValue(view["content"]))
	extraction["total_bytes"] = counted.bytes
	extraction["truncated"] = view["truncated"] == true || numberValue(view["truncated_lines"]) > 0
	return view, nil
}

type agentWorkspacePDFTextCounter struct {
	io.Reader
	bytes int64
	eof   bool
}

func (reader *agentWorkspacePDFTextCounter) Read(buffer []byte) (int, error) {
	count, err := reader.Reader.Read(buffer)
	reader.bytes += int64(count)
	reader.eof = reader.eof || errors.Is(err, io.EOF)
	return count, err
}

func streamAgentWorkspacePDFText(ctx context.Context, converter, path string, pages []int) (*io.PipeReader, <-chan struct{}, *agentWorkspacePDFTextCoverage) {
	reader, writer := io.Pipe()
	finished := make(chan struct{})
	coverage := &agentWorkspacePDFTextCoverage{textPages: []int{}, noTextPages: []int{}}
	selected := append([]int(nil), pages...)
	if len(selected) == 0 {
		selected = []int{0}
	}
	go func() {
		defer close(finished)
		var failure error
		defer func() { _ = writer.CloseWithError(failure) }()
		for index, page := range selected {
			if failure = context.Cause(ctx); failure != nil {
				return
			}
			if len(pages) > 1 {
				prefix := ""
				if index > 0 {
					prefix = "\n"
				}
				if _, failure = fmt.Fprintf(writer, "%s[PDF page %d]\n", prefix, page); failure != nil {
					return
				}
			}
			arguments := []string{"-layout", "-nopgbrk", "-enc", "UTF-8"}
			if page > 0 {
				arguments = append(arguments, "-f", strconv.Itoa(page), "-l", strconv.Itoa(page))
			}
			arguments = append(arguments, path, "-")
			command := exec.CommandContext(ctx, converter, arguments...)
			command.WaitDelay = time.Second
			body := &agentWorkspacePDFBodyWriter{destination: writer}
			command.Stdout = body
			stderr := &agentWorkspaceBoundedCapture{limit: 4 << 10}
			command.Stderr = stderr
			if err := command.Run(); err != nil {
				failure = context.Cause(ctx)
				if failure == nil {
					failure = fmt.Errorf("PDF text extraction failed: %s", strings.TrimSpace(stderr.String()))
				}
				return
			}
			// Host provenance markers never pass through this observer. Failed
			// or cancelled extraction is not a successful empty-page result.
			coverage.record(page, body.hasText)
		}
	}()
	return reader, finished, coverage
}
