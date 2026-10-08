// Command ivy-dev is the local development harness: it starts the fake mail
// world and Ivy, and lets a human or a test play "the other mail client"
// (STANDARDS.md section 3, DEV.md). It is a thin shell over internal/devstack
// and is never shipped in the production image.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	ivycmd "github.com/AutumnsGrove/Ivy/cmd"
	"github.com/AutumnsGrove/Ivy/config"
	"github.com/AutumnsGrove/Ivy/events"
	"github.com/AutumnsGrove/Ivy/gateway"
	"github.com/AutumnsGrove/Ivy/internal/devstack"
	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	"github.com/AutumnsGrove/Ivy/internal/webui"
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
		snapshotCmd(&root),
		deliverCmd(&root),
		flagCmd(&root),
		moveCmd(&root),
		expungeCmd(&root),
		faultCmd(&root),
		stateCmd(&root),
		scenarioCmd(&root),
		advanceClockCmd(&root),
	)
	return cmd
}

func upCmd(root *string) *cobra.Command {
	opts := devstack.DefaultOptions()
	opts.Listen = config.DefaultListen
	var noWeb bool
	var watch bool
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Start mailworld, Ivy and the web dev server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts.Root = *root
			applyRecipe(cmd, &opts)
			key, err := resolveLLM(cmd, &opts)
			if err != nil {
				return err
			}
			if watch {
				return runWatched(cmd, opts, noWeb, key)
			}
			return runUp(cmd, opts, noWeb, key)
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
	f.BoolVar(&watch, "watch", true, "rebuild and restart Ivy on Go changes")
	return cmd
}

// resolveLLM settles which provider this run really uses and returns the
// OpenRouter key the dev Ivy gets: the key for live (from the environment or the
// repo's .env), a throwaway for the fake. Live with no key falls back to the fake
// and says so, as DEV.md section 5 promises.
func resolveLLM(cmd *cobra.Command, opts *devstack.Options) (string, error) {
	dot, err := devstack.ReadEnv(filepath.Join(opts.Root, ".env"), "OPENROUTER_API_KEY")
	if err != nil {
		return "", err
	}
	mode, key, note := devstack.ResolveLLM(opts.LLM, os.Getenv("OPENROUTER_API_KEY"), dot["OPENROUTER_API_KEY"])
	if note != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), note)
	}
	opts.LLM = mode
	return key, nil
}

// printStack reports what up started; both the watch and no-watch paths use it.
func printStack(out io.Writer, opts devstack.Options, stack *devstack.Stack) {
	fmt.Fprintf(out, "mailworld  imap %s  smtp %s\n", stack.World.IMAPAddr(), stack.World.SMTPAddr())
	fmt.Fprintf(out, "ivy %s  http://%s\n", version, stack.Config.Listen)
	fmt.Fprintf(out, "profile %s  seed %d  delivered %d  hash %s\n",
		opts.Profile, opts.Seed, stack.Seed.Delivered, shortHash(stack.Seed.Hash))
	for _, a := range stack.Config.Accounts {
		fmt.Fprintf(out, "  account %s (id %s)\n", a.Address, a.ID)
	}
	if opts.Expose {
		fmt.Fprintln(out, "exposed on the tailnet: fake data, no auth; never the public internet")
		if err := devstack.WriteQR(out, "http://"+stack.Config.Listen); err != nil {
			fmt.Fprintf(out, "qr: %v\n", err)
		}
	}
}

