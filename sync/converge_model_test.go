package sync_test

// The model-based convergence harness (TESTING.md 2, CHUNK3-BRIEF.md 4, gate C1).
//
// One invariant: after sync quiesces, the mirror equals the server. A script of
// operations (the other mail client moving, flagging, expunging, renaming, and
// the provider bumping UIDVALIDITY) is applied to the fake server, with sync
// points in between, and after every sync point the mirror is checked.
//
// Three parts, kept apart on purpose:
//
//   - The MODEL is a small pure-Go copy of the server. It exists only so the
//     generator can pick operations that are valid right now and so a script can
//     be replayed after the shrinker deletes steps. It is never what the mirror is
//     compared to.
//   - The ORACLE reads the server itself, through mailworld's own IMAP read
//     (Account.Messages, a separate connection with no Ivy code in between), and
//     compares the mirror to that. If the model and the server disagree the
//     harness is wrong, and it says "harness", not "sync".
//   - The SYSTEM UNDER TEST is one function (config.sut). 3a replaces its body
//     with the runner's single-pass entry point; nothing else here changes.
//
// Operations are index picks resolved when they run ("move the 3rd message to the
// 2nd folder"), not UIDs, so a script stays valid after earlier steps are shrunk
// away.

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	"github.com/AutumnsGrove/Ivy/store"
	ivysync "github.com/AutumnsGrove/Ivy/sync"
)

const (
	harnessAddress  = "me@sync.test"
	harnessPassword = "secret"
	harnessAccount  = "acct-1"
	syncDeadline    = 30 * time.Second
	// Small message tiers so a few KiB of body takes the spool path and a script
	// of a few dozen messages crosses every size tier (STANDARDS.md 4a).
	harnessInline = 2 << 10
	harnessMax    = 64 << 10
	harnessBatch  = 7
)

// ---------------------------------------------------------------- operations

type opKind int

const (
	opAppend opKind = iota
	opCopy
	opFlag
	opUnflag
	opMove
	opExpunge
	opCreateFolder
	opRenameFolder
	opDeleteFolder
	opBumpValidity
	opSync
	numOpKinds
)

var opNames = [numOpKinds]string{
	"append", "copy", "flag", "unflag", "move", "expunge",
	"create-folder", "rename-folder", "delete-folder", "bump-uidvalidity", "sync",
}

func (k opKind) String() string { return opNames[k] }

// op is one step. n numbers a new message (opAppend); a and b are choices
// resolved modulo what exists when the op runs.
type op struct {
	kind opKind
	n    int
	a, b int
}

type weight struct {
	kind opKind
	w    int
}

// defaultMix is every operation the brief names (move, flag, expunge, append,
// folder rename and delete, UIDVALIDITY bump) plus copy, which makes duplicate
// Message-IDs across folders, and unflag, which a flag-only mix would never test.
var defaultMix = []weight{
	{opAppend, 22},
	{opSync, 18},
	{opFlag, 14},
	{opMove, 10},
	{opExpunge, 7},
	{opUnflag, 6},
	{opCopy, 5},
	{opCreateFolder, 4},
	{opRenameFolder, 3},
	{opDeleteFolder, 3},
	{opBumpValidity, 3},
}

// appendOnlyMix is the one history the one-shot fetch handles correctly, used to
// show the oracle accepts a correct outcome and is not simply strict about everything.
var appendOnlyMix = []weight{{opAppend, 70}, {opSync, 25}, {opCreateFolder, 5}}

// generate builds a script from a seed. The same seed and mix always give the
// same script, so the seed printed on a failure reproduces it.
func generate(seed uint64, mix []weight) []op {
	rng := rand.New(rand.NewPCG(seed, 0x9e3779b97f4a7c15))
	total := 0
	for _, w := range mix {
		total += w.w
	}
	script := make([]op, 12+rng.IntN(29))
	next := 0
	for i := range script {
		pick := rng.IntN(total)
		kind := mix[0].kind
		for _, w := range mix {
			if pick < w.w {
				kind = w.kind
				break
			}
			pick -= w.w
		}
		o := op{kind: kind, a: rng.IntN(1 << 16), b: rng.IntN(1 << 16)}
		if kind == opAppend {
			o.n = next
			next++
		}
		script[i] = o
	}
	return script
}

