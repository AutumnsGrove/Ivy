// Command ivy-dev is the local development harness: it starts the fake mail
// world and Ivy, and lets a human or a test play "the other mail client"
// (STANDARDS.md section 3, DEV.md). It is a thin shell over internal/devstack
// and is never shipped in the production image.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/AutumnsGrove/Ivy/gateway"
	"github.com/AutumnsGrove/Ivy/internal/devstack"
	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	"github.com/AutumnsGrove/Ivy/store"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := newRootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	var root string
	cmd := &cobra.Command{
		Use:          "ivy-dev",
		Short:        "Run and drive the local Ivy development stack",
		SilenceUsage: true,
	}
	cmd.PersistentFlags().StringVar(&root, "root", ".", "repo root that holds .dev/")
	cmd.AddCommand(
		upCmd(&root),
		seedCmd(&root),
		resetCmd(&root),
		deliverCmd(&root),
		flagCmd(&root),
		moveCmd(&root),
		expungeCmd(&root),
		faultCmd(&root),
		stateCmd(&root),
		advanceClockCmd(&root),
	)
	return cmd
}

func upCmd(root *string) *cobra.Command {
	opts := devstack.DefaultOptions()
	opts.Listen = "127.0.0.1:8787"
	var noWeb bool
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Start mailworld, Ivy and the web dev server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts.Root = *root
			return runUp(cmd, opts, noWeb)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.Profile, "profile", opts.Profile, "mailworld profile: empty|minimal|demo|large")
	f.Int64Var(&opts.Seed, "seed", opts.Seed, "seed for deterministic data")
	f.StringVar((*string)(&opts.Mode), "mode", string(opts.Mode), "full|fast")
	f.StringVar((*string)(&opts.LLM), "llm", string(opts.LLM), "live|fake")
	f.IntVar(&opts.Accounts, "accounts", opts.Accounts, "number of accounts to configure")
	f.BoolVar(&opts.Pair, "pair", opts.Pair, "two accounts for send-between-accounts testing")
	f.BoolVar(&opts.Expose, "expose", opts.Expose, "bind the tailnet interface for phone testing")
	f.Float64Var(&opts.LLMCap, "llm-cap", opts.LLMCap, "monthly dev LLM spend cap in dollars")
	f.StringVar(&opts.Listen, "listen", opts.Listen, "Ivy listen address")
	f.BoolVar(&noWeb, "no-web", false, "do not start the Vite dev server")
	return cmd
}

// runUp starts a prepared stack and the real Ivy gateway in-process, then
// blocks until Ctrl-C. The real binary is exercised separately by the E2E
// smoke; keeping it in-process here means one command, no build step.
func runUp(cmd *cobra.Command, opts devstack.Options, noWeb bool) error {
	stack, err := devstack.Prepare(opts)
	if err != nil {
		return err
	}
	defer stack.Close()

	dbs, err := store.Open(stack.Config.DataDir)
	if err != nil {
		return err
	}
	defer dbs.Close()

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := &http.Server{Addr: stack.Config.Listen, Handler: gateway.New(dbs, version).Handler()}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "mailworld  imap %s  smtp %s\n", stack.World.IMAPAddr(), stack.World.SMTPAddr())
	fmt.Fprintf(out, "ivy %s  http://%s\n", version, stack.Config.Listen)
	fmt.Fprintf(out, "profile %s  seed %d  delivered %d  hash %s\n",
		opts.Profile, opts.Seed, stack.Seed.Delivered, shortHash(stack.Seed.Hash))
	for _, a := range stack.Config.Accounts {
		fmt.Fprintf(out, "  account %s (id %s)\n", a.Address, a.ID)
	}

	web := startWeb(ctx, cmd, opts, noWeb)
	fmt.Fprintln(out, "press Ctrl-C to stop")

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := srv.Shutdown(shutdownCtx)
		if web != nil {
			_ = web.Wait()
		}
		return err
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// startWeb runs Vite when pnpm is available. The frontend is still backed by
// mocks, so no API proxy is configured yet; Chunk 2 adds it.
func startWeb(ctx context.Context, cmd *cobra.Command, opts devstack.Options, noWeb bool) *exec.Cmd {
	if noWeb {
		return nil
	}
	pnpm, err := exec.LookPath("pnpm")
	if err != nil {
		fmt.Fprintln(cmd.OutOrStdout(), "web: pnpm not found, skipping the Vite dev server")
		return nil
	}
	web := exec.CommandContext(ctx, pnpm, "dev", "--host", "127.0.0.1", "--port", "5173")
	web.Dir = filepath.Join(opts.Root, "web")
	web.Stdout = cmd.OutOrStdout()
	web.Stderr = cmd.ErrOrStderr()
	if err := web.Start(); err != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "web: %v\n", err)
		return nil
	}
	fmt.Fprintln(cmd.OutOrStdout(), "web  http://127.0.0.1:5173")
	return web
}

