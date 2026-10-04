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
	"github.com/AutumnsGrove/Ivy/internal/lockfile"
	"github.com/AutumnsGrove/Ivy/internal/webui"
	"github.com/AutumnsGrove/Ivy/store"
	ivysync "github.com/AutumnsGrove/Ivy/sync"
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

			// No Read/WriteTimeout: SSE streams and large bodies are long-lived. The
			// header and idle timeouts still shed slow-loris connections.
			hub := events.New()
			srv := &http.Server{
				Addr:              cfg.Listen,
				Handler:           gateway.New(dbs, version, webui.FS).WithEvents(hub).WithAllowedHosts(cfg.HostAllowList()).Handler(),
				ReadHeaderTimeout: 10 * time.Second,
				IdleTimeout:       2 * time.Minute,
			}
			// An open stream is never idle, so Shutdown would wait on it until its
			// timeout; closing the hub is what ends the streams.
			srv.RegisterOnShutdown(hub.Close)

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			// One owned worker per account: reconcile, then IDLE on INBOX. The defer
			// order matters: workerStop cancels first, then workers.Wait lets every
			// goroutine finish before RunE returns.
			workerCtx, workerStop := context.WithCancel(ctx)
			var workers sync.WaitGroup
			defer workers.Wait()
			defer workerStop()
			for _, a := range cfg.Accounts {
				acct := syncAccountFor(a)
				worker := ivysync.NewWorker(ivysync.NewFetcher(dbs), acct,
					ivysync.WithWorkerSyncFunc(func(res ivysync.Result, err error) {
						hub.Publish(events.Event{Type: events.SyncState, AccountID: acct.ID})
						if err == nil && res.Stored > 0 {
							hub.Publish(events.Event{Type: events.MessageChanged, AccountID: acct.ID})
						}
						// A sweep that hid enough mail to look like a mistake is a Mirror
						// health alert, not a reason to stop syncing (ARCHITECTURE.md 4).
						for _, md := range res.MassDisabled {
							hub.Publish(events.Event{
								Type: events.HealthAlert, AccountID: acct.ID,
								Folder: md.Folder, Code: "mass_disable",
							})
						}
					}))
				workers.Add(1)
				go func() {
					defer workers.Done()
					if err := worker.Run(workerCtx); err != nil && !errors.Is(err, context.Canceled) {
						slog.WarnContext(workerCtx, "sync worker stopped", "account", acct.ID, "error", err)
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

// backupLoop runs one backup at the configured local time each day. It owns no
// other goroutine and ends promptly when the server's context is cancelled.
func backupLoop(ctx context.Context, manager *backup.Manager, at string) {
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
			res, err := manager.Run(ctx)
			if err != nil {
				slog.WarnContext(ctx, "daily backup failed", "error", err)
				continue
			}
			slog.InfoContext(ctx, "daily backup written", "snapshots", len(res.Snapshots))
		}
	}
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

// syncAccountFor is the connection descriptor a sync worker uses for a
// configured account. Real accounts use implicit TLS; only the loopback dev
// fake sets Insecure (config.Account).
func syncAccountFor(a config.Account) ivysync.Account {
	return ivysync.Account{
		ID: a.ID, Address: a.Address,
		IMAPHost: a.IMAPHost, IMAPPort: a.IMAPPort,
		SMTPHost: a.SMTPHost, SMTPPort: a.SMTPPort,
		Username: a.Username, Password: a.Password,
		Insecure:           a.Insecure,
		TrustedAuthservIDs: a.TrustedAuthservIDs,
	}
}

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
