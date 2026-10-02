package devstack

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Recipe is the launch shape recorded for a stack: enough to rebuild a
// byte-identical mailbox from the seed (DEV.md section 8). Root and Listen are
// deliberately excluded, since those are per-invocation.
type Recipe struct {
	Profile  string  `json:"profile"`
	Seed     int64   `json:"seed"`
	Mode     Mode    `json:"mode"`
	LLM      LLM     `json:"llm"`
	Accounts int     `json:"accounts"`
	Pair     bool    `json:"pair"`
	LLMCap   float64 `json:"llm_cap"`
	Hash     string  `json:"hash"`
}

// Options merges the recipe into a base, keeping the base's root and listen.
func (r Recipe) Options(base Options) Options {
	base.Profile = r.Profile
	base.Seed = r.Seed
	base.Mode = r.Mode
	base.LLM = r.LLM
	base.Accounts = r.Accounts
	base.Pair = r.Pair
	base.LLMCap = r.LLMCap
	return base
}

// RecipePath is the launch recipe Prepare writes.
func RecipePath(root string) string { return filepath.Join(DevDir(root), "state.json") }

// SnapshotDir holds named recipes under .dev/.
func SnapshotDir(root string) string { return filepath.Join(DevDir(root), "snapshots") }

var snapshotName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func snapshotPath(root, name string) (string, error) {
	if !snapshotName.MatchString(name) {
		return "", fmt.Errorf("devstack: unsafe snapshot name %q", name)
	}
	return filepath.Join(SnapshotDir(root), name+".json"), nil
}

func writeRecipe(path string, r Recipe) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("devstack: marshal recipe: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("devstack: write %s: %w", path, err)
	}
	return nil
}

func readRecipe(path string) (Recipe, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: built from the dev root and a validated snapshot name
	if err != nil {
		return Recipe{}, err
	}
	var r Recipe
	if err := json.Unmarshal(data, &r); err != nil {
		return Recipe{}, fmt.Errorf("devstack: parse %s: %w", path, err)
	}
	return r, nil
}

// LoadRecipe returns the current launch recipe, if one was recorded.
func LoadRecipe(root string) (Recipe, error) { return readRecipe(RecipePath(root)) }

// SaveSnapshot records the current launch recipe under name.
func SaveSnapshot(root, name string) error {
	r, err := readRecipe(RecipePath(root))
	if err != nil {
		return fmt.Errorf("devstack: no current stack to snapshot: %w", err)
	}
	path, err := snapshotPath(root, name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(SnapshotDir(root), 0o700); err != nil {
		return err
	}
	return writeRecipe(path, r)
}

// RestoreSnapshot makes name the current recipe, so the next Prepare rebuilds
// that exact mailbox.
func RestoreSnapshot(root, name string) (Recipe, error) {
	path, err := snapshotPath(root, name)
	if err != nil {
		return Recipe{}, err
	}
	r, err := readRecipe(path)
	if err != nil {
		return Recipe{}, fmt.Errorf("devstack: unknown snapshot %q: %w", name, err)
	}
	if err := writeRecipe(RecipePath(root), r); err != nil {
		return Recipe{}, err
	}
	return r, nil
}

// Snapshots lists saved snapshot names, sorted.
func Snapshots(root string) ([]string, error) {
	entries, err := os.ReadDir(SnapshotDir(root))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	sort.Strings(names)
	return names, nil
}
