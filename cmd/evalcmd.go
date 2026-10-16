package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/AutumnsGrove/Ivy/config"
	"github.com/AutumnsGrove/Ivy/eval"
	"github.com/AutumnsGrove/Ivy/llm"
	"github.com/AutumnsGrove/Ivy/store"
)

// needsMeSheet is the labelling sheet's name inside the data directory's eval folder.
const needsMeSheet = "needs-me-labels.csv"

// evalReportMax bounds how many messages one report reads. The sheet is for a person
// to label by hand, so it is far below this in practice.
const evalReportMax = 5000

// previewRunes is how much of a body the sheet shows beside the subject.
const previewRunes = 160

func evalCmd(configPath *string) *cobra.Command {
	root := &cobra.Command{
		Use:   "eval",
		Short: "Measure how well Ivy's smart features agree with your own judgement",
	}
	var account string
	var limit int
	sheet := &cobra.Command{
		Use:   "sheet",
		Short: "Write the needs-me labelling sheet (a CSV of your classified mail, no verdicts)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, dbs, err := openForEval(cmd.Context(), *configPath)
			if err != nil {
				return err
			}
			defer dbs.Close()
			return runEvalSheet(cmd.Context(), dbs, evalDir(cfg), account, limit, cmd.OutOrStdout())
		},
	}
	sheet.Flags().StringVar(&account, "account", "", "the account id, when there is more than one")
	sheet.Flags().IntVar(&limit, "limit", 200, "the most messages to list, newest first")

	report := &cobra.Command{
		Use:   "report",
		Short: "Score the cascade against your labels (costs nothing, calls no provider)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, dbs, err := openForEval(cmd.Context(), *configPath)
			if err != nil {
				return err
			}
			defer dbs.Close()
			return runEvalReport(cmd.Context(), dbs, evalDir(cfg), account, cmd.OutOrStdout())
		},
	}
	report.Flags().StringVar(&account, "account", "", "the account id, when there is more than one")
	root.AddCommand(sheet, report)
	return root
}

// evalDir keeps the labels beside the data they describe and out of the repository:
// they name real mail.
func evalDir(cfg *config.Config) string { return filepath.Join(cfg.DataDir, "eval") }

func openForEval(ctx context.Context, configPath string) (*config.Config, *store.DBs, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, nil, err
	}
	dbs, err := store.Open(ctx, cfg.DataDir)
	if err != nil {
		return nil, nil, err
	}
	return cfg, dbs, nil
}

// evalAccount resolves which account to read: the named one, or the only one.
func evalAccount(ctx context.Context, dbs *store.DBs, id string) (string, error) {
	accounts, err := dbs.ListAccounts(ctx)
	if err != nil {
		return "", err
	}
	ids := make([]string, 0, len(accounts))
	for _, a := range accounts {
		if id != "" && a.ID == id {
			return id, nil
		}
		ids = append(ids, a.ID)
	}
	switch {
	case id != "":
		return "", fmt.Errorf("no account %q (have %s)", id, strings.Join(ids, ", "))
	case len(ids) == 1:
		return ids[0], nil
	case len(ids) == 0:
		return "", errors.New("there are no accounts to evaluate")
	}
	return "", fmt.Errorf("name the account with --account (have %s)", strings.Join(ids, ", "))
}

// runEvalSheet lists the classified mail for labelling. Labels already typed are kept;
// it refuses rather than drop one.
func runEvalSheet(ctx context.Context, dbs *store.DBs, dir, accountID string, limit int, out io.Writer) error {
	account, err := evalAccount(ctx, dbs, accountID)
	if err != nil {
		return err
	}
	msgs, err := dbs.ClassifiedMessages(ctx, account, limit)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, needsMeSheet)
	var labels eval.Labels
	switch raw, err := os.ReadFile(path); { //nolint:gosec // G304: a fixed name inside the operator's data directory
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return fmt.Errorf("read %s: %w", path, err)
	default:
		// A sheet we cannot read is not overwritten: it may hold an hour of her labelling.
		if labels, err = eval.ReadSheet(bytes.NewReader(raw)); err != nil {
			return fmt.Errorf("%s: %w; fix or move it, nothing was changed", path, err)
		}
	}

	items := make([]eval.Item, 0, len(msgs))
	listed := make(map[string]bool, len(msgs))
	for _, m := range msgs {
		items = append(items, eval.Item{
			Key: m.ContentKey, Date: m.Date.Format("2006-01-02"), From: sender(m.From),
			Subject: m.Subject, Preview: preview(m.BodyText),
		})
		listed[eval.ID(m.ContentKey)] = true
	}
	dropped := 0
	for id, cell := range labels {
		if strings.TrimSpace(cell) != "" && !listed[id] {
			dropped++
		}
	}
	if dropped > 0 {
		return fmt.Errorf("%d of your labels belong to messages this listing no longer reaches; "+
			"raise --limit so none are lost, nothing was changed", dropped)
	}

	var buf bytes.Buffer
	if err := eval.WriteSheet(&buf, items, labels); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// Written beside and renamed over, so a crash cannot leave half a sheet.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	fmt.Fprintf(out, "Wrote %d messages to %s\n\n", len(items), path)
	fmt.Fprintf(out, "Open it in a spreadsheet and fill the last column (needs_me) with y or n:\n")
	fmt.Fprintf(out, "  y  this needed something from you: a reply, a decision, a form, a payment, a deadline\n")
	fmt.Fprintf(out, "  n  it did not, however interesting it was\n")
	fmt.Fprintf(out, "Leave a cell blank to skip that message. Then run `ivy eval report`.\n")
	return nil
}

