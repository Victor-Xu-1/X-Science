//go:build linux

package kernel

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"x-science/internal/networkpolicy"
)

const nativeNodeEgressHost = "native-node.example.com"
const nativeNodeEgressBody = "owned-native-node-egress"

const nativeNodeEgressScript = `
const [client, target] = process.argv.slice(2);
async function request() {
  let body;
  if (client === 'https') {
    body = await new Promise((resolve, reject) => {
      const req = require('node:https').get(target, { headers: { Connection: 'close' } }, res => {
        if (res.statusCode !== 200) { res.resume(); reject(new Error('HTTP status')); return; }
        let value = '';
        res.setEncoding('utf8');
        res.on('data', chunk => { value += chunk; });
        res.on('error', reject);
        res.on('end', () => resolve(value));
      });
      req.setTimeout(10000, () => req.destroy(new Error('request deadline')));
      req.on('error', reject);
    });
  } else {
    const res = await fetch(target, { signal: AbortSignal.timeout(10000), headers: { Connection: 'close' } });
    if (!res.ok) throw new Error('HTTP status');
    body = await res.text();
  }
  process.stdout.write(JSON.stringify({ version: process.version, client, body }) + '\n');
}
request().then(() => process.exit(0), error => {
  process.stderr.write('native Node request failed: ' + (error.code || error.name) + '\n');
  process.exit(23);
});
`

type nativeNodeEgressFixture struct {
	manager  *Manager
	proxy    *kernelEgressProxy
	node     string
	script   string
	caBundle string
	requests atomic.Int64
	dials    atomic.Int64
}

func newNativeNodeEgressFixture(t *testing.T, denied bool, handler http.HandlerFunc) *nativeNodeEgressFixture {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("native Node executable is unavailable")
	}
	versionContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	version, err := exec.CommandContext(versionContext, node, "--version").Output()
	if err != nil {
		t.Fatalf("read actual Node version: %v", err)
	}
	var major, minor, patch int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(version)), "v%d.%d.%d", &major, &minor, &patch); err != nil ||
		!((major == 22 && minor >= 21) || (major == 24 && minor >= 5)) {
		t.Skipf("native default-agent proxy support requires Node22.21+ or24.5+; actual=%q", version)
	}
	t.Logf("actual native Node executable=%s version=%s", node, strings.TrimSpace(string(version)))
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate owned egress assets")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	workspace := t.TempDir()
	fixture := &nativeNodeEgressFixture{
		manager: &Manager{config: Config{AssetRoot: filepath.Join(root, "assets", "optional")}},
		node:    node, script: filepath.Join(workspace, "native-node.cjs"), caBundle: filepath.Join(workspace, "ca.pem"),
	}
	if err := os.WriteFile(fixture.script, []byte(nativeNodeEgressScript), 0o600); err != nil {
		t.Fatal(err)
	}
	certificate := nativeNodeEgressCertificate(t, fixture.caBundle)
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.requests.Add(1)
		if handler != nil {
			handler(writer, request)
			return
		}
		_, _ = io.WriteString(writer, nativeNodeEgressBody)
	}))
	upstream.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	upstream.Config.ErrorLog = log.New(io.Discard, "", 0)
	upstream.StartTLS()
	t.Cleanup(upstream.Close)
	var deniedHosts []string
	if denied {
		deniedHosts = []string{nativeNodeEgressHost}
	}
	if admitted := networkpolicy.AllowsHost(nativeNodeEgressHost, []string{nativeNodeEgressHost}, deniedHosts); admitted == denied {
		t.Fatalf("controlled public-host fixture has incorrect admission: admitted=%t denied=%t", admitted, denied)
	}
	fixture.proxy, err = startKernelEgressProxyWithOptions(workspace, "native-node-egress", []string{nativeNodeEgressHost}, deniedHosts,
		kernelEgressProxyOptions{
			proxy: func(*http.Request) (*url.URL, error) { return nil, nil },
			lookup: func(_ context.Context, host string) ([]net.IPAddr, error) {
				if host != nativeNodeEgressHost {
					return nil, fmt.Errorf("unexpected controlled hostname %q", host)
				}
				return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
			},
			dial: func(ctx context.Context, address string) (net.Conn, error) {
				if address != "93.184.216.34:443" {
					return nil, fmt.Errorf("destination was not pinned to public HTTPS: %q", address)
				}
				fixture.dials.Add(1)
				return (&net.Dialer{}).DialContext(ctx, "tcp", upstream.Listener.Addr().String())
			},
		})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fixture.proxy.Close)
	return fixture
}

func (fixture *nativeNodeEgressFixture) run(ctx context.Context, client, caBundle string) (string, string, error) {
	executable, arguments, environment, err := fixture.manager.wrapKernelEgressRuntime(
		"/bin/bash", []string{"--noprofile", "--norc", "-c", `exec "$@"`, "node-egress",
			fixture.node, fixture.script, client, "https://" + nativeNodeEgressHost + "/owned"},
		kernelEnvironment(nil), fixture.proxy.port, caBundle)
	if err != nil {
		return "", "", err
	}
	child := fixture.proxy.takeChild()
	if child == nil {
		return "", "", errors.New("owned forwarder descriptor was already consumed")
	}
	defer child.Close()
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Env = mergeKernelEnvironment(environment, map[string]string{"X_SCIENCE_KERNEL_EGRESS_CONTROL_FD": "3", "PYTHONDONTWRITEBYTECODE": "1"})
	command.ExtraFiles = []*os.File{child}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	command.Cancel = func() error {
		if err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL); errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		} else {
			return err
		}
	}
	command.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err = command.Run()
	return stdout.String(), stderr.String(), err
}

func nativeNodeEgressFailed(err error) bool {
	var exitError *exec.ExitError
	return errors.As(err, &exitError) && exitError.ExitCode() == 23
}

func (fixture *nativeNodeEgressFixture) assertReleased(t *testing.T) {
	t.Helper()
	fixture.proxy.Close()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		fixture.proxy.connectionMu.Lock()
		remaining := len(fixture.proxy.connections)
		fixture.proxy.connectionMu.Unlock()
		connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(fixture.proxy.port)), 50*time.Millisecond)
		if connection != nil {
			_ = connection.Close()
		}
		if remaining == 0 && err != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("owned forwarder listener or policy tunnel remained after child exit")
}

func nativeNodeEgressCertificate(t *testing.T, caPath string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: nativeNodeEgressHost}, DNSNames: []string{nativeNodeEgressHost},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(caPath, certificatePEM, 0o600); err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.X509KeyPair(certificatePEM, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	return certificate
}