// ---------------------------------------------------------------- the model

var folderPool = []string{"Archive", "Sent", "Trash", "Junk", "Drafts", "Work", "Home"}

var flagPool = []imap.Flag{
	imap.FlagSeen, imap.FlagFlagged, imap.FlagAnswered, imap.FlagDraft, imap.FlagDeleted,
	"$ivy-work", "$ivy-home",
}

type mmsg struct {
	id    int
	flags map[string]struct{} // lower-case: IMAP flags are case-insensitive
}

type mfolder struct {
	uidNext uint32
	msgs    map[uint32]*mmsg
}

type placement struct {
	folder string
	uid    uint32
	msg    *mmsg
}

type model struct{ folders map[string]*mfolder }

func newModel() *model {
	return &model{folders: map[string]*mfolder{"INBOX": {uidNext: 1, msgs: map[uint32]*mmsg{}}}}
}

func (m *model) names() []string {
	names := make([]string, 0, len(m.folders))
	for n := range m.folders {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

func (m *model) placements() []placement {
	var out []placement
	for _, name := range m.names() {
		f := m.folders[name]
		uids := make([]uint32, 0, len(f.msgs))
		for u := range f.msgs {
			uids = append(uids, u)
		}
		slices.Sort(uids)
		for _, u := range uids {
			out = append(out, placement{name, u, f.msgs[u]})
		}
	}
	return out
}

func (f *mfolder) add(id int) (uint32, *mmsg) {
	uid := f.uidNext
	f.uidNext++
	msg := &mmsg{id: id, flags: map[string]struct{}{}}
	f.msgs[uid] = msg
	return uid, msg
}

func messageID(n int) string { return fmt.Sprintf("<m%d@sync.test>", n) }

// rawFor is the bytes of logical message n. Every fourth one is big enough to
// be spooled rather than kept in the row, so both storage tiers are exercised.
func rawFor(n int) []byte {
	body := fmt.Sprintf("body of message %d", n)
	if n%4 == 0 {
		body = strings.Repeat(body+". ", 200)
	}
	return mailworld.Msg().
		From("Sender <sender@example.com>").To(harnessAddress).
		Subject(fmt.Sprintf("m%d", n)).MessageID(messageID(n)).
		Date(time.Date(2026, 3, 1, 9, 0, n%60, 0, time.UTC)).
		Text(body).Build()
}

func lowerFlags(flags []imap.Flag) []string {
	out := make([]string, 0, len(flags))
	for _, f := range flags {
		out = append(out, strings.ToLower(string(f)))
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func flagKey(flags []string) string {
	out := slices.Clone(flags)
	for i := range out {
		out[i] = strings.ToLower(out[i])
	}
	slices.Sort(out)
	return strings.Join(slices.Compact(out), ",")
}

func (m *mmsg) flagList() []string {
	out := make([]string, 0, len(m.flags))
	for f := range m.flags {
		out = append(out, f)
	}
	slices.Sort(out)
	return out
}

// ------------------------------------------------------------- configuration

type config struct {
	condStore bool
	// sut brings the mirror up to date with a quiet server. nil means no sync at
	// all, so only the model-against-server check runs.
	sut syncFunc
	// checkModelEachOp compares the model with the server after every operation,
	// not just at sync points: slow, used to test the model itself.
	checkModelEachOp bool
}

type syncFunc func(ctx context.Context, dbs *store.DBs, f *ivysync.Fetcher, acct ivysync.Account) error

// oneShotFetch is today's system under test: the chunk 2b read fetch, which
// only adds messages it has not seen. It is why the convergence test is red.
// When the 3a runner exists this is the one line to change.
func oneShotFetch(ctx context.Context, _ *store.DBs, f *ivysync.Fetcher, acct ivysync.Account) error {
	_, err := f.Fetch(ctx, acct)
	return err
}

func defaultConfig(condStore bool) config { return config{condStore: condStore, sut: oneShotFetch} }

// -------------------------------------------------------------------- replay

type failure struct{ kind, detail string }

type outcome struct {
	fail  *failure
	trace []string
	ran   map[opKind]int // operations that actually changed the server
}

// replay runs a script against a fresh world and mirror and checks the mirror at
// every sync point (and once more at the end). It never calls t.Fatal, so the
// shrinker can run it hundreds of times.
func (c config) replay(script []op) outcome {
	out := outcome{ran: map[opKind]int{}}
	fail := func(kind, format string, args ...any) outcome {
		out.fail = &failure{kind, fmt.Sprintf(format, args...)}
		return out
	}

	var worldOpts []mailworld.Option
	if !c.condStore {
		worldOpts = append(worldOpts, mailworld.WithoutCondStore())
	}
	w, err := mailworld.New(worldOpts...)
	if err != nil {
		return fail("harness", "new world: %v", err)
	}
	defer func() { _ = w.Close() }()
	acc := w.Account(harnessAddress, harnessPassword)

	dir, err := os.MkdirTemp("", "ivy-converge-")
	if err != nil {
		return fail("harness", "temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	dbs, err := store.Open(context.Background(), dir)
	if err != nil {
		return fail("harness", "open store: %v", err)
	}
	defer func() { _ = dbs.Close() }()

	host, port, err := splitAddr(w.IMAPAddr())
	if err != nil {
		return fail("harness", "imap address: %v", err)
	}
	h := &run{
		c: c, acc: acc, dbs: dbs, mdl: newModel(), raw: map[int][]byte{},
		fetcher: ivysync.NewFetcher(dbs, ivysync.WithMessageLimits(harnessInline, harnessMax), ivysync.WithBatchSize(harnessBatch)),
		acct: ivysync.Account{
			ID: harnessAccount, Address: harnessAddress, IMAPHost: host, IMAPPort: port,
			Username: harnessAddress, Password: harnessPassword, Insecure: true,
		},
		seenRows: map[string]bool{}, everMirrored: map[string]bool{}, prev: map[string]rowState{},
	}

	steps := append(slices.Clone(script), op{kind: opSync}) // the end of every script is checked
	for i, o := range steps {
		desc, changed, err := h.apply(o)
		out.trace = append(out.trace, fmt.Sprintf("%2d. %s", i+1, desc))
		if err != nil {
			return fail("harness", "step %d (%s): %v", i+1, desc, err)
		}
		if changed {
			out.ran[o.kind]++
		}
		if c.checkModelEachOp || o.kind == opSync {
			if diff, err := h.modelVsServer(); err != nil {
				return fail("harness", "step %d: reading the server: %v", i+1, err)
			} else if diff != "" {
				return fail("harness", "after step %d (%s) the model and the server disagree, so the harness is wrong, not sync: %s", i+1, desc, diff)
			}
		}
		if o.kind == opSync && c.sut != nil {
			if f := h.checkSync(); f != nil {
				out.fail = f
				out.trace[len(out.trace)-1] += "   <-- fails here"
				return out
			}
		}
	}
	return out
}

func splitAddr(addr string) (string, int, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(portStr)
	return host, port, err
}

// run is the state of one replay.
type run struct {
	c       config
	acc     *mailworld.Account
	dbs     *store.DBs
	fetcher *ivysync.Fetcher
	acct    ivysync.Account
	mdl     *model
	raw     map[int][]byte // the bytes delivered for each logical message

	seenRows     map[string]bool     // every mirror row id ever observed
	everMirrored map[string]bool     // every Message-ID ever observed in a row
	prev         map[string]rowState // the last observation of each row
}

// apply performs one operation on the server and the model. changed is false for
// a step that had nothing to act on (an expunge with no messages).
func (h *run) apply(o op) (desc string, changed bool, err error) {
	m := h.mdl
	pls := m.placements()
	pickPlacement := func() (placement, bool) {
		if len(pls) == 0 {
			return placement{}, false
		}
		return pls[o.a%len(pls)], true
	}
	// otherFolder picks a folder that is not except.
	otherFolder := func(except string) (string, bool) {
		var names []string
		for _, n := range m.names() {
			if n != except {
				names = append(names, n)
			}
		}
		if len(names) == 0 {
			return "", false
		}
		return names[o.b%len(names)], true
	}
	// freeFolderName picks a pool name no folder currently has.
	freeFolderName := func() (string, bool) {
		var free []string
		for _, n := range folderPool {
			if m.folders[n] == nil {
				free = append(free, n)
			}
		}
		if len(free) == 0 {
			return "", false
		}
		return free[o.b%len(free)], true
	}
	// namedFolder picks any folder but INBOX (a server cannot rename or delete it).
	namedFolder := func() (string, bool) {
		var names []string
		for _, n := range m.names() {
			if n != "INBOX" {
				names = append(names, n)
			}
		}
		if len(names) == 0 {
			return "", false
		}
		return names[o.a%len(names)], true
	}
	skip := func(why string) (string, bool, error) {
		return fmt.Sprintf("%s (skipped: %s)", o.kind, why), false, nil
	}
	label := func(p placement) string { return fmt.Sprintf("m%d %s/uid %d", p.msg.id, p.folder, p.uid) }

	switch o.kind {
	case opSync:
		return "sync", false, nil

	case opAppend:
		names := m.names()
		dest := names[o.a%len(names)]
		raw := rawFor(o.n)
		h.raw[o.n] = raw
		uid, err := h.acc.Append(dest, raw)
		if err != nil {
			return fmt.Sprintf("append m%d -> %s", o.n, dest), false, err
		}
		want, _ := m.folders[dest].add(o.n)
		if uid != want {
			return fmt.Sprintf("append m%d -> %s", o.n, dest), false, fmt.Errorf("server gave uid %d, model expected %d", uid, want)
		}
		return fmt.Sprintf("append m%d -> %s (uid %d, %d bytes)", o.n, dest, uid, len(raw)), true, nil

	case opCopy:
		p, ok := pickPlacement()
		if !ok {
			return skip("no messages")
		}
		dest, ok := otherFolder(p.folder)
		if !ok {
			return skip("no other folder")
		}
		uid, err := h.acc.Append(dest, h.raw[p.msg.id])
		if err != nil {
			return "copy " + label(p), false, err
		}
		want, _ := m.folders[dest].add(p.msg.id)
		if uid != want {
			return "copy " + label(p), false, fmt.Errorf("server gave uid %d, model expected %d", uid, want)
		}
		return fmt.Sprintf("copy %s -> %s (uid %d), the same Message-ID in two folders", label(p), dest, uid), true, nil

	case opFlag:
		p, ok := pickPlacement()
		if !ok {
			return skip("no messages")
		}
		flag := flagPool[o.b%len(flagPool)]
		if err := h.acc.Flag(p.folder, p.uid, flag); err != nil {
			return "flag " + label(p), false, err
		}
		p.msg.flags[strings.ToLower(string(flag))] = struct{}{}
		return fmt.Sprintf("flag %s +%s", label(p), flag), true, nil

	case opUnflag:
		p, ok := pickPlacement()
		if !ok {
			return skip("no messages")
		}
		set := p.msg.flagList()
		if len(set) == 0 {
			return skip("message has no flags")
		}
		flag := set[o.b%len(set)]
		if err := h.acc.Unflag(p.folder, p.uid, imap.Flag(flag)); err != nil {
			return "unflag " + label(p), false, err
		}
		delete(p.msg.flags, flag)
		return fmt.Sprintf("unflag %s -%s", label(p), flag), true, nil

	case opMove:
		p, ok := pickPlacement()
		if !ok {
			return skip("no messages")
		}
		dest, ok := otherFolder(p.folder)
		if !ok {
			return skip("no other folder")
		}
		if err := h.acc.Move(p.folder, p.uid, dest); err != nil {
			return "move " + label(p), false, err
		}
		delete(m.folders[p.folder].msgs, p.uid)
		uid := m.folders[dest].uidNext
		m.folders[dest].uidNext++
		m.folders[dest].msgs[uid] = p.msg
		return fmt.Sprintf("move %s -> %s (uid %d)", label(p), dest, uid), true, nil

	case opExpunge:
		p, ok := pickPlacement()
		if !ok {
			return skip("no messages")
		}
		if err := h.acc.Expunge(p.folder, p.uid); err != nil {
			return "expunge " + label(p), false, err
		}
		delete(m.folders[p.folder].msgs, p.uid)
		return "expunge " + label(p), true, nil

	case opCreateFolder:
		name, ok := freeFolderName()
		if !ok {
			return skip("every pool name exists")
		}
		if err := h.acc.CreateMailbox(name); err != nil {
			return "create-folder " + name, false, err
		}
		m.folders[name] = &mfolder{uidNext: 1, msgs: map[uint32]*mmsg{}}
		return "create-folder " + name, true, nil

	case opRenameFolder:
		old, ok := namedFolder()
		if !ok {
			return skip("no folder but INBOX")
		}
		name, ok := freeFolderName()
		if !ok {
			return skip("every pool name exists")
		}
		if err := h.acc.RenameMailbox(old, name); err != nil {
			return fmt.Sprintf("rename-folder %s -> %s", old, name), false, err
		}
		m.folders[name] = m.folders[old]
		delete(m.folders, old)
		return fmt.Sprintf("rename-folder %s -> %s (UIDs and UIDVALIDITY kept)", old, name), true, nil

	case opDeleteFolder:
		name, ok := namedFolder()
		if !ok {
			return skip("no folder but INBOX")
		}
		if err := h.acc.DeleteMailbox(name); err != nil {
			return "delete-folder " + name, false, err
		}
		gone := len(m.folders[name].msgs)
		delete(m.folders, name)
		return fmt.Sprintf("delete-folder %s (%d messages go with it)", name, gone), true, nil

	case opBumpValidity:
		names := m.names()
		name := names[o.a%len(names)]
		if err := h.acc.BumpUIDValidity(name); err != nil {
			return "bump-uidvalidity " + name, false, err
		}
		gone := len(m.folders[name].msgs)
		m.folders[name] = &mfolder{uidNext: 1, msgs: map[uint32]*mmsg{}}
		return fmt.Sprintf("bump-uidvalidity %s (%d messages gone, UIDs restart at 1)", name, gone), true, nil
	}
	return "", false, fmt.Errorf("unknown op %d", o.kind)
}

// -------------------------------------------------------------- the server

type serverFolder struct {
	name        string
	uidValidity uint32
	uidNext     uint32
	msgs        []mailworld.ServerMessage
}

// serverSnapshot is what any client would see, read through the fake's own IMAP
// connection with no Ivy code in between: the oracle's source of truth.
func (h *run) serverSnapshot() ([]serverFolder, error) {
	infos, err := h.acc.Mailboxes()
	if err != nil {
		return nil, err
	}
	out := make([]serverFolder, 0, len(infos))
	for _, mb := range infos {
		msgs, err := h.acc.Messages(mb.Name)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", mb.Name, err)
		}
		out = append(out, serverFolder{mb.Name, mb.UIDValidity, mb.UIDNext, msgs})
	}
	slices.SortFunc(out, func(a, b serverFolder) int { return cmp.Compare(a.name, b.name) })
	return out, nil
}

// modelVsServer reports how the model differs from the server, or "" if they
// agree. A difference means the harness, not sync, is wrong.
func (h *run) modelVsServer() (string, error) {
	snap, err := h.serverSnapshot()
	if err != nil {
		return "", err
	}
	if got, want := folderNames(snap), h.mdl.names(); !slices.Equal(got, want) {
		return fmt.Sprintf("folders: server %v, model %v", got, want), nil
	}
	for _, sf := range snap {
		mf := h.mdl.folders[sf.name]
		if sf.uidNext != mf.uidNext {
			return fmt.Sprintf("%s: server UIDNEXT %d, model %d", sf.name, sf.uidNext, mf.uidNext), nil
		}
		if len(sf.msgs) != len(mf.msgs) {
			return fmt.Sprintf("%s: server holds %d messages, model %d", sf.name, len(sf.msgs), len(mf.msgs)), nil
		}
		for _, sm := range sf.msgs {
			mm := mf.msgs[sm.UID]
			switch {
			case mm == nil:
				return fmt.Sprintf("%s: server has uid %d, model does not", sf.name, sm.UID), nil
			case sm.MessageID != messageID(mm.id):
				return fmt.Sprintf("%s/uid %d: server Message-ID %s, model %s", sf.name, sm.UID, sm.MessageID, messageID(mm.id)), nil
			case flagKey(lowerFlags(sm.Flags)) != flagKey(mm.flagList()):
				return fmt.Sprintf("%s/uid %d: server flags %q, model %q", sf.name, sm.UID, flagKey(lowerFlags(sm.Flags)), flagKey(mm.flagList())), nil
			}
		}
	}
	return "", nil
}

func folderNames(snap []serverFolder) []string {
	out := make([]string, len(snap))
	for i, f := range snap {
		out[i] = f.name
	}
	return out
}

// ----------------------------------------------------------------- the mirror

// row is one mirror message row, read by plain SQL so nothing between the
// database and the oracle can hide a disabled message.
type row struct {
	id, folderID, folder string
	uid                  uint32
	msgID, key           string
	flags                []string
	seen, disabled       bool
	reason, thread       string
	blob                 []byte
	path                 string
}

type rowState struct {
	disabled bool
	reason   string
}

func (h *run) readRows(ctx context.Context) ([]row, error) {
	rs, err := h.dbs.Mirror.Read.QueryContext(ctx, `
		SELECT m.id, m.folder_id, COALESCE(f.name, ''), m.uid,
		       COALESCE(m.message_id_hdr, ''), m.content_key, COALESCE(m.flags_json, ''),
		       m.seen, m.disabled_at IS NOT NULL, COALESCE(m.disabled_reason, ''),
		       COALESCE(m.thread_id, ''), m.raw_blob, COALESCE(m.raw_path, '')
		FROM messages m LEFT JOIN folders f ON f.id = m.folder_id
		WHERE m.account_id = ? ORDER BY m.id`, harnessAccount)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	var out []row
	for rs.Next() {
		var r row
		var flagsJSON string
		if err := rs.Scan(&r.id, &r.folderID, &r.folder, &r.uid, &r.msgID, &r.key, &flagsJSON,
			&r.seen, &r.disabled, &r.reason, &r.thread, &r.blob, &r.path); err != nil {
			return nil, err
		}
		r.flags = parseFlagsJSON(flagsJSON)
		out = append(out, r)
	}
	return out, rs.Err()
}

func parseFlagsJSON(s string) []string {
	var flags []string
	if s != "" {
		// A malformed value would make every flag comparison wrong in a confusing
		// way, so fail loudly: this is the oracle reading its own database.
		if err := json.Unmarshal([]byte(s), &flags); err != nil {
			panic(fmt.Sprintf("flags_json %q is not a JSON list: %v", s, err))
		}
	}
	return flags
}

// rawOf is a row's original bytes from the row or its spool file, or nil when
// neither holds them.
func (h *run) rawOf(r row) []byte {
	if len(r.blob) > 0 {
		return r.blob
	}
	if r.path == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(h.dbs.Dir, filepath.FromSlash(r.path)))
	if err != nil {
		return nil
	}
	return b
}

func logicalOf(msgID string) (int, bool) {
	var n int
	if _, err := fmt.Sscanf(msgID, "<m%d@sync.test>", &n); err != nil || messageID(n) != msgID {
		return 0, false
	}
	return n, true
}

// ---------------------------------------------------------------- the oracle

// checkSync runs the system under test against the quiet server and checks the
// mirror against what the server really holds. It returns the first failure.
func (h *run) checkSync() *failure {
	ctx, cancel := context.WithTimeout(context.Background(), syncDeadline)
	defer cancel()

	snap, err := h.serverSnapshot()
	if err != nil {
		return &failure{"harness", fmt.Sprintf("reading the server: %v", err)}
	}
	if err := h.c.sut(ctx, h.dbs, h.fetcher, h.acct); err != nil {
		return &failure{"sync failed", err.Error()}
	}
	rows, err := h.readRows(ctx)
	if err != nil {
		return &failure{"harness", fmt.Sprintf("reading the mirror: %v", err)}
	}

	// Invariant 4 (sync defers to the outbox): once the outbox exists (3d), rows
	// with a pending op are left out of the checks below. There is none yet.
	if f := h.checkVisibleEqualsServer(snap, rows); f != nil {
		return f
	}
	if f := h.checkRowInvariants(rows); f != nil {
		return f
	}
	if f := h.checkNothingErased(rows); f != nil {
		return f
	}
	if f := h.checkDisabledReasons(snap, rows); f != nil {
		return f
	}
	if f := h.checkFolders(ctx, snap); f != nil {
		return f
	}

	// The second attempt: syncing again against the same quiet server must
	// change nothing (STANDARDS.md 4a, repeat and resume).
	before := digest(rows)
	if err := h.c.sut(ctx, h.dbs, h.fetcher, h.acct); err != nil {
		return &failure{"sync failed", "second run: " + err.Error()}
	}
	again, err := h.readRows(ctx)
	if err != nil {
		return &failure{"harness", fmt.Sprintf("reading the mirror again: %v", err)}
	}
	if after := digest(again); !slices.Equal(before, after) {
		return &failure{"second sync changed the mirror", firstDifference(before, after)}
	}

	for _, r := range rows {
		h.prev[r.id] = rowState{r.disabled, r.reason}
	}
	return nil
}

// serverKeys is every message the server holds, as "folder|uid|Message-ID|flags".
func serverKeys(snap []serverFolder) []string {
	var out []string
	for _, f := range snap {
		for _, m := range f.msgs {
			out = append(out, fmt.Sprintf("%s|%d|%s|%s", f.name, m.UID, m.MessageID, flagKey(lowerFlags(m.Flags))))
		}
	}
	slices.Sort(out)
	return out
}

func (h *run) checkVisibleEqualsServer(snap []serverFolder, rows []row) *failure {
	var got []string
	for _, r := range rows {
		if !r.disabled {
			got = append(got, fmt.Sprintf("%s|%d|%s|%s", r.folder, r.uid, r.msgID, flagKey(r.flags)))
		}
	}
	slices.Sort(got)
	want := serverKeys(snap)
	if slices.Equal(got, want) {
		return nil
	}
	missing, extra := setDiff(want, got), setDiff(got, want)
	return &failure{"mirror differs from server", fmt.Sprintf(
		"the server holds %d messages and the mirror shows %d visible.\n"+
			"      on the server but not visible in the mirror (folder|uid|Message-ID|flags): %s\n"+
			"      visible in the mirror but not on the server: %s",
		len(want), len(got), clip(missing), clip(extra))}
}

func (h *run) checkRowInvariants(rows []row) *failure {
	for _, r := range rows {
		if want := store.ContentKey(r.msgID, nil); r.key != want {
			return &failure{"content key wrong", fmt.Sprintf("%s has key %s, want the hash of its Message-ID (%s)", r.msgID, r.key, want)}
		}
		if r.disabled {
			continue
		}
		if has := slices.Contains(lowerFlagsOf(r.flags), `\seen`); r.seen != has {
			return &failure{"seen column disagrees with flags", fmt.Sprintf("%s in %s: seen=%v but flags %q", r.msgID, r.folder, r.seen, flagKey(r.flags))}
		}
		if r.thread == "" {
			return &failure{"visible message without a thread", fmt.Sprintf("%s in %s/uid %d has no thread_id", r.msgID, r.folder, r.uid)}
		}
	}
	return nil
}

func lowerFlagsOf(flags []string) []string {
	out := make([]string, len(flags))
	for i, f := range flags {
		out[i] = strings.ToLower(f)
	}
	return out
}

// checkNothingErased is invariant 1: no row is ever deleted, and every message
// that was ever mirrored still has its original bytes in some row (live or
// disabled), whether in the row or in its spool file.
func (h *run) checkNothingErased(rows []row) *failure {
	now := map[string]bool{}
	for _, r := range rows {
		now[r.id] = true
	}
	for id := range h.seenRows {
		if !now[id] {
			return &failure{"message row deleted", fmt.Sprintf("row %s was in the mirror and is gone; nothing may ever be erased", id)}
		}
	}
	for _, r := range rows {
		h.seenRows[r.id] = true
		h.everMirrored[r.msgID] = true
	}
	for msgID := range h.everMirrored {
		n, ok := logicalOf(msgID)
		if !ok {
			continue
		}
		kept := false
		for _, r := range rows {
			if r.msgID == msgID && bytes.Equal(h.rawOf(r), h.raw[n]) {
				kept = true
				break
			}
		}
		if !kept {
			return &failure{"raw message lost", fmt.Sprintf("%s was mirrored and no row, disabled or not, still holds its original bytes (row or spool file)", msgID)}
		}
	}
	return nil
}

// checkDisabledReasons is the round 37 rule: a message disabled in this pass
// whose Message-ID is still on the server is a move, and any other is a removal.
func (h *run) checkDisabledReasons(snap []serverFolder, rows []row) *failure {
	live := map[string]bool{}
	for _, f := range snap {
		for _, m := range f.msgs {
			live[m.MessageID] = true
		}
	}
	for _, r := range rows {
		if !r.disabled || h.prev[r.id].disabled {
			continue
		}
		switch {
		case live[r.msgID] && r.reason != "moved":
			return &failure{"wrong disabled reason", fmt.Sprintf("%s (%s/uid %d) was disabled but is still on the server, so it is a move: reason %q, want \"moved\"", r.msgID, r.folder, r.uid, r.reason)}
		case !live[r.msgID] && (r.reason == "" || r.reason == "moved"):
			return &failure{"wrong disabled reason", fmt.Sprintf("%s (%s/uid %d) was disabled and is nowhere on the server, so it was removed: reason %q is not a removal reason", r.msgID, r.folder, r.uid, r.reason)}
		}
	}
	return nil
}

func (h *run) checkFolders(ctx context.Context, snap []serverFolder) *failure {
	// Only what the server has is checked. What the mirror does with a folder the
	// server deleted (keep its row, hide it, mark it gone) is a 3a/3b design
	// question recorded in the C1 handoff, so it is not pinned here.
	for _, sf := range snap {
		f, err := h.dbs.GetFolderByName(ctx, harnessAccount, sf.name)
		if err != nil {
			return &failure{"folder differs from server", fmt.Sprintf("the server has %s and the mirror has no folder row: %v", sf.name, err)}
		}
		if f.UIDValidity != sf.uidValidity {
			return &failure{"folder differs from server", fmt.Sprintf("%s: server UIDVALIDITY %d, mirror %d", sf.name, sf.uidValidity, f.UIDValidity)}
		}
	}
	return nil
}

// digest is every row in a comparable form, to prove a second sync is a no-op.
func digest(rows []row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = fmt.Sprintf("%s|%s|%d|%s|%s|%v|%s|%s", r.id, r.folderID, r.uid, r.msgID, flagKey(r.flags), r.disabled, r.reason, r.thread)
	}
	slices.Sort(out)
	return out
}

func firstDifference(a, b []string) string {
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y string
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return fmt.Sprintf("row %d was %q and became %q", i, x, y)
		}
	}
	return "the rows differ"
}

// setDiff returns the elements of a that are not in b (both sorted).
func setDiff(a, b []string) []string {
	have := map[string]int{}
	for _, s := range b {
		have[s]++
	}
	var out []string
	for _, s := range a {
		if have[s] > 0 {
			have[s]--
			continue
		}
		out = append(out, s)
	}
	return out
}

func clip(list []string) string {
	if len(list) == 0 {
		return "none"
	}
	const limit = 4
	if len(list) <= limit {
		return strings.Join(list, "; ")
	}
	return fmt.Sprintf("%s; and %d more", strings.Join(list[:limit], "; "), len(list)-limit)
}

// ------------------------------------------------------------------ shrinking

// shrink reduces a failing script to a smaller one that still fails, by deleting
// chunks (delta debugging) and then simplifying each remaining op's choices. It
// stops after budget replays, so a slow predicate cannot run away; exhausted
// reports that the result may not be minimal.
func shrink(script []op, budget int, fails func([]op) bool) (smallest []op, replays int, exhausted bool) {
	try := func(cand []op) bool {
		if replays >= budget {
			exhausted = true
			return false
		}
		replays++
		return fails(cand)
	}
	cur := slices.Clone(script)

	for n := 2; len(cur) >= 2; {
		chunk := (len(cur) + n - 1) / n
		reduced := false
		for start := 0; start < len(cur); start += chunk {
			end := min(start+chunk, len(cur))
			cand := append(slices.Clone(cur[:start]), cur[end:]...)
			if try(cand) {
				cur, reduced = cand, true
				n = max(n-1, 2)
				break
			}
			if exhausted {
				return cur, replays, true
			}
		}
		if !reduced {
			if n >= len(cur) {
				break
			}
			n = min(n*2, len(cur))
		}
	}

	// Choices of zero are the simplest, so a reproducer reads "the first message,
	// the first folder" where it can.
	for i := range cur {
		simpler := op{kind: cur[i].kind, n: cur[i].n}
		if simpler == cur[i] {
			continue
		}
		cand := slices.Clone(cur)
		cand[i] = simpler
		if try(cand) {
			cur = cand
		}
	}
	return cur, replays, exhausted
}
