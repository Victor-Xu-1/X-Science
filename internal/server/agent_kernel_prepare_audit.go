package server

import "sync"

const maxKernelPrepareDiagnostics = 16

// Bounded per-run diagnostics piggyback on the original audit write. This is
// neither an execution queue nor an asynchronous persistence owner.
type kernelPrepareDiagnosticBuffer struct {
	mu      sync.Mutex
	records [maxKernelPrepareDiagnostics]kernelPrepareDiagnostic
	count   int
	dropped uint32
	closed  bool
	loss    kernelPrepareDiagnosticLoss
}

type kernelPrepareDiagnosticLoss struct {
	Unavailable bool
	Dropped     uint32
	Saturated   bool
}

func (buffer *kernelPrepareDiagnosticBuffer) append(record kernelPrepareDiagnostic) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	if buffer.closed {
		return // A late completion never changes the already-closed diagnostic.
	}
	if buffer.count == len(buffer.records) {
		if buffer.dropped < 1<<32-1 {
			buffer.dropped++
		}
		return
	}
	buffer.records[buffer.count] = record
	buffer.count++
}

func (buffer *kernelPrepareDiagnosticBuffer) take() ([]kernelPrepareDiagnostic, uint32) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	records := append([]kernelPrepareDiagnostic(nil), buffer.records[:buffer.count]...)
	dropped := buffer.dropped
	clear(buffer.records[:])
	buffer.count, buffer.dropped = 0, 0
	return records, dropped
}

func (buffer *kernelPrepareDiagnosticBuffer) takeForAttempt(attempt int) ([]kernelPrepareDiagnostic, uint32) {
	records, dropped := buffer.take()
	current := records[:0]
	for _, record := range records {
		if record.Attempt == attempt {
			current = append(current, record)
		} else if dropped < 1<<32-1 {
			dropped++
		}
	}
	return current, dropped
}

func addKernelPrepareDiagnostics(record map[string]any, diagnostics []kernelPrepareDiagnostic, dropped uint32) {
	if len(diagnostics) > 0 {
		record["kernel_preparation"] = diagnostics
	}
	if dropped > 0 {
		record["kernel_preparation_dropped"] = dropped
		if dropped == 1<<32-1 {
			record["kernel_preparation_drop_count_saturated"] = true
		}
	}
}

// Closing deliberately performs no I/O. If no original audit write followed a
// preparation, its diagnostics are unavailable, not a durable timing receipt.
func (buffer *kernelPrepareDiagnosticBuffer) close() kernelPrepareDiagnosticLoss {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	if !buffer.closed {
		buffer.closed = true
		buffer.loss.Unavailable = true
		remaining := uint64(buffer.dropped) + uint64(buffer.count)
		buffer.loss.Dropped = uint32(min(remaining, 1<<32-1))
		buffer.loss.Saturated = remaining >= 1<<32-1
		clear(buffer.records[:])
		buffer.count, buffer.dropped = 0, 0
	}
	return buffer.loss
}

func closeKernelPrepareDiagnostics(run *sessionRunnerChatRun) {
	if run != nil {
		run.kernelPrepareDiagnostics.close()
	}
}
