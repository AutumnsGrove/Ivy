// Spike S1b: one real send (operator-authorised recipient), does SMTP file a copy in Sent, and a
// raw download of the INBOX into the git-ignored .dev/ directory.
package main

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/smtp"
	"os"
	"path/filepath"
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
		if k, v, ok := strings.Cut(line, "="); ok && !strings.HasPrefix(line, "#") {
			env[k] = strings.Trim(v, `"'`)
		}
	}
	return env
}

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: send <recipient>")
	}
	to := os.Args[1]
	env := loadEnv("../../../.env")

	c, err := imapclient.DialTLS(net.JoinHostPort(env["IMAP_HOST"], env["IMAP_PORT"]), nil)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()
	if err := c.Login(env["MAIL_USER"], env["MAIL_PASSWORD"]).Wait(); err != nil {
		log.Fatal("login failed")
	}
	boxes, err := c.List("", "*", nil).Collect()
	if err != nil {
		log.Fatal(err)
	}
	sent := ""
	for _, b := range boxes {
		for _, a := range b.Attrs {
			if a == imap.MailboxAttrSent {
				sent = b.Mailbox
			}
		}
	}
	fmt.Println("Sent folder found via attribute:", sent != "")

	sentCount := func() uint32 {
		s, err := c.Select(sent, &imap.SelectOptions{ReadOnly: true}).Wait()
		if err != nil {
			log.Fatal(err)
		}
		return s.NumMessages
	}
	if to == "-" { // download only, send nothing
		download(c)
		return
	}
	before := sentCount()

	id := fmt.Sprintf("<ivy-spike-%d@example.com>", time.Now().UnixNano())
	msg := strings.Join([]string{
		"From: " + env["MAIL_USER"],
		"To: " + to,
		"Subject: Ivy spike S1 test send",
		"Date: " + time.Now().Format(time.RFC1123Z),
		"Message-ID: " + id,
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"Automated test from the Ivy S1 spike. Safe to delete.",
		"",
	}, "\r\n")

	conn, err := tls.Dial("tcp", net.JoinHostPort(env["SMTP_HOST"], env["SMTP_PORT"]), nil)
	if err != nil {
		log.Fatal(err)
	}
	sc, err := smtp.NewClient(conn, env["SMTP_HOST"])
	if err != nil {
		log.Fatal(err)
	}
	if err := sc.Auth(smtp.PlainAuth("", env["MAIL_USER"], env["MAIL_PASSWORD"], env["SMTP_HOST"])); err != nil {
		log.Fatal("smtp auth failed")
	}
	if err := sc.Mail(env["MAIL_USER"]); err != nil {
		log.Fatal("MAIL FROM rejected: ", err)
	}
	if err := sc.Rcpt(to); err != nil {
		log.Fatal("RCPT rejected: ", err)
	}
	w, err := sc.Data()
	if err != nil {
		log.Fatal(err)
	}
	_, _ = w.Write([]byte(msg))
	if err := w.Close(); err != nil {
		log.Fatal("DATA rejected: ", err)
	}
	_ = sc.Quit()
	fmt.Println("SMTP accepted the message")

	var after uint32
	for i := 0; i < 10; i++ {
		time.Sleep(time.Second)
		if after = sentCount(); after > before {
			break
		}
	}
	fmt.Printf("Sent folder messages: before=%d after=%d => SMTP files a copy: %v\n", before, after, after > before)

	download(c)
}

// download saves every INBOX message as raw .eml (git-ignored) and prints a one-line summary each.
func download(c *imapclient.Client) {
	sel, err := c.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("INBOX: %d messages\n", sel.NumMessages)
	if sel.NumMessages == 0 {
		return
	}
	dir := "../../../.dev/s1-inbox"
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Fatal(err)
	}
	sect := &imap.FetchItemBodySection{Peek: true}
	// SeqSetNum(1, n) would mean "message 1 and message n", not the range.
	var all imap.SeqSet
	all.AddRange(1, sel.NumMessages)
	msgs, err := c.Fetch(all, &imap.FetchOptions{
		UID: true, Envelope: true, Flags: true, RFC822Size: true,
		BodySection: []*imap.FetchItemBodySection{sect},
	}).Collect()
	if err != nil {
		log.Fatal(err)
	}
	for _, m := range msgs {
		name := filepath.Join(dir, fmt.Sprintf("%d.eml", m.UID))
		if err := os.WriteFile(name, m.FindBodySection(sect), 0o600); err != nil {
			log.Fatal(err)
		}
		from := ""
		if len(m.Envelope.From) > 0 {
			f := m.Envelope.From[0]
			from = fmt.Sprintf("%s <%s@%s>", f.Name, f.Mailbox, f.Host)
		}
		fmt.Printf("  uid=%d %s | %s | %q | %d bytes | flags=%v\n",
			m.UID, m.Envelope.Date.Format("2006-01-02 15:04"), from, m.Envelope.Subject, m.RFC822Size, m.Flags)
	}
}