// runWatched starts a prepared stack and supervises the real Ivy binary,
// rebuilding and restarting it on Go file changes (DEV.md section 1).
func runWatched(cmd *cobra.Command, opts devstack.Options, noWeb bool, llmKey string) error {
	moduleRoot, err := devstack.ModuleRoot(opts.Root)
	if err != nil {
		return err
	}
	stack, err := devstack.Prepare(opts)
	if err != nil {
		return err
	}
	defer stack.Close()

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	out := cmd.OutOrStdout()
	printStack(out, opts, stack)
	// The child Ivy only opens the databases, so they are filled before it starts.
	if err := populate(ctx, out, stack, opts); err != nil {
		return err
	}

	bin := filepath.Join(devstack.DevDir(opts.Root), "ivy")
	// Later entries win, so this replaces any OPENROUTER_API_KEY already in the
	// environment: the fake gets a throwaway, never the real key.
	env := append(os.Environ(), stack.PasswordEnv()...)
	env = append(env, "OPENROUTER_API_KEY="+llmKey)
	web := startWeb(ctx, cmd, opts, noWeb)
	fmt.Fprintln(out, "press Ctrl-C to stop")

	sup := &devstack.Supervisor{
		Root: moduleRoot,
		Build: func(buildCtx context.Context) error {
			// ivy-dev is the operator's own dev harness: it runs the Go toolchain and the
			// binary it just built from paths it derived itself.
			build := exec.CommandContext(buildCtx, "go", "build", "-o", bin, ".") //nolint:gosec // G204: fixed toolchain command, harness-derived paths
			build.Dir = moduleRoot
			build.Stdout = out
			build.Stderr = cmd.ErrOrStderr()
			return build.Run()
		},
		Command: func() *exec.Cmd {
			child := exec.CommandContext(ctx, bin, "run", "--config", devstack.ConfigPath(opts.Root)) //nolint:gosec // G204: the binary this harness just built
			child.Env = env
			return child
		},
		Log: out,
	}
	if err := sup.Run(ctx); err != nil {
		return err
	}
	if web != nil {
		_ = web.Wait()
	}
	return nil
}

