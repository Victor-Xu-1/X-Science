//go:build linux

package kernel

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"x-science/internal/kernelcontract/bashsource"
)

func TestBashStartupEgressBootstrapUsesSameIsolatedSupervisor(t *testing.T) {
	manager, executable, _ := startupManagerFixture(t)
	workspace := t.TempDir()
	probe := exec.Command(executable, "-I", "-c", "import sysconfig;print(sysconfig.get_path('purelib'))")
	purelib, err := probe.Output()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(workspace, "unexpected-site")
	hook := "import pathlib; pathlib.Path(" + strconv.Quote(marker) + ").touch()\n"
	if err := os.WriteFile(filepath.Join(strings.TrimSpace(string(purelib)), "startup-egress.pth"), []byte(hook), 0600); err != nil {
		t.Fatal(err)
	}
	shellHook := filepath.Join(workspace, "shell-hook")
	if err := os.WriteFile(shellHook, []byte("touch "+strconv.Quote(marker)), 0600); err != nil {
		t.Fatal(err)
	}
	control, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	parent, child := os.NewFile(uintptr(control[0]), "startup-parent"), os.NewFile(uintptr(control[1]), "startup-child")
	defer parent.Close()
	defer child.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	environment := kernelEnvironment(map[string]string{"X_SCIENCE_KERNEL_EGRESS_CONTROL_FD": "3", "PYTHONDONTWRITEBYTECODE": "1", "BASH_ENV": shellHook})
	program, args, environment, err := manager.wrapKernelEgressRuntime(executable, []string{"-I", "-S", "-c", "print('isolated-ready')"}, environment, port, "", bashsource.CanonicalBashStdlibV1)
	if err != nil {
		t.Fatal(err)
	}
	if program != "/bin/bash" || strings.Join(args[:4], " ") != "--noprofile --norc -p -c" {
		t.Fatalf("shell startup not isolated: %s %v", program, args)
	}
	if !strings.Contains(args[4], "\n  /bin/sleep 0.01\n") || strings.Contains(args[4], "$(seq ") {
		t.Fatal("restricted startup resolves a helper from the scientific PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, program, args...)
	command.Env = environment
	command.ExtraFiles = []*os.File{child}
	output, err := command.CombinedOutput()
	_ = child.Close()
	if err != nil || !strings.Contains(string(output), "isolated-ready") {
		t.Fatalf("bootstrap: %v %s", err, output)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("bootstrap ran inherited shell or site initialization")
	}
	legacyProgram, legacyArgs, _, err := manager.wrapKernelEgressRuntime("/usr/bin/python3", nil, nil, port, "")
	if err != nil || legacyProgram != "/bin/sh" || legacyArgs[0] != "-c" || strings.Count(legacyArgs[1], "/usr/bin/python3") != 2 || strings.Contains(legacyArgs[1], "-I -S") {
		t.Fatal("legacy bootstrap changed")
	}
}
