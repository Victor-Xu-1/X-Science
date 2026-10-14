//go:build linux

package detached

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func commandCancellationFixture(t *testing.T, serve func(*net.UnixConn, CommandRequest) error) (Client, <-chan error) {
	t.Helper()
	dir, err := os.MkdirTemp("", "xsci-rpc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "rpc.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		connection, err := listener.AcceptUnix()
		if err != nil {
			done <- err
			return
		}
		defer connection.Close()
		_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
		frame, err := readBoundedCommandFrame(connection, maxCommandRequestBytes)
		if err != nil {
			done <- err
			return
		}
		request, err := DecodeCommandRequest(frame)
		if err == nil {
			err = serve(connection, request)
		}
		done <- err
	}()
	t.Cleanup(func() { _ = listener.Close() })
	return Client{SocketPath: path}, done
}

func cancellationCommand() CommandRequest {
	return CommandRequest{Version: ProtocolVersion, RequestID: "caller-cancel", Command: CommandProbe,
		BackendID: "backend", BackendGeneration: 1, ExecutionID: "execution", ControllerEpoch: 1, ControllerToken: strings.Repeat("a", 32)}
}

func TestCommandClientCancellationClosesBlockedRead(t *testing.T) {
	for _, mode := range []string{"lease_cancel", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			received := make(chan struct{})
			client, peerDone := commandCancellationFixture(t, func(connection *net.UnixConn, _ CommandRequest) error {
				close(received)
				_, err := io.Copy(io.Discard, connection)
				return err
			})
			cause := errors.New("caller lease was cancelled")
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			if mode == "deadline" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeoutCause(ctx, 100*time.Millisecond, context.DeadlineExceeded)
				defer stop()
				cause = context.DeadlineExceeded
			}
			finished := make(chan error, 1)
			go func() { _, err := client.Call(ctx, cancellationCommand()); finished <- err }()
			select {
			case <-received:
			case err := <-finished:
				t.Fatalf("caller failed before the blocked response: %v", err)
			case <-time.After(time.Second):
				t.Fatal("server did not receive command")
			}
			if mode == "lease_cancel" {
				cancel(cause)
			}
			select {
			case err := <-finished:
				if !errors.Is(err, cause) {
					t.Fatalf("caller cause was lost: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("cancelled RPC remained blocked")
			}
			select {
			case err := <-peerDone:
				if err != nil {
					t.Fatalf("cancelled client connection stayed open: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("server connection or handler leaked")
			}
		})
	}
}

func TestCommandClientPreservesServerAndIOResults(t *testing.T) {
	for _, outcome := range []string{"success", "server_rejection", "peer_eof"} {
		t.Run(outcome, func(t *testing.T) {
			client, peerDone := commandCancellationFixture(t, func(connection *net.UnixConn, request CommandRequest) error {
				if outcome == "peer_eof" {
					return nil
				}
				response := CommandResponse{Version: ProtocolVersion, RequestID: request.RequestID, OK: true}
				if outcome == "server_rejection" {
					response.OK, response.Code, response.Message = false, "stale", "Original control lease is stale"
				}
				encoded, err := EncodeCommandResponse(response)
				if err == nil {
					err = writeAll(connection, encoded)
				}
				return err
			})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			response, err := client.Call(ctx, cancellationCommand())
			var rejected *CommandError
			switch outcome {
			case "success":
				if err != nil || !response.OK {
					t.Fatalf("successful response changed: %+v %v", response, err)
				}
			case "server_rejection":
				if !errors.As(err, &rejected) || rejected.Code != "stale" {
					t.Fatalf("server rejection changed: %v", err)
				}
			case "peer_eof":
				if !errors.Is(err, io.EOF) {
					t.Fatalf("peer EOF changed: %v", err)
				}
			}
			if peerErr := <-peerDone; peerErr != nil {
				t.Fatal(peerErr)
			}
		})
	}
}
