// Package cmd wires the CLI. Commands are thin: they load config, open the
// store and call into packages (STANDARDS.md section 4).
package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/AutumnsGrove/Ivy/config"
	"github.com/AutumnsGrove/Ivy/gateway"
	"github.com/AutumnsGrove/Ivy/internal/webui"
	"github.com/AutumnsGrove/Ivy/store"
)

// New builds the root command tree for the given build version.
func New(version string) *cobra.Command {
	var configPath string
	root := &cobra.Command{
		Use:          "ivy",
		Short:        "Ivy is a self-hosted web mail client",
		SilenceUsage: true,
	}
	root.PersistentFlags().StringVar(&configPath, "config", "", "path to ivy.yaml")
	root.AddCommand(
		initCmd(&configPath),
		runCmd(&configPath, version),
		doctorCmd(&configPath, version),
	)
	return root
}

func initCmd(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create the data directory and migrate the databases",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(*configPath)
			if err != nil {
				return err
			}
			dbs, err := store.Open(cfg.DataDir)
			if err != nil {
				return err
			}
			defer dbs.Close()
			fmt.Fprintf(cmd.OutOrStdout(), "initialised %s\n", cfg.DataDir)
			return nil
		},
	}
}

func runCmd(configPath *string, version string) *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Run the Ivy server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(*configPath)
			if err != nil {
				return err
			}
			dbs, err := store.Open(cfg.DataDir)
			if err != nil {
				return err
			}
			defer dbs.Close()

			srv := &http.Server{
				Addr:    cfg.Listen,
				Handler: gateway.New(dbs, version, webui.FS).Handler(),
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			errCh := make(chan error, 1)
			go func() {
				fmt.Fprintf(cmd.OutOrStdout(), "ivy %s listening on %s\n", version, cfg.Listen)
				errCh <- srv.ListenAndServe()
			}()

			select {
			case <-ctx.Done():
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				return srv.Shutdown(shutdownCtx)
			case err := <-errCh:
				if errors.Is(err, http.ErrServerClosed) {
					return nil
				}
				return err
			}
		},
	}
}

func doctorCmd(configPath *string, version string) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check the configuration, data directory and databases",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(*configPath)
			if err != nil {
				return err
			}
			dbs, err := store.Open(cfg.DataDir)
			if err != nil {
				return err
			}
			defer dbs.Close()

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "ivy %s\n", version)
			fmt.Fprintf(out, "listen:   %s\n", cfg.Listen)
			fmt.Fprintf(out, "data dir: %s\n", cfg.DataDir)
			fmt.Fprintf(out, "accounts: %d\n", len(cfg.Accounts))
			fmt.Fprintf(out, "store:    ok\n")
			return nil
		},
	}
}
