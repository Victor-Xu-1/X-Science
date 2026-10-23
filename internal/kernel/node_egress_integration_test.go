//go:build linux

package kernel

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// This runs the production wrapper, inherited Bash environment, real Node,
// FD forwarder and policy proxy, but intentionally does not launch bubblewrap.
// It proves the owned HTTPS route, not network-namespace confinement.
func TestKernelNativeNodeHTTPSUsesOwnedEgress(t *testing.T) {
	for _, client := range []string{"https", "fetch"} {
		t.Run(client, func(t *testing.T) {
			fixture := newNativeNodeEgressFixture(t, false, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			stdout, stderr, err := fixture.run(ctx, client, fixture.caBundle)
			if err != nil || !strings.Contains(stdout, nativeNodeEgressBody) {
				t.Fatalf("default Node %s did not consume the owned HTTPS route: err=%v stdout=%q stderr=%q dials=%d requests=%d",
					client, err, stdout, stderr, fixture.dials.Load(), fixture.requests.Load())
			}
			if fixture.requests.Load() != 1 || fixture.dials.Load() != 1 {
				t.Fatalf("expected one policy-pinned upstream request, dials=%d requests=%d", fixture.dials.Load(), fixture.requests.Load())
			}
			fixture.assertReleased(t)
		})
		t.Run(client+"-denied", func(t *testing.T) {
			fixture := newNativeNodeEgressFixture(t, true, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			stdout, stderr, err := fixture.run(ctx, client, fixture.caBundle)
			if !nativeNodeEgressFailed(err) || strings.Contains(stdout, nativeNodeEgressBody) {
				t.Fatalf("denied Node %s did not finish as an explicit failure: err=%v stdout=%q stderr=%q", client, err, stdout, stderr)
			}
			if fixture.requests.Load() != 0 || fixture.dials.Load() != 0 {
				t.Fatalf("denied destination reached upstream, dials=%d requests=%d", fixture.dials.Load(), fixture.requests.Load())
			}
			fixture.assertReleased(t)
		})
	}
}

func TestKernelNativeNodeEgressRetainsTLSVerification(t *testing.T) {
	for _, client := range []string{"https", "fetch"} {
		t.Run(client, func(t *testing.T) {
			fixture := newNativeNodeEgressFixture(t, false, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			stdout, stderr, err := fixture.run(ctx, client, "")
			if !nativeNodeEgressFailed(err) || strings.Contains(stdout, nativeNodeEgressBody) || fixture.dials.Load() < 1 || fixture.requests.Load() != 0 {
				t.Fatalf("untrusted TLS must fail after proxy admission, not bypass verification: err=%v stdout=%q stderr=%q dials=%d requests=%d",
					err, stdout, stderr, fixture.dials.Load(), fixture.requests.Load())
			}
			fixture.assertReleased(t)
		})
	}
}

func TestKernelNativeNodeEgressCancellationClosesOwnedResources(t *testing.T) {
	for _, client := range []string{"https", "fetch"} {
		t.Run(client, func(t *testing.T) {
			started, closed := make(chan struct{}), make(chan struct{})
			fixture := newNativeNodeEgressFixture(t, false, func(_ http.ResponseWriter, request *http.Request) {
				close(started)
				<-request.Context().Done()
				close(closed)
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, _, err := fixture.run(ctx, client, fixture.caBundle)
				done <- err
			}()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				cancel()
				<-done
				t.Fatal("real Node request never reached controlled HTTPS upstream")
			}
			cancel()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("canceled child reported success")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("owned Node/Bash process group did not stop")
			}
			select {
			case <-closed:
			case <-time.After(3 * time.Second):
				t.Fatal("cancellation left upstream request open")
			}
			fixture.assertReleased(t)
		})
	}
}
