//go:build linux

package kernel

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"x-science/internal/kernelcontract/bashsource"
)

type startupProtocol struct {
	command *exec.Cmd
	input   io.WriteCloser
	frames  chan map[string]json.RawMessage
}

func startStartupProtocol(t *testing.T, executable string, args, environment []string, workspace string) *startupProtocol {
	t.Helper()
	// Callers pass Python sessionRuntime argv, never the egress shell. Isolated
	// Python ignores PYTHONDONTWRITEBYTECODE, so source fixtures need explicit -B.
	command := exec.Command(executable, append([]string{"-B"}, args...)...)
	command.Env, command.Dir = mergeKernelEnvironment(environment, map[string]string{"HOME": workspace, "PYTHONDONTWRITEBYTECODE": "1"}), workspace
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	p := &startupProtocol{command, input, make(chan map[string]json.RawMessage, 16)}
	go func() {
		defer close(p.frames)
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		for scanner.Scan() {
			var frame map[string]json.RawMessage
			if json.Unmarshal(scanner.Bytes(), &frame) != nil {
				return
			}
			p.frames <- frame
		}
	}()
	t.Cleanup(func() {
		_ = input.Close()
		done := make(chan error, 1)
		go func() { done <- command.Wait() }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
			<-done
		}
	})
	return p
}

func (p *startupProtocol) send(t *testing.T, id, command, workspace string) {
	t.Helper()
	code, err := bashsource.BashPythonWrapper(command)
	if err != nil {
		t.Fatal(err)
	}
	frame, _ := json.Marshal(request{ID: id, Code: code, WorkspaceDir: workspace, WorkingDir: workspace})
	if err := bashsource.ValidateWorkerFrame(bashsource.CanonicalBashStdlibV1, frame); err != nil {
		t.Fatal(err)
	}
	if _, err := p.input.Write(append(frame, '\n')); err != nil {
		t.Fatal(err)
	}
}

func (p *startupProtocol) result(t *testing.T, id string) Response {
	t.Helper()
	timeout := time.NewTimer(12 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case frame, ok := <-p.frames:
			if !ok {
				t.Fatal("worker protocol ended before result")
			}
			if _, event := frame["type"]; event {
				continue
			}
			payload, _ := json.Marshal(frame)
			var response Response
			if err := json.Unmarshal(payload, &response); err != nil || response.ID != id {
				t.Fatalf("unexpected response: %s %v", payload, err)
			}
			return response
		case <-timeout.C:
			t.Fatal("worker protocol result deadline")
		}
	}
}

func TestBashStartupRealProtocolPreservesShellFilesAndChildSite(t *testing.T) {
	manager, executable, generation := startupManagerFixture(t)
	workspace := t.TempDir()
	probe := exec.Command(executable, "-I", "-c", "import sysconfig;print(sysconfig.get_path('purelib'))")
	purelib, err := probe.Output()
	if err != nil {
		t.Fatal(err)
	}
	directory := strings.TrimSpace(string(purelib))
	if !strings.HasPrefix(directory, filepath.Dir(filepath.Dir(executable))+"/") {
		t.Fatal("fixture escaped runtime")
	}
	marker := filepath.Join(workspace, "site-pids")
	hook := "import os,pathlib; pathlib.Path(" + strconv.Quote(marker) + ").open('a').write(str(os.getpid())+'\\n')\n"
	if err := os.WriteFile(filepath.Join(directory, "startup-fixture.pth"), []byte(hook), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "startup_fixture.py"), []byte("VALUE=42\n"), 0600); err != nil {
		t.Fatal(err)
	}
	spec := startupSessionFixture(generation, workspace)
	selected, args, environment, err := manager.sessionRuntime(spec)
	if err != nil {
		t.Fatal(err)
	}
	environment = mergeKernelEnvironment(environment, map[string]string{"PYTHONDONTWRITEBYTECODE": "1"})
	p := startStartupProtocol(t, selected, args, environment, workspace)
	p.send(t, "first", "export X_SCIENCE_STARTUP_FIXTURE=present; mkdir sub; cd sub; printf persisted > result.txt; printf first", workspace)
	if response := p.result(t, "first"); response.Error != "" || response.Stdout != "first" {
		t.Fatalf("first: %#v", response)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("supervisor ran a .pth startup hook")
	}
	p.send(t, "second", "printf '%s\\n' \"${X_SCIENCE_STARTUP_FIXTURE-unset}\"; pwd; cat sub/result.txt; exit 7", workspace)
	response := p.result(t, "second")
	if response.Error != "" || response.Stdout != "unset\n"+workspace+"\npersisted" || !strings.Contains(response.Stderr, bashsource.BashExitPrefix+"7") {
		t.Fatalf("shell state/cwd/file/exit: %#v", response)
	}
	childCode := "import os,sys,startup_fixture;print('child=%d isolated=%d no_site=%d value=%d'%(os.getpid(),sys.flags.isolated,sys.flags.no_site,startup_fixture.VALUE))"
	p.send(t, "child", fmt.Sprintf("%s -c %s", strconv.Quote(executable), strconv.Quote(childCode)), workspace)
	response = p.result(t, "child")
	if response.Error != "" || !strings.Contains(response.Stdout, "isolated=0 no_site=0 value=42") {
		t.Fatalf("scientific child contract: %#v", response)
	}
	pids, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal("child .pth startup was suppressed", err)
	}
	for _, pid := range strings.Fields(string(pids)) {
		if pid == strconv.Itoa(p.command.Process.Pid) {
			t.Fatal("site initialized supervisor")
		}
	}
}

func TestBashStartupRealProtocolCancellationRemainsFunctional(t *testing.T) {
	manager, _, generation := startupManagerFixture(t)
	workspace := t.TempDir()
	executable, args, environment, err := manager.sessionRuntime(startupSessionFixture(generation, workspace))
	if err != nil {
		t.Fatal(err)
	}
	p := startStartupProtocol(t, executable, args, environment, workspace)
	marker := filepath.Join(workspace, "started")
	p.send(t, "cancel", "printf ready > "+strconv.Quote(marker)+"; exec /bin/sleep 30", workspace)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("owned child did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := syscall.Kill(-p.command.Process.Pid, syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	if response := p.result(t, "cancel"); !response.Interrupted {
		t.Fatalf("cancellation did not settle: %#v", response)
	}
}
