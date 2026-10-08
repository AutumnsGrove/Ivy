// Package cmd wires the CLI. Commands are thin: they load config, open the
// store and call into packages (STANDARDS.md section 4).
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/AutumnsGrove/Ivy/backup"
	"github.com/AutumnsGrove/Ivy/config"
	"github.com/AutumnsGrove/Ivy/events"
	"github.com/AutumnsGrove/Ivy/gateway"
	"github.com/AutumnsGrove/Ivy/internal/accountsvc"
	"github.com/AutumnsGrove/Ivy/internal/lockfile"
	"github.com/AutumnsGrove/Ivy/internal/webui"
	"github.com/AutumnsGrove/Ivy/store"
	"github.com/AutumnsGrove/Ivy/update"
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
		updateCmd(&configPath),
		backupCmd(&configPath),
		restoreCmd(&configPath),
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
			dbs, err := store.Open(cmd.Context(), cfg.DataDir)
			if err != nil {
				return err
			}
			defer dbs.Close()
			fmt.Fprintf(cmd.OutOrStdout(), "initialised %s\n", cfg.DataDir)
			reportHosts(cmd.OutOrStdout(), cfg)
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
			// One server per data directory. The lock dies with the process, so a
			// crash never leaves a stale lock behind to refuse the next start.
			if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
				return fmt.Errorf("create data dir: %w", err)
			}
			lock, err := lockfile.Acquire(filepath.Join(cfg.DataDir, backup.LockName))
			if err != nil {
				if errors.Is(err, lockfile.ErrLocked) {
					return fmt.Errorf("ivy is already running on %s", cfg.DataDir)
				}
				return err
			}
			defer func() { _ = lock.Release() }()
			dbs, err := store.Open(cmd.Context(), cfg.DataDir)
			if err != nil {
				return err
			}
			defer dbs.Close()

			// Accounts typed into the app come back after a restart. They join the
			// ivy.yaml ones here, before the embedding pipeline reads the account list,
			// which is why "smart features" on a new account starts at the next start.
			ivyYAMLAccounts := append([]config.Account(nil), cfg.Accounts...)
			stored, err := accountsvc.StoredAccounts(cmd.Context(), dbs, cfg)
			if err != nil {
				return err
			}
			cfg.Accounts = append(cfg.Accounts, stored...)

			// The embeddings gate is the only path to a paid provider; search uses it
			// for the query embedding and the embed worker for each message.
			appIDs := make([]string, len(stored))
			for i, a := range stored {
				appIDs[i] = a.ID
			}
			embedding := NewEmbedding(cfg, dbs, os.Getenv("OPENROUTER_API_KEY"), FromApp(appIDs...))

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			// One owned worker per account: reconcile, then IDLE on INBOX. The defer
			// order matters: workerStop cancels first, then the supervisor and the
			// group wait, so every goroutine finishes before RunE returns.
			workerCtx, workerStop := context.WithCancel(ctx)
			var workers sync.WaitGroup
			hub := events.New()
			supervisor := accountsvc.NewSupervisor(workerCtx, dbs, hub)
			defer workers.Wait()
			defer supervisor.Wait()
			defer workerStop()

			connector := accountsvc.NewConnector(accountsvc.ConnectorOptions{
				DBs: dbs, Supervisor: supervisor, SecretsDir: cfg.SecretsDir(), Provider: appProvider,
				Existing: func() []config.Account { return ivyYAMLAccounts },
			})
			api := gateway.New(dbs, version, webui.FS).WithSearch(embedding.Query).WithSpend(embedding.Caps).WithEvents(hub).
				WithAllowedHosts(cfg.HostAllowList()).WithBackupTargets(cfg.BackupTargets()).
				WithAccountConnector(connector).WithConfiguredSmart(smartOf(ivyYAMLAccounts))

			// The self-update path only makes sense in the container, where a host-side
			// watcher reads the signal file. In dev it would write a file nothing reads
			// and the UI would spin, so leave the endpoint reporting "unavailable".
			if inContainer() {
				api = api.WithUpdate(ctx, newUpdateClient(cfg))
			}

			// No Read/WriteTimeout: SSE streams and large bodies are long-lived. The
			// header and idle timeouts still shed slow-loris connections.
			srv := &http.Server{
				Addr:              cfg.Listen,
				Handler:           api.Handler(),
				ReadHeaderTimeout: 10 * time.Second,
				IdleTimeout:       2 * time.Minute,
			}
			// An open stream is never idle, so Shutdown would wait on it until its
			// timeout; closing the hub is what ends the streams.
			srv.RegisterOnShutdown(hub.Close)

			for _, a := range cfg.Accounts {
				supervisor.Start(accountsvc.SyncAccount(a))
			}

			// Embedding is a low-priority background queue: one job at a time, so it
			// never competes with sync for the potato's CPU or the provider's rate
			// limit (ARCHITECTURE.md 6).
			if embedding.Worker != nil {
				workers.Add(1)
				go func() {
					defer workers.Done()
					if err := embedding.Worker.Run(workerCtx); err != nil && !errors.Is(err, context.Canceled) {
						slog.WarnContext(workerCtx, "embed worker stopped", "error", err)
					}
				}()
			}

			// The daily state.db snapshot is its own owned goroutine, started with
			// the server and stopped by the same cancel path.
			backupManager := backup.New(dbs, cfg.BackupTargets())
			workers.Add(1)
			go func() {
				defer workers.Done()
				backupLoop(workerCtx, backupManager, cfg.Backup.At)
			}()

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