// runUp starts a prepared stack and the real Ivy gateway in-process, then
// blocks until Ctrl-C. The --watch=false path, used by fast tests.
func runUp(cmd *cobra.Command, opts devstack.Options, noWeb bool, llmKey string) error {
	stack, err := devstack.Prepare(opts)
	if err != nil {
		return err
	}
	defer stack.Close()

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := populate(ctx, cmd.OutOrStdout(), stack, opts); err != nil {
		return err
	}
	dbs, err := store.Open(ctx, stack.Config.DataDir)
	if err != nil {
		return err
	}
	defer dbs.Close()

	// The same search and embedding stack `ivy run` builds, so dev exercises what ships.
	embedding := ivycmd.NewEmbedding(stack.Config, dbs, llmKey)
	var workers sync.WaitGroup
	workerCtx, stopWorkers := context.WithCancel(ctx)
	defer func() { stopWorkers(); workers.Wait() }()
	if embedding.Worker != nil {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := embedding.Worker.Run(workerCtx); err != nil && !errors.Is(err, context.Canceled) {
				fmt.Fprintf(cmd.ErrOrStderr(), "embed worker stopped: %v\n", err)
			}
		}()
	}

	hub := events.New()
	srv := &http.Server{
		Addr:              stack.Config.Listen,
		Handler:           gateway.New(dbs, version, webui.FS).WithSearch(embedding.Query).WithSpend(embedding.Caps).WithEmbedBacklog(embedding.Backlog()).WithEvents(hub).WithAllowedHosts(stack.Config.HostAllowList()).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	srv.RegisterOnShutdown(hub.Close) // open streams are never idle; see cmd.go
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	out := cmd.OutOrStdout()
	printStack(out, opts, stack)

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

// populate fills the dev databases and says how, so a slow or failed first
// sync is visible instead of looking like a hung start.
func populate(ctx context.Context, out io.Writer, stack *devstack.Stack, opts devstack.Options) error {
	start := time.Now()
	sum, err := devstack.Populate(ctx, stack, opts)
	if err != nil {
		return err
	}
	if sum.Discarded != "" {
		fmt.Fprintln(out, sum.Discarded+"; building instead")
	}
	how := string(opts.Mode) + " mode"
	if sum.Restored {
		how = "restored from cache"
	}
	fmt.Fprintf(out, "populated %d messages across %d accounts (%s, %s)\n",
		sum.Messages, sum.Accounts, how, time.Since(start).Round(time.Millisecond))
	return nil
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
	// --strictPort: without it Vite silently moves to 5174 when 5173 is taken,
	// and the URL printed below (and the QR code) would be wrong.
	web := exec.CommandContext(ctx, pnpm, "dev", "--host", "127.0.0.1", "--port", "5173", "--strictPort") //nolint:gosec // G204: pnpm resolved by LookPath, fixed arguments
	web.Dir = filepath.Join(opts.Root, "web")
	// pnpm runs Vite as a child, and CommandContext alone kills only pnpm. Own
	// process group, signalled as a whole, so no Vite is left holding the port.
	web.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	web.Cancel = func() error { return syscall.Kill(-web.Process.Pid, syscall.SIGTERM) }
	web.WaitDelay = 3 * time.Second
	web.Env = append(os.Environ(), "IVY_API_TARGET=http://"+opts.Listen)
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
			opts.Listen = config.DefaultListen
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

// applyRecipe folds a recorded snapshot into the flags the user did not set,
// so `snapshot restore` followed by `up` reproduces that mailbox.
func applyRecipe(cmd *cobra.Command, opts *devstack.Options) {
	r, err := devstack.LoadRecipe(opts.Root)
	if err != nil {
		return
	}
	f := cmd.Flags()
	if !f.Changed("profile") {
		opts.Profile = r.Profile
	}
	if !f.Changed("seed") {
		opts.Seed = r.Seed
	}
	if !f.Changed("mode") {
		opts.Mode = r.Mode
	}
	if !f.Changed("llm") {
		opts.LLM = r.LLM
	}
	if !f.Changed("accounts") {
		opts.Accounts = r.Accounts
	}
	if !f.Changed("pair") {
		opts.Pair = r.Pair
	}
	if !f.Changed("llm-cap") {
		opts.LLMCap = r.LLMCap
	}
}

func resetCmd(root *string) *cobra.Command {
	return &cobra.Command{
		Use:   "reset",
		Short: "Throw away the built dev state (snapshots and the recipe stay)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := devstack.Reset(*root); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "reset built state under %s\n", devstack.DevDir(*root))
			return nil
		},
	}
}

func snapshotCmd(root *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Save, restore or list the stack's launch recipe",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "save <name>",
		Short: "Record the current launch recipe",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := devstack.SaveSnapshot(*root, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "saved %s\n", args[0])
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "restore <name>",
		Short: "Make a snapshot the current recipe",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := devstack.RestoreSnapshot(*root, args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "restored %s (profile %s seed %d hash %s)\n",
				args[0], r.Profile, r.Seed, shortHash(r.Hash))
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List saved snapshots",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			names, err := devstack.Snapshots(*root)
			if err != nil {
				return err
			}
			for _, name := range names {
				fmt.Fprintln(cmd.OutOrStdout(), name)
			}
			return nil
		},
	})
	return cmd
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
		raw, err := os.ReadFile(file) //nolint:gosec // G304: the operator names the .eml to deliver
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
		RunE: func(_ *cobra.Command, args []string) error {
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
		RunE: func(_ *cobra.Command, _ []string) error {
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
		RunE: func(_ *cobra.Command, _ []string) error {
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
		Short: "Arm a mailworld fault: drop|unreachable|auth-fail|smtp-reject|smtp-auth-fail",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := control(*root)
			if err != nil {
				return err
			}
			defer c.Close()
			return c.Fault(spec)
		},
	}
	f := cmd.Flags()
	f.StringVar(&spec.Kind, "kind", "", "drop|unreachable|auth-fail|smtp-reject|smtp-auth-fail")
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

func scenarioCmd(root *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scenario",
		Short: "Run scripted dev scenarios",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "run <file.yaml>",
		Short: "Replay a scenario against the running stack",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := devstack.ParseScenario(args[0])
			if err != nil {
				return err
			}
			c, err := control(*root)
			if err != nil {
				return err
			}
			defer c.Close()
			if err := devstack.RunScenario(c, s, filepath.Dir(args[0])); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "ran %d steps\n", len(s.Steps))
			return nil
		},
	})
	return cmd
}

func advanceClockCmd(root *string) *cobra.Command {
	return &cobra.Command{
		Use:   "advance-clock <duration>",
		Short: "Move the fake clock forward, e.g. 2h or 30m",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
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