func shortHash(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

func seedCmd(root *string) *cobra.Command {
	var profile string
	var seed int64
	cmd := &cobra.Command{
		Use:   "seed",
		Short: "(Re)generate the seed data only, without starting anything",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts := devstack.DefaultOptions()
			opts.Root = *root
			opts.Profile = profile
			opts.Seed = seed
			opts.Listen = "127.0.0.1:8787"
			stack, err := devstack.Prepare(opts)
			if err != nil {
				return err
			}
			defer stack.Close()
			fmt.Fprintf(cmd.OutOrStdout(), "profile %s  seed %d  delivered %d  hash %s\n",
				profile, seed, stack.Seed.Delivered, shortHash(stack.Seed.Hash))
			for _, a := range stack.Seed.Accounts {
				fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", a.Address)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&profile, "profile", "demo", "mailworld profile: empty|minimal|demo|large")
	cmd.Flags().Int64Var(&seed, "seed", 1, "seed for deterministic data")
	return cmd
}

func resetCmd(root *string) *cobra.Command {
	return &cobra.Command{
		Use:   "reset",
		Short: "Throw away the dev state under .dev/",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := devstack.Reset(*root); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "reset %s\n", devstack.DevDir(*root))
			return nil
		},
	}
}

func control(root string) (*devstack.Client, error) {
	return devstack.Dial(devstack.ControlPath(root))
}

func deliverCmd(root *string) *cobra.Command {
	var account, mailbox, from, subject, text, file string
	cmd := &cobra.Command{
		Use:   "deliver",
		Short: "Append a message to a mailbox as another client would",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			raw, err := deliverRaw(account, file, from, subject, text)
			if err != nil {
				return err
			}
			c, err := control(*root)
			if err != nil {
				return err
			}
			defer c.Close()
			uid, err := c.Deliver(account, mailbox, raw)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%d\n", uid)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&account, "account", "", "recipient dev address")
	f.StringVar(&mailbox, "mailbox", "INBOX", "destination mailbox")
	f.StringVar(&from, "from", "other@example.com", "sender address")
	f.StringVar(&subject, "subject", "dev message", "subject")
	f.StringVar(&text, "text", "A message delivered by ivy-dev.\n", "plain-text body")
	f.StringVar(&file, "file", "", "read a raw .eml instead of building one")
	_ = cmd.MarkFlagRequired("account")
	return cmd
}

func deliverRaw(account, file, from, subject, text string) ([]byte, error) {
	if file != "" {
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", file, err)
		}
		return raw, nil
	}
	return mailworld.Msg().From(from).To(account).Subject(subject).Text(text).Build(), nil
}

func flagCmd(root *string) *cobra.Command {
	var account, mailbox string
	var uid uint32
	cmd := &cobra.Command{
		Use:   "flag [flag...]",
		Short: "Add flags to a message",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := control(*root)
			if err != nil {
				return err
			}
			defer c.Close()
			return c.Flag(account, mailbox, uid, args...)
		},
	}
	f := cmd.Flags()
	f.StringVar(&account, "account", "", "dev address")
	f.StringVar(&mailbox, "mailbox", "INBOX", "mailbox")
	f.Uint32Var(&uid, "uid", 0, "message UID")
	_ = cmd.MarkFlagRequired("account")
	_ = cmd.MarkFlagRequired("uid")
	return cmd
}

