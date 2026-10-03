// Package thread groups messages into conversations with JWZ threading
// (https://www.jwz.org/doc/threading.html), falling back to a normalized
// subject when the References and In-Reply-To headers do not connect a run of
// messages.
//
// It is pure: it reads the header fields a caller already parsed and returns
// the assignment, with no database, network or clock. The mirror stores the
// result on each message (messages.thread_id) and in the threads table, so a
// conversation survives moves and UID changes by the stable content key each
// message is keyed on (ARCHITECTURE.md 3).
package thread

import (
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// messageIDRe pulls the angle-bracketed ids out of References and In-Reply-To.
// Message-IDs cannot contain angle brackets, so this cannot backtrack.
var messageIDRe = regexp.MustCompile(`<[^<>]+>`)

// replyPrefixRe is the "Re:" family JWZ's subject grouping strips, with the
// "RE[5]:" counter form. It is deliberately not "Fwd:": JWZ treats a forward as
// a different subject, and grouping it here would merge unrelated mail.
var replyPrefixRe = regexp.MustCompile(`(?i)^[ \t]*re[ \t]*(\[[0-9]+\])?[ \t]*:[ \t]*`)

// Message is one message offered to the algorithm. ID is opaque and only used
// to name the message in the output; Key is the stable identity (the content
// key) that becomes the thread's id. MessageID, References and InReplyTo are the
// verbatim header values: references are parsed here.
type Message struct {
	ID         string
	Key        string
	MessageID  string
	References string
	InReplyTo  string
	Subject    string
	Date       time.Time
}

// Thread is one conversation. ID is the *proposed* id: the Key of the message at
// the root (or of the earliest message when the root is a placeholder for a
// message the mirror does not hold). It is not what gets stored:
// store.ReplaceThreads keeps a conversation's existing id when it has one and
// scopes a new one to the account. MessageIDs are the input IDs, oldest first,
// which that rule depends on.
type Thread struct {
	ID            string
	RootMessageID string
	SubjectNorm   string
	LastDate      time.Time
	MessageIDs    []string
}

// container is a JWZ node: either a real message or a placeholder for an id
// named in a References/In-Reply-To header that the mirror does not hold.
type container struct {
	id       string // the Message-ID this node stands for; set for placeholders too
	msg      *Message
	parent   *container
	children []*container
}

// Build groups messages into conversations. Every input message appears in
// exactly one returned thread.
func Build(messages []Message) []Thread {
	if len(messages) == 0 {
		return nil
	}

	// Process oldest first, then by id, so which of two identical Message-IDs
	// becomes the canonical parent never depends on row order.
	ordered := slices.Clone(messages)
	sort.SliceStable(ordered, func(i, j int) bool {
		if !ordered[i].Date.Equal(ordered[j].Date) {
			return ordered[i].Date.Before(ordered[j].Date)
		}
		return ordered[i].ID < ordered[j].ID
	})

	idTable := map[string]*container{}
	all := make([]*container, 0, len(ordered))
	var dummies []*container
	for i := range ordered {
		c := &container{id: ordered[i].MessageID, msg: &ordered[i]}
		all = append(all, c)
		if id := ordered[i].MessageID; id != "" {
			if idTable[id] == nil {
				idTable[id] = c
			}
		}
	}

	// JWZ step 1: link each message to its parent through References (with the
	// first In-Reply-To id appended when References does not already name it),
	// creating placeholders for ids we have not seen.
	for _, c := range all {
		var prev *container
		for _, id := range referenceIDs(c.msg) {
			rc := idTable[id]
			if rc == nil {
				rc = &container{id: id}
				idTable[id] = rc
				dummies = append(dummies, rc)
			}
			if prev != nil {
				link(prev, rc)
			}
			prev = rc
		}
		if prev != nil {
			setParent(c, prev)
		}
	}

	roots := make([]*container, 0, len(all)+len(dummies))
	for _, c := range all {
		if c.parent == nil {
			roots = append(roots, c)
		}
	}
	for _, d := range dummies {
		if d.parent == nil {
			roots = append(roots, d)
		}
	}

	roots = pruneRoots(roots)
	for _, r := range roots {
		sortChildren(r)
	}
	sortRoots(roots)
	roots = groupBySubject(roots)

	out := make([]Thread, 0, len(roots))
	for _, r := range roots {
		members := collect(r)
		if len(members) == 0 {
			continue
		}
		sortMembers(members)
		rep := members[0].msg
		rootMessageID := r.id
		if r.msg != nil {
			rep = r.msg
			rootMessageID = r.msg.MessageID
		}
		t := Thread{
			ID:            rep.Key,
			RootMessageID: rootMessageID,
			SubjectNorm:   normalizeSubject(subtreeSubject(r)),
			LastDate:      members[len(members)-1].msg.Date,
		}
		if t.ID == "" {
			t.ID = rep.ID
		}
		for _, m := range members {
			t.MessageIDs = append(t.MessageIDs, m.msg.ID)
		}
		out = append(out, t)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].LastDate.Equal(out[j].LastDate) {
			return out[i].LastDate.Before(out[j].LastDate)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// referenceIDs returns the References ids in order, with the first In-Reply-To
// id appended when References does not already carry it (JWZ's preliminary).
// Appending only when absent avoids re-parenting a message to an ancestor that
// a broken sender left in both headers.
func referenceIDs(m *Message) []string {
	refs := parseMessageIDs(m.References)
	if in := parseMessageIDs(m.InReplyTo); len(in) > 0 && !slices.Contains(refs, in[0]) {
		refs = append(refs, in[0])
	}
	return refs
}

func parseMessageIDs(s string) []string {
	if s == "" {
		return nil
	}
	return messageIDRe.FindAllString(s, -1)
}

// link makes parent the parent of child only when child has none yet: JWZ says
// not to change reference links that already exist.
func link(parent, child *container) {
	if child.parent != nil {
		return
	}
	setParent(child, parent)
}

// setParent re-parents child under parent, unlinking it from its old parent and
// refusing a link that would introduce a cycle.
func setParent(child, parent *container) {
	if child == nil || parent == nil || child == parent || child.parent == parent {
		return
	}
	// A leaf cannot be an ancestor of anything, so the cycle check is only
	// needed when child already has a subtree. Skipping it for a fresh message
	// or placeholder keeps a long References chain linear rather than quadratic.
	if len(child.children) > 0 && isDescendant(child, parent) {
		return
	}
	if child.parent != nil {
		child.parent.removeChild(child)
	}
	child.parent = parent
	parent.children = append(parent.children, child)
}

func (c *container) removeChild(target *container) {
	for i, ch := range c.children {
		if ch == target {
			c.children = append(c.children[:i], c.children[i+1:]...)
			return
		}
	}
}

// isDescendant reports whether node sits somewhere below ancestor.
func isDescendant(ancestor, node *container) bool {
	for p := node.parent; p != nil; p = p.parent {
		if p == ancestor {
			return true
		}
	}
	return false
}

// pruneRoots applies JWZ step 4 to the root set: placeholder leaves are dropped,
// and a placeholder with children is spliced out (its children are promoted)
// unless promoting them would create several roots, in which case the
// placeholder stands as the thread root.
func pruneRoots(roots []*container) []*container {
	out := make([]*container, 0, len(roots))
	for _, r := range roots {
		pruneChildren(r)
		if r.msg == nil {
			switch len(r.children) {
			case 0:
				continue
			case 1:
				child := r.children[0]
				child.parent = nil
				out = append(out, child)
			default:
				out = append(out, r)
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

func pruneChildren(c *container) {
	for _, child := range c.children {
		pruneChildren(child)
	}
	kept := make([]*container, 0, len(c.children))
	for _, child := range c.children {
		switch {
		case child.msg == nil && len(child.children) == 0:
			// Placeholder with nothing under it: drop it.
		case child.msg == nil:
			// Placeholder with children: splice them into this level.
			for _, g := range child.children {
				g.parent = c
				kept = append(kept, g)
			}
		default:
			kept = append(kept, child)
		}
	}
	c.children = kept
}

// groupBySubject merges root subtrees that share a normalized subject (JWZ step
// 5), so mail with no useful References still threads.
func groupBySubject(roots []*container) []*container {
	groups := map[string][]*container{}
	var order []string
	out := make([]*container, 0, len(roots))
	for _, r := range roots {
		subj := normalizeSubject(subtreeSubject(r))
		if subj == "" {
			out = append(out, r)
			continue
		}
		if _, ok := groups[subj]; !ok {
			order = append(order, subj)
		}
		groups[subj] = append(groups[subj], r)
	}
	for _, subj := range order {
		g := groups[subj]
		chosen := g[0]
		for _, c := range g[1:] {
			if betterRoot(c, chosen) {
				chosen = c
			}
		}
		root := chosen
		for _, c := range g {
			if c != chosen {
				root = merge(root, c)
			}
		}
		out = append(out, root)
	}
	return out
}

// betterRoot reports whether candidate makes a better thread root than current:
// a placeholder beats a real message, and a non-Re: subject beats a "Re:" one.
func betterRoot(candidate, current *container) bool {
	if candidate.msg == nil && current.msg != nil {
		return true
	}
	if candidate.msg != nil && current.msg == nil {
		return false
	}
	return !isReplyContainer(candidate) && isReplyContainer(current)
}

// merge joins two root subtrees that share a subject, following the JWZ cases:
// placeholders absorb, a reply becomes a child of the base subject, and two
// unrelated real messages become siblings under a fresh placeholder.
func merge(a, b *container) *container {
	switch {
	case a.msg == nil && b.msg == nil:
		for _, child := range slices.Clone(b.children) {
			setParent(child, a)
		}
		return a
	case a.msg == nil:
		setParent(b, a)
		return a
	case b.msg == nil:
		setParent(a, b)
		return b
	case !isReplyContainer(a) && isReplyContainer(b):
		setParent(b, a)
		return a
	case isReplyContainer(a) && !isReplyContainer(b):
		setParent(a, b)
		return b
	default:
		dummy := &container{}
		setParent(a, dummy)
		setParent(b, dummy)
		return dummy
	}
}

func collect(r *container) []*container {
	out := make([]*container, 0, 4)
	var walk func(*container)
	walk = func(c *container) {
		if c.msg != nil {
			out = append(out, c)
		}
		for _, child := range c.children {
			walk(child)
		}
	}
	walk(r)
	return out
}

// firstMessage returns the earliest real message in a subtree, which is what a
// placeholder's subject is taken from.
func firstMessage(c *container) *Message {
	if c.msg != nil {
		return c.msg
	}
	for _, child := range c.children {
		if m := firstMessage(child); m != nil {
			return m
		}
	}
	return nil
}

func subtreeSubject(c *container) string {
	if m := firstMessage(c); m != nil {
		return m.Subject
	}
	return ""
}

func isReplyContainer(c *container) bool {
	return hasReplyPrefix(subtreeSubject(c))
}

func hasReplyPrefix(s string) bool {
	return replyPrefixRe.MatchString(s)
}

// normalizeSubject strips every leading "Re:"-family prefix so a reply threads
// with its base. An empty result means "do not group" (JWZ step 5.2).
func normalizeSubject(s string) string {
	for {
		loc := replyPrefixRe.FindStringIndex(s)
		if loc == nil {
			break
		}
		s = s[loc[1]:]
	}
	return strings.TrimSpace(s)
}

func sortRoots(roots []*container) {
	sort.SliceStable(roots, func(i, j int) bool {
		return earlier(firstMessage(roots[i]), firstMessage(roots[j]))
	})
}

func sortChildren(c *container) {
	sort.SliceStable(c.children, func(i, j int) bool {
		return earlier(firstMessage(c.children[i]), firstMessage(c.children[j]))
	})
	for _, child := range c.children {
		sortChildren(child)
	}
}

func sortMembers(members []*container) {
	sort.SliceStable(members, func(i, j int) bool {
		return earlier(members[i].msg, members[j].msg)
	})
}

// earlier orders by date and then id, so every pass is deterministic and a
// thread's members read oldest first. A nil message sorts last.
func earlier(a, b *Message) bool {
	if a == nil || b == nil {
		return a != nil
	}
	if !a.Date.Equal(b.Date) {
		return a.Date.Before(b.Date)
	}
	return a.ID < b.ID
}
