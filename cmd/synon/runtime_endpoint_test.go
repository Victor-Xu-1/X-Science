package main

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func privateRuntimeEndpointTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRuntimeEndpointReceiptBindsActualListenerAndCLI(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	path := filepath.Join(privateRuntimeEndpointTestDir(t), "endpoint.json")
	cleanup, err := publishRuntimeEndpoint(path, listener.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	var out bytes.Buffer
	if err := runRuntimeEndpointCLI([]string{"--file", path, "--pid", strconv.Itoa(os.Getpid())}, &out); err != nil {
		t.Fatal(err)
	}
	want := "http://" + listener.Addr().String() + "/health\n"
	if out.String() != want {
		t.Fatalf("endpoint = %q, want actual bound listener %q", out.String(), want)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("receipt is not private: %v %v", info, err)
		}
	}
	if _, err := readRuntimeEndpoint(path, os.Getpid()+1); err == nil {
		t.Fatal("another child pid accepted the receipt")
	}
	cleanup()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("owned receipt was not removed: %v", err)
	}
}

func TestRuntimeEndpointMapsWildcardBindingsByAddressFamily(t *testing.T) {
	for _, test := range []struct{ ip, want string }{
		{"0.0.0.0", "http://127.0.0.1:18886/health"},
		{"::", "http://[::1]:18886/health"},
		{"::1", "http://[::1]:18886/health"},
	} {
		t.Run(test.ip, func(t *testing.T) {
			got, err := runtimeListenerHealthURL(&net.TCPAddr{IP: net.ParseIP(test.ip), Port: 18886})
			if err != nil || got != test.want {
				t.Fatalf("health URL = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestRuntimeEndpointDoesNotOverwriteOrRemoveReplacedFiles(t *testing.T) {
	path := filepath.Join(privateRuntimeEndpointTestDir(t), "endpoint.json")
	address := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 18886}
	cleanup, err := publishRuntimeEndpoint(path, address)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publishRuntimeEndpoint(path, address); err == nil {
		t.Fatal("existing receipt was overwritten")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement data"), 0600); err != nil {
		t.Fatal(err)
	}
	cleanup()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "replacement data" {
		t.Fatalf("cleanup touched a replacement: %q, %v", got, err)
	}
}

func TestRuntimeEndpointRejectsUnsafeReceiptsAndPublication(t *testing.T) {
	for _, target := range []string{
		"https://127.0.0.1:18886/health", "http://example.invalid:18886/health",
		"http://0.0.0.0:18886/health", "http://127.0.0.1:18886/other",
		"http://u:p@127.0.0.1:18886/health", "http://127.0.0.1:18886/health?x=1",
		"http://127.0.0.1:18886/health#x", "http://127.0.0.1:0/health",
	} {
		t.Run(target, func(t *testing.T) {
			path := filepath.Join(privateRuntimeEndpointTestDir(t), "endpoint.json")
			payload, err := json.Marshal(runtimeEndpointReceipt{Schema: 1, PID: os.Getpid(), HealthURL: target})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, payload, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := readRuntimeEndpoint(path, os.Getpid()); err == nil {
				t.Fatal("unsafe endpoint accepted")
			}
		})
	}
	dir := privateRuntimeEndpointTestDir(t)
	path := filepath.Join(dir, "oversize.json")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", runtimeEndpointLimit+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readRuntimeEndpoint(path, os.Getpid()); err == nil {
		t.Fatal("oversize receipt accepted")
	}
	symlink := filepath.Join(dir, "link.json")
	if err := os.Symlink(path, symlink); err == nil {
		if _, err := readRuntimeEndpoint(symlink, os.Getpid()); err == nil {
			t.Fatal("symlink receipt accepted")
		}
		if _, err := publishRuntimeEndpoint(symlink, &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 18886}); err == nil {
			t.Fatal("symlink publication target overwritten")
		}
	}
}

func TestRuntimeEndpointRejectsUntrustedFileModesAndTrailingData(t *testing.T) {
	dir := privateRuntimeEndpointTestDir(t)
	path := filepath.Join(dir, "endpoint.json")
	payload, err := json.Marshal(runtimeEndpointReceipt{Schema: 1, PID: os.Getpid(), HealthURL: "http://127.0.0.1:18886/health"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(payload, []byte(` {"extra":true}`)...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readRuntimeEndpoint(path, os.Getpid()); err == nil {
		t.Fatal("trailing JSON accepted")
	}
	if err := os.WriteFile(path, payload, 0600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := readRuntimeEndpoint(path, os.Getpid()); err == nil {
			t.Fatal("public-readable receipt accepted")
		}
		if err := os.Chmod(path, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if _, err := readRuntimeEndpoint(path, os.Getpid()); err == nil {
			t.Fatal("public-readable receipt parent accepted")
		}
		blockedPath := filepath.Join(dir, "unpublished.json")
		if _, err := publishRuntimeEndpoint(blockedPath, &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 18886}); err == nil {
			t.Fatal("publication to a public-readable parent accepted")
		}
		if _, err := os.Lstat(blockedPath); !os.IsNotExist(err) {
			t.Fatalf("failed publication left a receipt: %v", err)
		}
		if err := os.Chmod(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := readRuntimeEndpoint(path, os.Getpid()); err != nil {
		t.Fatalf("valid private receipt rejected: %v", err)
	}
}

func TestRuntimeEndpointBindsRealIPv6Listener(t *testing.T) {
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("host IPv6 loopback unavailable: %v", err)
	}
	defer listener.Close()
	path := filepath.Join(privateRuntimeEndpointTestDir(t), "endpoint.json")
	cleanup, err := publishRuntimeEndpoint(path, listener.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	got, err := readRuntimeEndpoint(path, os.Getpid())
	if err != nil || got != "http://"+listener.Addr().String()+"/health" {
		t.Fatalf("IPv6 endpoint = %q, %v", got, err)
	}
}

func TestRuntimeEndpointServeFlagIsOptionalAndExplicit(t *testing.T) {
	flags, _, err := parseServeCommandFlags(nil, new(bytes.Buffer))
	if err != nil || flags.runtimeEndpointFile != "" {
		t.Fatalf("default serve receipt = %q, %v", flags.runtimeEndpointFile, err)
	}
	flags, _, err = parseServeCommandFlags([]string{"--runtime-endpoint-file", "/private/generation/endpoint.json"}, new(bytes.Buffer))
	if err != nil || flags.runtimeEndpointFile != "/private/generation/endpoint.json" {
		t.Fatalf("explicit serve receipt = %q, %v", flags.runtimeEndpointFile, err)
	}
}
