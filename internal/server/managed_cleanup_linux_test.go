//go:build linux

package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kernelruntime "x-science/internal/kernel"
)

func TestTryDrainIdleRefusesUnsettledManagedEnvironmentOwner(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "started")
	installer := filepath.Join(root, "installer")
	quoted := "'" + strings.ReplaceAll(marker, "'", "'\\''") + "'"
	if err := os.WriteFile(installer, []byte("#!/bin/sh\nprintf started > "+quoted+"\nexec /bin/sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	manager := kernelruntime.NewManager(kernelruntime.Config{Micromamba: installer,
		CondaHome: filepath.Join(root, "conda"), CondaEnvsPath: filepath.Join(root, "conda", "envs")})
	supervisorCtx, stopSupervisor := context.WithCancel(context.Background())
	supervisorDone := make(chan error, 1)
	go func() { supervisorDone <- manager.RunManagedEnvironmentSupervisor(supervisorCtx) }()
	t.Cleanup(func() {
		stopSupervisor()
		select {
		case <-supervisorDone:
		case <-time.After(time.Second):
			t.Error("fixture environment supervisor did not stop")
		}
	})
	deadline := time.Now().Add(time.Second)
	for !manager.ManagedEnvironmentSupervisorReady() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, err := manager.CreateManagedEnvironment(ctx, kernelruntime.CreateManagedEnvironmentInput{
			Name: "analysis", Language: "python", OperationID: "drain-physical-owner"})
		done <- err
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("fixture installer did not physically settle")
		}
	})
	deadline = time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture installer never started")
		}
		time.Sleep(time.Millisecond)
	}
	server := &Server{kernelManager: manager}
	if count := server.activeKernelOperationCount(); count == 0 || server.TryDrainIdle() {
		t.Fatalf("backend replacement ignored an unsettled physical owner: %d", count)
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("fixture installer unexpectedly succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled physical installer did not settle")
	}
	if count := server.activeKernelOperationCount(); count != 0 || !server.TryDrainIdle() {
		t.Fatalf("settled physical owner still blocks idle drain: %d", count)
	}
}
