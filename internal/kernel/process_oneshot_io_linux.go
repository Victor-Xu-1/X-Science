//go:build linux

package kernel

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

type oneShotOutput struct {
	read   *os.File
	write  *os.File
	target io.Writer
	done   chan struct{}
	err    error
}

// Only this object writes caller-owned output buffers. Unknown-outcome return
// closes its read ends and joins every copier before exposing those buffers.
type oneShotIO struct {
	outputs []*oneShotOutput
	writes  sync.Mutex
}

type oneShotOutputWriter struct {
	owner  *oneShotIO
	target io.Writer
}

func (writer oneShotOutputWriter) Write(raw []byte) (int, error) {
	writer.owner.writes.Lock()
	defer writer.owner.writes.Unlock()
	return writer.target.Write(raw)
}

func prepareOneShotIO(source, helper *exec.Cmd) (*oneShotIO, error) {
	owner := &oneShotIO{}
	for _, output := range []struct {
		target io.Writer
		child  *io.Writer
	}{{source.Stdout, &helper.Stdout}, {source.Stderr, &helper.Stderr}} {
		if output.target == nil {
			continue
		}
		if output.child == &helper.Stderr && equalOneShotWriters(source.Stdout, source.Stderr) {
			helper.Stderr = helper.Stdout
			continue
		}
		if file, ok := output.target.(*os.File); ok {
			*output.child = file
			continue
		}
		read, write, err := os.Pipe()
		if err != nil {
			owner.abortStart()
			return nil, err
		}
		owner.outputs = append(owner.outputs, &oneShotOutput{
			read: read, write: write, target: output.target, done: make(chan struct{}),
		})
		*output.child = write
	}
	return owner, nil
}

func equalOneShotWriters(left, right io.Writer) (equal bool) {
	// Match os/exec's shared-output behavior without requiring arbitrary
	// io.Writer implementations to have comparable dynamic values.
	defer func() { _ = recover() }()
	return left == right
}

func (owner *oneShotIO) started() {
	for _, output := range owner.outputs {
		_ = output.write.Close()
		go func(output *oneShotOutput) {
			_, output.err = io.Copy(oneShotOutputWriter{owner: owner, target: output.target}, output.read)
			_ = output.read.Close()
			close(output.done)
		}(output)
	}
}

func (owner *oneShotIO) abortStart() {
	for _, output := range owner.outputs {
		_ = output.read.Close()
		_ = output.write.Close()
	}
}

func (owner *oneShotIO) closeReaders() {
	for _, output := range owner.outputs {
		_ = output.read.Close()
	}
}

func (owner *oneShotIO) join() error {
	var failure error
	for _, output := range owner.outputs {
		<-output.done
		failure = errors.Join(failure, output.err)
	}
	return failure
}

func (owner *oneShotIO) detach() error {
	owner.closeReaders()
	return owner.join()
}

func (owner *oneShotIO) afterExit() error {
	// This timer starts only after the helper has exited. It may close our I/O
	// reads, never signal a live reaper or substitute for its ECHILD proof.
	var expired atomic.Bool
	timer := time.AfterFunc(workerProcessWaitDelay, func() {
		expired.Store(true)
		owner.closeReaders()
	})
	err := owner.join()
	timer.Stop()
	if expired.Load() {
		err = errors.Join(err, exec.ErrWaitDelay)
	}
	return err
}