// newUpdateClient builds the self-update client for a config. It is a var so a
// test can point it at a fake registry and CI API.
var newUpdateClient = func(cfg *config.Config) *update.Client {
	return &update.Client{
		Repo:      update.DefaultRepo,
		SignalDir: cfg.UpdateSignalDir(),
		Token:     cfg.Update.Token,
	}
}

// updateCmd resolves the published image and asks the host watcher to deploy
// it. It never pulls or restarts anything in-process: that stays on the host,
// outside the container (ARCHITECTURE.md 9).
func updateCmd(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Resolve the latest image and ask the host watcher to deploy it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(*configPath)
			if err != nil {
				return err
			}
			target, err := newUpdateClient(cfg).Request(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "requested %s; the host update watcher will pull and restart Ivy\n", target)
			return nil
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
			dbs, err := store.Open(cmd.Context(), cfg.DataDir)
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
			reportHosts(out, cfg)
			reportBackups(out, cfg)
			return nil
		},
	}
}

// backupCmd writes one snapshot now, for a manual run or a test.
func backupCmd(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "backup",
		Short: "Write a state.db snapshot to every backup target",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(*configPath)
			if err != nil {
				return err
			}
			dbs, err := store.Open(cmd.Context(), cfg.DataDir)
			if err != nil {
				return err
			}
			defer dbs.Close()
			res, runErr := backup.New(dbs, cfg.BackupTargets()).Run(cmd.Context())
			for _, s := range res.Snapshots {
				fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", s.Path)
			}
			return runErr
		},
	}
}

// restoreCmd replaces state.db with a snapshot. It refuses while a server holds
// the data-directory lock, which is why it has its own one-line command.
func restoreCmd(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "restore <snapshot>",
		Short: "Restore a state.db snapshot in place (server stopped)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(*configPath)
			if err != nil {
				return err
			}
			if err := backup.Restore(cmd.Context(), cfg.DataDir, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "restored %s into %s\n", args[0], cfg.DataDir)
			return nil
		},
	}
}

// backupLoop runs one backup at the configured local time each day, and one at
// once on start when a slot was missed (the device was off at that time). It
// owns no other goroutine and ends promptly when the server's context is
// cancelled.
func backupLoop(ctx context.Context, manager *backup.Manager, at string) {
	due, err := manager.Due(time.Now())
	if err != nil {
		slog.WarnContext(ctx, "backup catch-up check", "error", err)
	}
	if due {
		runBackup(ctx, manager)
	}
	for {
		next, err := backup.NextDaily(time.Now(), at)
		if err != nil {
			slog.WarnContext(ctx, "backup schedule", "error", err)
			return
		}
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			runBackup(ctx, manager)
		}
	}
}

// runBackup takes one snapshot and reports the outcome. A failure is logged and
// retried at the next slot, never fatal to the server.
func runBackup(ctx context.Context, manager *backup.Manager) {
	res, err := manager.Run(ctx)
	if err != nil {
		slog.WarnContext(ctx, "daily backup failed", "error", err)
		return
	}
	slog.InfoContext(ctx, "daily backup written", "snapshots", len(res.Snapshots))
}

// reportBackups prints the backup targets and warns when every one shares the
// data directory's disk: that snapshot does not survive the device dying.
func reportBackups(out io.Writer, cfg *config.Config) {
	targets := cfg.BackupTargets()
	fmt.Fprintf(out, "backups:  %d target(s), daily at %s\n", len(targets), cfg.Backup.At)
	allSame := len(targets) > 0
	for _, target := range targets {
		fmt.Fprintf(out, "  %s\n", target)
		same, err := backup.SameDevice(cfg.DataDir, existingAncestor(target))
		if err != nil || !same {
			allSame = false
		}
	}
	if allSame {
		fmt.Fprintf(out, "warning: every backup target is on the same disk as the data directory;\n"+
			"a failed device would lose the database and its backups together\n")
	}
}

// existingAncestor walks up to the nearest path that exists, so a target that
// has not been created yet can still be compared with the data directory.
func existingAncestor(path string) string {
	for {
		if _, err := os.Stat(path); err == nil {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return path
		}
		path = parent
	}
}

// inContainer reports whether Ivy is running inside the deployment container,
// where a host-side update watcher reads the signal file. IVY_IN_CONTAINER is
// set by the Dockerfile; /.dockerenv covers a hand-run container.
func inContainer() bool {
	if os.Getenv("IVY_IN_CONTAINER") != "" {
		return true
	}
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

// appProvider is the server an account typed into the app talks to. It is a
// variable only so a test can point it at the fake mail world.
var appProvider = accountsvc.Purelymail

// reportHosts says which host names the API answers to (loopback always, then
// allowed_hosts and the listen host) and, when Ivy listens on a network address
// but no name is configured, how to let the phone's own name in. `ivy init`
// writes no config file, so this is guidance rather than a prompt.
func reportHosts(out io.Writer, cfg *config.Config) {
	allowed := cfg.HostAllowList()
	fmt.Fprintf(out, "allowed hosts: loopback%s\n", joinWithComma(allowed))
	if len(cfg.AllowedHosts) > 0 {
		return
	}
	if host, _, err := net.SplitHostPort(cfg.Listen); err == nil {
		if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			fmt.Fprintf(out, "note: Ivy listens on %s but allowed_hosts is empty, so a browser using any other\n"+
				"name (such as your Tailscale name) is refused. set allowed_hosts in ivy.yaml, e.g.\n"+
				"  allowed_hosts:\n    - ivy.your-tailnet.ts.net\n", cfg.Listen)
		}
	}
}

func joinWithComma(hosts []string) string {
	var b strings.Builder
	for _, h := range hosts {
		b.WriteString(", ")
		b.WriteString(h)
	}
	return b.String()
}