// runEvalReport scores the cascade against the labels and prints the report.
func runEvalReport(ctx context.Context, dbs *store.DBs, dir, accountID string, out io.Writer) error {
	account, err := evalAccount(ctx, dbs, accountID)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, needsMeSheet)
	raw, err := os.ReadFile(path) //nolint:gosec // G304: a fixed name inside the operator's data directory
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("no labels yet: make the sheet with `ivy eval sheet`, label it, then run this again")
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	typed, err := eval.ReadSheet(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	labels, err := typed.Parse()
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}

	reg, err := newJevRegistry(ctx, dbs, nil)
	if err != nil {
		return err
	}
	q, ok := reg.Get("needs_me")
	if !ok {
		return errors.New("the needs_me question is not in the registry")
	}
	model, ok := llm.LookupModel(llm.JevModelID)
	if !ok {
		return errors.New("the Jev model is not in the catalog")
	}
	msgs, err := dbs.ClassifiedMessages(ctx, account, evalReportMax)
	if err != nil {
		return err
	}
	var rows []eval.Row
	noAnswer := 0
	for _, m := range msgs {
		answer, found, err := stageOneAnswer(ctx, dbs, account, m.ContentKey, q.Hash(), model.Slug)
		if err != nil {
			return err
		}
		if !found {
			// Withheld by a safety check, or not asked yet: there is nothing to score.
			if _, labelled := labels[eval.ID(m.ContentKey)]; labelled {
				noAnswer++
			}
			continue
		}
		row := eval.Row{Key: m.ContentKey, Subject: m.Subject, From: sender(m.From), Stage1: answer}
		if v, has, err := dbs.GetNeedsMe(ctx, account, m.ContentKey); err != nil {
			return err
		} else if has {
			row.Verdict, row.Reason = v.Verdict, v.Reason
		}
		rows = append(rows, row)
	}

	report := eval.Score(rows, labels, eval.Config{Quiet: q.QuietOption, Threshold: q.Threshold})
	fmt.Fprint(out, report.Markdown())
	if noAnswer > 0 {
		verb, noun := "has", "message"
		if noAnswer != 1 {
			verb, noun = "have", "messages"
		}
		fmt.Fprintf(out, "\n%d labelled %s %s no stage-one answer (withheld by a safety check, or not asked yet) and %s left out of the numbers.\n",
			noAnswer, noun, verb, map[bool]string{true: "is", false: "are"}[noAnswer == 1])
	}
	return nil
}

// stageOneAnswer is the cached needs_me answer for the question's current wording and
// the current model: an answer to an edited question does not count.
func stageOneAnswer(ctx context.Context, dbs *store.DBs, account, key, hash, model string) (eval.Stage1, bool, error) {
	ds, err := dbs.DecisionsFor(ctx, account, key)
	if err != nil {
		return eval.Stage1{}, false, err
	}
	for _, d := range ds {
		if d.QuestionID == "needs_me" && d.InstructionHash == hash && d.Model == model {
			return eval.Stage1{Choice: d.Choice, Probs: d.Probabilities, Confidence: d.Confidence}, true, nil
		}
	}
	return eval.Stage1{}, false, nil
}

func sender(a store.Address) string {
	switch {
	case a.Name != "" && a.Address != "":
		return a.Name + " <" + a.Address + ">"
	case a.Address != "":
		return a.Address
	}
	return a.Name
}

// preview is the start of the body on one line, cut on a rune boundary.
func preview(body string) string {
	flat := strings.Join(strings.Fields(body), " ")
	if utf8.RuneCountInString(flat) <= previewRunes {
		return flat
	}
	return string([]rune(flat)[:previewRunes]) + "…"
}
