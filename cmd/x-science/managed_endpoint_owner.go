package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"x-science/internal/compute"
	"x-science/internal/persistence/workspace"
)

func runManagedEndpointOwnerCLI(args []string) error {
	flags := flag.NewFlagSet("managed-endpoint-owner", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	descriptor := flags.String("descriptor", "", "private server-created owner descriptor")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || !filepath.IsAbs(*descriptor) {
		return errors.New("managed endpoint worker requires one absolute private descriptor")
	}
	path, err := compute.ManagedEndpointOwnerWorkspacePath(*descriptor)
	if err != nil {
		return err
	}
	store, err := workspace.OpenExisting(path)
	if err != nil {
		return err
	}
	defer store.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return compute.RunManagedEndpointOwnerWorker(ctx, *descriptor, store)
}
