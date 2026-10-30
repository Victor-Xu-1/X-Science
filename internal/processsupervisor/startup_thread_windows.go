//go:build windows

package processsupervisor

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"

	"golang.org/x/sys/windows"
)

var startupKernel = windows.NewLazySystemDLL("kernel32.dll")
var startupRequests sync.Map
var startupSequence atomic.Uintptr
var startupCallbackOnce sync.Once
var startupCallback uintptr

type nativeStartupRequest struct {
	work      func()
	completed chan struct{}
	panicErr  error
}

type startupThreadFailure struct {
	err     error
	retired bool
}

func (failure *startupThreadFailure) Error() string { return failure.err.Error() }
func (failure *startupThreadFailure) Unwrap() error { return failure.err }

func startupThreadHasExited(err error) bool {
	var failure *startupThreadFailure
	return err == nil || (errors.As(err, &failure) && failure.retired)
}

type startupThreadAPI struct {
	create func(uintptr, uintptr) (windows.Handle, error)
	wait   func(windows.Handle, uint32) (uint32, error)
	close  func(windows.Handle) error
}

var systemStartupThreadAPI = startupThreadAPI{
	create: func(callback, token uintptr) (windows.Handle, error) {
		handle, _, err := startupKernel.NewProc("CreateThread").Call(0, 0, callback, token, 0, 0)
		if handle == 0 {
			return 0, fmt.Errorf("create Windows startup thread: %w", err)
		}
		return windows.Handle(handle), nil
	},
	wait:  windows.WaitForSingleObject,
	close: windows.CloseHandle,
}

func runWindowsStartupThread(work func()) error {
	return runStartupThreadWithAPI(work, systemStartupThreadAPI)
}

func runStartupThreadWithAPI(work func(), api startupThreadAPI) error {
	startupCallbackOnce.Do(func() {
		// NewCallback registrations cannot be freed. Cache one ABI entry point;
		// only the per-call owned request token is allocated/retired for each run.
		startupCallback = syscall.NewCallback(func(token uintptr) uintptr {
			owned, ok := startupRequests.Load(token)
			if !ok {
				return 0
			}
			request := owned.(*nativeStartupRequest)
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			defer func() {
				if recover() != nil {
					request.panicErr = errors.New("Windows startup thread callback panicked")
				}
				request.completed <- struct{}{}
			}()
			request.work()
			return 0 // Return to Windows; never ExitThread/Goexit across Go frames.
		})
	})
	token := startupSequence.Add(1)
	request := &nativeStartupRequest{work: work, completed: make(chan struct{}, 1)}
	startupRequests.Store(token, request)
	thread, err := api.create(startupCallback, token)
	if err != nil {
		startupRequests.Delete(token)
		return &startupThreadFailure{err: err}
	}
	defer api.close(thread)
	// Keep the caller in the real native wait while the foreign callback may
	// block on external IO; a Go-channel-only wait can falsely trigger Go's
	// deadlock detector because foreign-thread syscalls are not Go-owned Ms.
	state, waitErr := api.wait(thread, windows.INFINITE)
	// The buffered publication supplies the Go-memory happens-before edge;
	// the kernel wait independently proves actual native thread retirement.
	<-request.completed
	// Callback completion settles the Go request ownership independently of
	// whether the kernel wait proved physical thread exit. No registry root
	// needs to retain work -> attempt -> Cmd after this publication.
	startupRequests.Delete(token)
	if waitErr != nil || state != windows.WAIT_OBJECT_0 {
		// The body no longer accesses command state, but native retirement was
		// not proved. Preserve that uncertainty in the typed failure.
		return &startupThreadFailure{err: errors.Join(request.panicErr, fmt.Errorf("wait Windows startup thread: status=%#x error=%v", state, waitErr))}
	}
	if request.panicErr != nil {
		return &startupThreadFailure{err: request.panicErr, retired: true}
	}
	return nil
}

func detachStartupDebugger(pid uint32) error {
	ok, _, err := startupKernel.NewProc("DebugActiveProcessStop").Call(uintptr(pid))
	if ok == 0 {
		return fmt.Errorf("detach Windows startup debugger: %w", err)
	}
	return nil
}
