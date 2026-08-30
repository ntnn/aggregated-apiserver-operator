// Command api-aggregator serves one AggregatedAPI.
package main

import (
	"flag"
	"fmt"
	"os"

	"golang.org/x/sync/errgroup"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/ntnn/aggregated-apiserver-operator/pkg/apiserver"
	"github.com/ntnn/aggregated-apiserver-operator/pkg/controllers/api-aggregator/config"
	_ "github.com/ntnn/aggregated-apiserver-operator/pkg/register"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	opts := config.Options{}
	opts.RegisterFlags(flag.CommandLine)

	serverOpts := apiserver.Options{}
	serverOpts.RegisterFlags(flag.CommandLine)

	zapOpts := zap.Options{}
	zapOpts.BindFlags(flag.CommandLine)

	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOpts)))

	server, err := apiserver.New(serverOpts)
	if err != nil {
		return fmt.Errorf("building aggregated API server: %w", err)
	}
	opts.Server = server

	hostConfig, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("loading host-cluster rest config: %w", err)
	}

	mgr, err := ctrl.NewManager(hostConfig, manager.Options{})
	if err != nil {
		return fmt.Errorf("building manager: %w", err)
	}

	if err := config.Setup(mgr, opts); err != nil {
		return fmt.Errorf("setting up controllers: %w", err)
	}

	group, ctx := errgroup.WithContext(ctrl.SetupSignalHandler())
	group.Go(func() error {
		if err := mgr.Start(ctx); err != nil {
			return fmt.Errorf("running manager: %w", err)
		}
		return nil
	})
	group.Go(func() error {
		if err := server.Run(ctx); err != nil {
			return fmt.Errorf("running aggregated API server: %w", err)
		}
		return nil
	})
	return group.Wait()
}
