package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"syscall"

	"x-science/internal/compute"
	"x-science/internal/persistence/workspace"
)

func runManagedEndpointOwnerCLI(args []string) error {
	flags := flag.NewFlagSet("managed-endpoint-owner", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	bootstrap := flags.String("bootstrap", "", "private captured owner scope and requested operation")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *bootstrap == "" {
		return errors.New("managed endpoint worker requires one captured bootstrap scope")
	}
	input, err := compute.ParseManagedEndpointBootstrap(*bootstrap)
	if err != nil {
		return err
	}
	store, err := workspace.OpenExisting(input.Scope.WorkspacePath)
	if err != nil {
		return err
	}
	defer store.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return compute.RunManagedEndpointOwnerWorker(ctx, input, store)
}
