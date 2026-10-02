// Spike S1: what does the real mail provider do? Read-mostly; never prints credentials or mail.
// Writes only one scratch folder with one message, deleted at the end.
package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

func loadEnv(path string) map[string]string {
	f, err := os.Open(path)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	env := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok {
			env[k] = strings.Trim(v, `"'`)
		}
	}
	return env
}

func main() {
	env := loadEnv("../../.env")
	addr := net.JoinHostPort(env["IMAP_HOST"], env["IMAP_PORT"])
	for _, k := range []string{"MAIL_USER", "MAIL_PASSWORD", "IMAP_HOST", "SMTP_HOST"} {
		if env[k] == "" {
			log.Fatalf("%s is empty in .env", k)
		}
	}

	c, err := imapclient.DialTLS(addr, nil)
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer c.Close()

	pre, _ := c.Capability().Wait()
	fmt.Println("== pre-login capabilities:", capNames(pre))
	if err := c.Login(env["MAIL_USER"], env["MAIL_PASSWORD"]).Wait(); err != nil {
		log.Fatalf("login failed: %v", scrub(err, env))
	}
	caps, err := c.Capability().Wait()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("== post-login capabilities:", capNames(caps))
	for _, want := range []imap.Cap{
		imap.CapCondStore, imap.CapQResync, imap.CapMove, imap.CapUIDPlus, imap.CapIdle,
		imap.CapSpecialUse, imap.CapUnselect, imap.CapSort, imap.CapESearch, imap.CapListExtended,
		imap.CapMetadata, imap.CapMetadataServer, "COMPRESS=DEFLATE", "ANNOTATE-EXPERIMENT-1",
		"X-GM-EXT-1", "THREAD=REFERENCES", "SEARCHRES", "SAVEDATE", "OBJECTID", "NOTIFY",
	} {
		fmt.Printf("  %-24s %v\n", want, caps.Has(want))
	}

	boxes, err := c.List("", "*", nil).Collect()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("== %d mailboxes; special-use:\n", len(boxes))
	for _, b := range boxes {
		for _, a := range b.Attrs {
			if strings.HasPrefix(string(a), `\`) && a != imap.MailboxAttrHasNoChildren && a != imap.MailboxAttrHasChildren {
				fmt.Printf("  %-10s delimiter=%q\n", a, b.Delim)
			}
		}
	}

	const scratch = "ivy-spike-scratch"
	if err := c.Create(scratch, nil).Wait(); err != nil {
		log.Fatalf("create scratch: %v", err)
	}
	defer func() {
		_ = c.Unselect().Wait()
		if err := c.Delete(scratch).Wait(); err != nil {
			fmt.Println("!! could not delete scratch folder, remove it by hand:", err)
		} else {
			fmt.Println("== scratch folder deleted")
		}
	}()

	msg := "From: spike@example.com\r\nTo: spike@example.com\r\nSubject: ivy spike\r\nMessage-ID: <ivy-spike-1@example.com>\r\n\r\nbody\r\n"
	ap := c.Append(scratch, int64(len(msg)), nil)
	_, _ = ap.Write([]byte(msg))
	_ = ap.Close()
	apData, err := ap.Wait()
	if err != nil {
		log.Fatalf("append: %v", err)
	}
	fmt.Printf("== APPEND ok; APPENDUID returned: %v\n", apData != nil && apData.UID != 0)

	sel, err := c.Select(scratch, &imap.SelectOptions{CondStore: caps.Has(imap.CapCondStore)}).Wait()
	if err != nil {
		log.Fatalf("select: %v", err)
	}
	fmt.Printf("== PERMANENTFLAGS: %v\n", sel.PermanentFlags)
	fmt.Printf("   HIGHESTMODSEQ=%d uidvalidity>0=%v\n", sel.HighestModSeq, sel.UIDValidity > 0)

	for _, kw := range []imap.Flag{"$ivy-tag", "ivytag-receipts", "$label1"} {
		err := c.Store(imap.UIDSetNum(apData.UID), &imap.StoreFlags{
			Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{kw},
		}, nil).Close()
		fmt.Printf("   STORE keyword %-16s err=%v\n", kw, err)
	}
	// Persistence: reselect and read the flags back, not just trust the STORE reply.
	_ = c.Unselect().Wait()
	if _, err := c.Select(scratch, nil).Wait(); err != nil {
		log.Fatal(err)
	}
	got, err := c.Fetch(imap.UIDSetNum(apData.UID), &imap.FetchOptions{Flags: true}).Collect()
	if err != nil || len(got) == 0 {
		fmt.Println("   fetch flags failed:", err)
	} else {
		fmt.Printf("   flags after reselect: %v\n", got[0].Flags)
	}

	inboxAuthProbe(c)

	fmt.Println("== connection limit probe (up to 12 simultaneous logins)")
	var extra []*imapclient.Client
	for i := 1; i <= 12; i++ {
		x, err := imapclient.DialTLS(addr, nil)
		if err == nil {
			err = x.Login(env["MAIL_USER"], env["MAIL_PASSWORD"]).Wait()
		}
		if err != nil {
			fmt.Printf("   connection %d (plus the main one) refused: %v\n", i, scrub(err, env))
			break
		}
		extra = append(extra, x)
		if i == 12 {
			fmt.Println("   all 12 extra connections accepted")
		}
	}
	for _, x := range extra {
		_ = x.Close()
	}

	smtpProbe(env)
}

// inboxAuthProbe reads only the headers of the newest INBOX message, read-only, and prints header
// names and the SPF/DKIM/DMARC verdicts, never the sender, subject or body.
func inboxAuthProbe(c *imapclient.Client) {
	fmt.Println("== newest INBOX message: headers only")
	sel, err := c.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil || sel.NumMessages == 0 {
		fmt.Println("   INBOX empty or unreadable:", err)
		return
	}
	sect := &imap.FetchItemBodySection{Specifier: imap.PartSpecifierHeader, Peek: true}
	msgs, err := c.Fetch(imap.SeqSetNum(sel.NumMessages), &imap.FetchOptions{BodySection: []*imap.FetchItemBodySection{sect}}).Collect()
	if err != nil || len(msgs) == 0 {
		fmt.Println("   fetch failed:", err)
		return
	}
	m, err := mail.ReadMessage(bytes.NewReader(msgs[0].FindBodySection(sect)))
	if err != nil {
		fmt.Println("   parse failed:", err)
		return
	}
	var names []string
	for k := range m.Header {
		names = append(names, k)
	}
	sort.Strings(names)
	fmt.Println("   header names:", strings.Join(names, ", "))
	hostLike := regexp.MustCompile(`[\w.+-]*[.@][\w.+-]+`)
	verdict := regexp.MustCompile(`(?i)\b(spf|dkim|dmarc|arc)=(\w+)`)
	for _, h := range []string{"Authentication-Results", "Received-Spf", "X-Spam-Status", "X-Spam-Flag"} {
		for _, v := range m.Header[h] {
			var out []string
			for _, mm := range verdict.FindAllStringSubmatch(v, -1) {
				out = append(out, mm[1]+"="+mm[2])
			}
			if h == "X-Spam-Status" || h == "X-Spam-Flag" {
				out = []string{strings.SplitN(strings.TrimSpace(v), ",", 2)[0]}
			}
			if len(out) == 0 {
				// Unknown shape: show it with anything host- or address-like masked.
				out = []string{hostLike.ReplaceAllString(strings.Join(strings.Fields(v), " "), "<x>")}
			}
			fmt.Printf("   %s: %s\n", h, strings.Join(out, " "))
		}
	}
}

func smtpProbe(env map[string]string) {
	fmt.Println("== SMTP (no mail sent)")
	addr := net.JoinHostPort(env["SMTP_HOST"], env["SMTP_PORT"])
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 15 * time.Second}, "tcp", addr, nil)
	if err != nil {
		fmt.Println("   dial:", err)
		return
	}
	sc, err := smtp.NewClient(conn, env["SMTP_HOST"])
	if err != nil {
		fmt.Println("   client:", err)
		return
	}
	defer sc.Close()
	if ok, p := sc.Extension("SIZE"); ok {
		fmt.Println("   SIZE:", p)
	} else {
		fmt.Println("   SIZE: not advertised")
	}
	for _, e := range []string{"AUTH", "8BITMIME", "SMTPUTF8", "PIPELINING", "CHUNKING", "DSN", "REQUIRETLS"} {
		ok, p := sc.Extension(e)
		fmt.Printf("   %-10s %v %s\n", e, ok, p)
	}
	auth := smtp.PlainAuth("", env["MAIL_USER"], env["MAIL_PASSWORD"], env["SMTP_HOST"])
	fmt.Println("   AUTH PLAIN works:", sc.Auth(auth) == nil)
	_ = sc.Quit()
}

func capNames(c imap.CapSet) string {
	var n []string
	for k := range c {
		n = append(n, string(k))
	}
	sort.Strings(n)
	return strings.Join(n, " ")
}

// scrub keeps credentials out of anything we print or commit.
func scrub(err error, env map[string]string) string {
	s := err.Error()
	for _, k := range []string{"MAIL_USER", "MAIL_PASSWORD"} {
		if env[k] != "" {
			s = strings.ReplaceAll(s, env[k], "<"+k+">")
		}
	}
	return s
}
