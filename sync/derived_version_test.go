package sync

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	mailmime "github.com/AutumnsGrove/Ivy/mime"
)

// derivedFingerprints pins what the derivation pipeline (parser, sanitizer,
// part walk) produces for a fixed corpus, one entry per DerivedVersion. Changing
// the output without bumping the version would leave every mirrored message
// with the old output forever, because rows at the current version are never
// re-derived; this test makes that a failure instead of a hope.
//
// When it fails: if you meant to change the output, bump DerivedVersion and add
// a new entry here with the hash the failure prints. Never edit an old entry.
var derivedFingerprints = map[int]string{
	1: "6cfecf0cdc86a2e92feef6c057bc44c6747bd3134be6205148fd560d6806a80c",
}

// inlineCorpus covers the shapes the fixture files do not: a mangled
// attachment (#48), a style that tries to fetch (#45), inline images and a
// remote beacon.
var inlineCorpus = map[string]string{
	"bad-base64": "From: a@example.com\r\nSubject: s\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\nthe real body\r\n" +
		"--b\r\nContent-Type: application/pdf; name=\"a.pdf\"\r\nContent-Disposition: attachment; filename=\"a.pdf\"\r\nContent-Transfer-Encoding: base64\r\n\r\nQUJD*#$%^&REVGR0hJ\r\n--b--\r\n",
	"styled-html": "From: a@example.com\r\nSubject: s\r\nMIME-Version: 1.0\r\nContent-Type: multipart/related; boundary=\"r\"\r\n\r\n" +
		"--r\r\nContent-Type: text/html; charset=utf-8\r\n\r\n" +
		"<ul style=\"list-style:url(https://t.example/b.gif);color:red\"><li>x</li></ul>" +
		"<p style=\"font:12px url(https://t.example/f.woff);color:blue\">y</p>" +
		"<img src=\"cid:logo@x\"><img src=\"https://t.example/p.gif\" width=1 height=1><a href=\"/api/v1/messages/m/inline/../../x\">l</a>\r\n" +
		"--r\r\nContent-Type: image/png\r\nContent-ID: <logo@x>\r\nContent-Transfer-Encoding: base64\r\n\r\niVBORw0KGgo=\r\n--r--\r\n",
}

func derivedFingerprint(t *testing.T) string {
	t.Helper()
	files := map[string][]byte{}
	for _, dir := range []string{"../mime/testdata/corpus", "../internal/mailworld/testdata/corpus"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read corpus %s: %v", dir, err)
		}
		for _, e := range entries {
			raw, err := os.ReadFile(filepath.Join(dir, e.Name())) //nolint:gosec // G304: fixed test corpus directories
			if err != nil {
				t.Fatalf("read %s: %v", e.Name(), err)
			}
			files[dir+"/"+e.Name()] = raw
		}
	}
	for name, raw := range inlineCorpus {
		files["inline/"+name] = []byte(raw)
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)

	h := sha256.New()
	for _, n := range names {
		d := derivedFrom("m", mailmime.ParseStream(bytes.NewReader(files[n])))
		out, err := json.Marshal(d)
		if err != nil {
			t.Fatalf("marshal %s: %v", n, err)
		}
		h.Write([]byte(n))
		h.Write(out)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestDerivedVersionMatchesTheCorpusOutput(t *testing.T) {
	t.Parallel()
	got := derivedFingerprint(t)
	want, ok := derivedFingerprints[DerivedVersion]
	if !ok {
		t.Fatalf("DerivedVersion %d has no entry in derivedFingerprints; add\n\t%d: %q,", DerivedVersion, DerivedVersion, got)
	}
	if got != want {
		t.Fatalf("the derived output of the corpus changed but DerivedVersion is still %d.\n"+
			"If the change is intended, bump DerivedVersion and add\n\t%d: %q,\nso already-mirrored mail is re-derived.\n"+
			"If it is not, a parser or sanitizer change altered what gets stored.", DerivedVersion, DerivedVersion+1, got)
	}
}
