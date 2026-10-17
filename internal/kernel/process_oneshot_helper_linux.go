//go:build linux

package kernel

import (
	"encoding/binary"
	"encoding/gob"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	oneShotHelperArgument = "--x-science-internal-one-shot-v1"
	oneShotReceiptMagic   = "XSCOS001"
	oneShotReceiptSize    = 28
	oneShotLaunchGrant    = 1
	oneShotReady          = 1
	oneShotIncomplete     = 2
	oneShotComplete       = 3
	oneShotSetupFailure   = 1
	oneShotStartFailure   = 2
	oneShotCleanupFailure = 3
	oneShotWaitFailure    = 4
)

type oneShotReceipt struct {
	kind    uint32
	status  syscall.WaitStatus
	failure uint32
	errno   syscall.Errno
	root    bool
}

func init() {
	if len(os.Args) < 2 || os.Args[1] != oneShotHelperArgument {
		return
	}
	// The target's parent-death signal belongs to this thread, which remains
	// alive through the final wait. Only this child helper is a subreaper.
	runtime.LockOSThread()
	os.Exit(runOneShotHelper(os.Args[2:]))
}

func runOneShotHelper(arguments []string) int {
	if len(arguments) != 1 {
		return 125
	}
	extraFiles, err := strconv.Atoi(arguments[0])
	if err != nil || extraFiles < 0 || extraFiles > int(^uint(0)>>1)-4 {
		return 125
	}
	control := os.NewFile(uintptr(3+extraFiles), "one-shot-control")
	receiptFile := os.NewFile(uintptr(4+extraFiles), "one-shot-receipt")
	if !oneShotPipe(control) || !oneShotPipe(receiptFile) {
		return 125
	}
	defer control.Close()
	defer receiptFile.Close()
	unix.CloseOnExec(int(control.Fd()))
	unix.CloseOnExec(int(receiptFile.Fd()))
	result := oneShotReceipt{kind: oneShotComplete, failure: oneShotSetupFailure}
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		result.errno = oneShotErrno(err)
		_ = writeOneShotReceipt(receiptFile, result)
		return 125
	}
	decoder := gob.NewDecoder(control)
	var request oneShotRequest
	if err := decoder.Decode(&request); err != nil || request.Path == "" || request.ExtraFiles != extraFiles {
		_ = writeOneShotReceipt(receiptFile, result)
		return 125
	}
	if err := writeOneShotReceipt(receiptFile, oneShotReceipt{kind: oneShotReady}); err != nil {
		return 125
	}
	// No target exists before a successful parent grant. Known setup/protocol
	// failures or EOF before that grant are safely non-executing outcomes.
	// No grant bytes exist until Decode has returned and Ready is written.
	// Gob preserves arbitrary argv/environment bytes, unlike JSON strings.
	lifetime := control
	var grant [1]byte
	if _, err := io.ReadFull(lifetime, grant[:]); err != nil || grant[0] != oneShotLaunchGrant {
		result.errno = syscall.ECANCELED
		_ = writeOneShotReceipt(receiptFile, result)
		return 125
	}
	events := make(chan os.Signal, 1)
	signal.Notify(events, syscall.SIGCHLD, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(events)
	parentLost := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, lifetime)
		close(parentLost)
	}()
	select {
	case <-parentLost:
		result.errno = syscall.ECANCELED
		_ = writeOneShotReceipt(receiptFile, result)
		return 125
	default:
	}
	target := &exec.Cmd{
		Path: request.Path, Args: request.Args, Env: request.Environment,
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
		SysProcAttr: &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL},
	}
	for index := 0; index < extraFiles; index++ {
		fd := 3 + index
		var file *os.File
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err == nil {
			file = os.NewFile(uintptr(fd), "one-shot-target-extra")
		} else if !errors.Is(err, syscall.EBADF) {
			result.errno = oneShotErrno(err)
			_ = writeOneShotReceipt(receiptFile, result)
			return 125
		}
		target.ExtraFiles = append(target.ExtraFiles, file)
	}
	err = target.Start()
	for _, file := range target.ExtraFiles {
		if file != nil {
			_ = file.Close()
		}
	}
	if err != nil {
		result.failure, result.errno = oneShotStartFailure, oneShotErrno(err)
		_ = writeOneShotReceipt(receiptFile, result)
		return 125
	}
	// No exec.Cmd.Wait competes with this reaper. The target uses inherited
	// files, so there are no target-side I/O copiers needing another waiter.
	defer target.Process.Release()
	result = reapOneShotTarget(target.Process.Pid, parentLost, events, receiptFile)
	if err := writeOneShotReceipt(receiptFile, result); err != nil {
		return 125
	}
	if result.failure != 0 || !result.root {
		return 125
	}
	if result.status.Signaled() {
		return 128 + int(result.status.Signal())
	}
	return result.status.ExitStatus()
}

func oneShotPipe(file *os.File) bool {
	if file == nil {
		return false
	}
	var stat unix.Stat_t
	return unix.Fstat(int(file.Fd()), &stat) == nil && stat.Mode&unix.S_IFMT == unix.S_IFIFO
}

