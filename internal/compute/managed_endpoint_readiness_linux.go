//go:build linux

package compute

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

func (worker *managedEndpointWorker) verifyReady(ctx context.Context, instance ManagedEndpointOwnerInstance, receipt *ManagedEndpointOwnerReceipt) {
	parsed, err := url.Parse(instance.Registration.URL)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || parsed.Port() == "" {
		receipt.Failure = "managed endpoint readiness URL is invalid"
		return
	}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, ResponseHeaderTimeout: 3 * time.Second}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("managed endpoint readiness cannot redirect")
	}}
	probe := parsed.Scheme + "://" + parsed.Host + instance.Registration.LivePath
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, probe, nil)
		if err != nil {
			receipt.Failure = err.Error()
			return
		}
		response, err := client.Do(request)
		ready := false
		if err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
			_ = response.Body.Close()
			ready = response.StatusCode >= 200 && response.StatusCode < 300
		}
		if ready {
			if instance.Registration.Owner.Kind == "native-unit" {
				err = verifyManagedEndpointNativeListener(ctx, instance, worker.identity)
				if err == nil {
					receipt.Identity = worker.identity
				}
			} else {
				var runtime *managedEndpointDocker
				runtime, err = newManagedEndpointDocker()
				if err == nil {
					receipt.Identity, err = runtime.observeReady(ctx, instance)
				}
			}
			if err != nil {
				receipt.Failure = err.Error()
				return
			}
			receipt.OwnerVerified, receipt.Readiness = true, true
			return
		}
		select {
		case <-ctx.Done():
			receipt.Failure = fmt.Errorf("managed endpoint readiness: %w", context.Cause(ctx)).Error()
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}
