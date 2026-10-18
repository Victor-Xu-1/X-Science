package kernel

import (
	"context"
	"errors"
	"time"
)

// responseAfterExit is called only after done. The finite protocol buffer and
// already received responses may contain the exact terminal that preceded EOF.
// An unrelated ExecID or an aborted drain must never become successful evidence.
func (w *Worker) responseAfterExit(execID string, retained ...*Response) (Response, error) {
	var result Response
	for _, response := range retained {
		if response != nil && response.ID == execID {
			result = *response
			break
		}
	}
	for remaining := cap(w.responses); result.ID == "" && remaining > 0; remaining-- {
		select {
		case response := <-w.responses:
			if response.ID == execID {
				result = response
			}
		default:
			remaining = 0
		}
	}
	var drainErr error
	if w.output != nil {
		drainErr = w.output.drainErr
	}
	if result.ID != "" && drainErr == nil {
		return result, nil
	}
	return result, errors.Join(w.stoppedError(), drainErr)
}

func (execution *managedExecution) workerExitOutcome(startedAt time.Time, retained ...*Response) ExecutionOutcome {
	response, err := execution.worker.responseAfterExit(execution.request.ExecID, retained...)
	response = execution.withUserStopReason(response)
	timedOut := execution.isTimedOut()
	if err == nil && timedOut {
		err = context.DeadlineExceeded
	}
	err = execution.withUserStopError(err)
	return ExecutionOutcome{Response: response, Err: err, TimedOut: timedOut, StartedAt: startedAt}
}

func (execution *managedExecution) terminalResponseOutcome(response Response, startedAt time.Time) ExecutionOutcome {
	if execution.worker.hasExited() {
		<-execution.worker.done
		return execution.workerExitOutcome(startedAt, &response)
	}
	return ExecutionOutcome{
		Response: execution.withUserStopReason(response),
		TimedOut: execution.isTimedOut(), StartedAt: startedAt,
	}
}

func (w *Worker) withRestartNotice(response Response) Response {
	if response.ID != "" {
		if state := lifecycleWorkerState(w); state != nil {
			state.mu.Lock()
			response = consumeRKernelRestartNotice(state, state.generation, response)
			state.mu.Unlock()
		}
	}
	return response
}