func oneShotErrno(err error) syscall.Errno {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno
	}
	return 0
}

func reapOneShotTarget(root int, parentLost <-chan struct{}, events <-chan os.Signal, receipts *os.File) oneShotReceipt {
	result := oneShotReceipt{kind: oneShotComplete}
	cleaning, reported := false, false
	reportIncomplete := func(failure uint32, err error) {
		if result.failure == 0 {
			result.failure, result.errno = failure, oneShotErrno(err)
		}
		if !reported {
			observed := result
			observed.kind = oneShotIncomplete
			_ = writeOneShotReceipt(receipts, observed)
			reported = true
		}
	}
	for {
		// SIGCHLD coalesces. Drain all statuses with a single wait owner, and
		// preserve the root result independently of adopted descendants.
		for {
			var status syscall.WaitStatus
			pid, err := syscall.Wait4(-1, &status, syscall.WNOHANG|syscall.WALL, nil)
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			if errors.Is(err, syscall.ECHILD) {
				if !result.root {
					result.failure = oneShotWaitFailure
				}
				return result
			}
			if err != nil {
				reportIncomplete(oneShotWaitFailure, err)
				cleaning = true
				break
			}
			if pid == 0 {
				break
			}
			if pid == root {
				result.status, result.root, cleaning = status, true, true
			}
		}
		if cleaning {
			if err := signalOneShotChildren(); err != nil {
				// Visibility cannot depend on another SIGCHLD arriving after
				// an incomplete scan or failed signal. Retain the reaper but
				// immediately publish a typed non-success observation.
				reportIncomplete(oneShotCleanupFailure, err)
			}
		}
		select {
		case event := <-events:
			if event != syscall.SIGCHLD {
				cleaning = true
			}
		case <-parentLost:
			parentLost = nil
			cleaning = true
		}
	}
}

func signalOneShotChildren() error {
	budget := int(^uint(0) >> 1)
	children, complete := linuxObservedChildren("/proc", os.Getpid(), &budget)
	var failure error
	if !complete {
		failure = errors.New("owned child membership snapshot is incomplete")
	}
	for _, pid := range children {
		member, err := readLinuxObservedCounter("/proc", pid)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			failure = errors.Join(failure, err)
			continue
		}
		if member.process.ParentPID != os.Getpid() {
			continue
		}
		// The sole reaper never reaps during signalling. An owned exiting
		// child retains its PID until the next wait; reuse the handle/start
		// identity fence, not a negative PGID or a process-name match.
		failure = errors.Join(failure, signalLinuxProcessMember(member, syscall.SIGKILL))
	}
	// Empty/partial snapshots never prove settlement. Only ECHILD does.
	return failure
}

func readOneShotReceipt(file *os.File) (oneShotReceipt, error) {
	var raw [oneShotReceiptSize]byte
	if _, err := io.ReadFull(file, raw[:]); err != nil {
		return oneShotReceipt{}, err
	}
	if string(raw[:8]) != oneShotReceiptMagic {
		return oneShotReceipt{}, errors.New("invalid one-shot receipt")
	}
	root := binary.LittleEndian.Uint32(raw[24:28])
	receipt := oneShotReceipt{
		kind:    binary.LittleEndian.Uint32(raw[8:12]),
		status:  syscall.WaitStatus(binary.LittleEndian.Uint32(raw[12:16])),
		failure: binary.LittleEndian.Uint32(raw[16:20]),
		errno:   syscall.Errno(binary.LittleEndian.Uint32(raw[20:24])), root: root == 1,
	}
	if root > 1 || receipt.kind < oneShotReady || receipt.kind > oneShotComplete ||
		receipt.failure > oneShotWaitFailure ||
		(receipt.failure == 0 && receipt.errno != 0) ||
		(receipt.root && !receipt.status.Exited() && !receipt.status.Signaled()) ||
		(!receipt.root && receipt.status != 0) ||
		(receipt.kind == oneShotReady && (receipt.root || receipt.failure != 0 || receipt.errno != 0)) ||
		(receipt.kind == oneShotIncomplete && receipt.failure == 0) ||
		(receipt.kind == oneShotComplete && !receipt.root && receipt.failure == 0) {
		return oneShotReceipt{}, errors.New("invalid one-shot terminal result")
	}
	return receipt, nil
}

func writeOneShotReceipt(file *os.File, receipt oneShotReceipt) error {
	var raw [oneShotReceiptSize]byte
	copy(raw[:8], oneShotReceiptMagic)
	binary.LittleEndian.PutUint32(raw[8:12], receipt.kind)
	binary.LittleEndian.PutUint32(raw[12:16], uint32(receipt.status))
	binary.LittleEndian.PutUint32(raw[16:20], receipt.failure)
	binary.LittleEndian.PutUint32(raw[20:24], uint32(receipt.errno))
	if receipt.root {
		binary.LittleEndian.PutUint32(raw[24:28], 1)
	}
	count, err := file.Write(raw[:])
	if count != len(raw) && err == nil {
		err = io.ErrShortWrite
	}
	return err
}