func moveCmd(root *string) *cobra.Command {
	var account, mailbox, dest string
	var uid uint32
	cmd := &cobra.Command{
		Use:   "move",
		Short: "Move a message to another mailbox",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := control(*root)
			if err != nil {
				return err
			}
			defer c.Close()
			return c.Move(account, mailbox, uid, dest)
		},
	}
	f := cmd.Flags()
	f.StringVar(&account, "account", "", "dev address")
	f.StringVar(&mailbox, "mailbox", "INBOX", "source mailbox")
	f.Uint32Var(&uid, "uid", 0, "message UID")
	f.StringVar(&dest, "dest", "", "destination mailbox")
	_ = cmd.MarkFlagRequired("account")
	_ = cmd.MarkFlagRequired("uid")
	_ = cmd.MarkFlagRequired("dest")
	return cmd
}

func expungeCmd(root *string) *cobra.Command {
	var account, mailbox string
	var uid uint32
	cmd := &cobra.Command{
		Use:   "expunge",
		Short: "Remove a message",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := control(*root)
			if err != nil {
				return err
			}
			defer c.Close()
			return c.Expunge(account, mailbox, uid)
		},
	}
	f := cmd.Flags()
	f.StringVar(&account, "account", "", "dev address")
	f.StringVar(&mailbox, "mailbox", "INBOX", "mailbox")
	f.Uint32Var(&uid, "uid", 0, "message UID")
	_ = cmd.MarkFlagRequired("account")
	_ = cmd.MarkFlagRequired("uid")
	return cmd
}

func faultCmd(root *string) *cobra.Command {
	var spec devstack.FaultSpec
	cmd := &cobra.Command{
		Use:   "fault",
		Short: "Arm a mailworld fault: drop|auth-fail|smtp-reject|smtp-auth-fail",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := control(*root)
			if err != nil {
				return err
			}
			defer c.Close()
			return c.Fault(spec)
		},
	}
	f := cmd.Flags()
	f.StringVar(&spec.Kind, "kind", "", "drop|auth-fail|smtp-reject|smtp-auth-fail")
	f.IntVar(&spec.After, "after", 0, "drop after N commands")
	f.IntVar(&spec.Code, "code", 0, "SMTP reject code")
	f.StringVar(&spec.Message, "message", "", "SMTP reject message")
	_ = cmd.MarkFlagRequired("kind")
	return cmd
}

func stateCmd(root *string) *cobra.Command {
	var list bool
	cmd := &cobra.Command{
		Use:   "state [name]",
		Short: "Apply a named dev condition (DEV.md section 4)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if list {
				for _, s := range devstack.States() {
					fmt.Fprintf(cmd.OutOrStdout(), "%-20s %s\n", s.Name, s.Description)
				}
				return nil
			}
			if len(args) != 1 {
				return errors.New("state needs a name, or --list")
			}
			c, err := control(*root)
			if err != nil {
				return err
			}
			defer c.Close()
			return c.State(args[0])
		},
	}
	cmd.Flags().BoolVar(&list, "list", false, "list the available states")
	return cmd
}

func advanceClockCmd(root *string) *cobra.Command {
	return &cobra.Command{
		Use:   "advance-clock <duration>",
		Short: "Move the fake clock forward, e.g. 2h or 30m",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := time.ParseDuration(args[0])
			if err != nil {
				return err
			}
			c, err := control(*root)
			if err != nil {
				return err
			}
			defer c.Close()
			return c.AdvanceClock(d)
		},
	}
}
