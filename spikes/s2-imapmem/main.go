// Spike S2: what does go-imap v2's imapmemserver support, as far as Ivy's sync needs?
// Throwaway code; findings go in docs/spikes/s2-imapmem.md.
package main

import (
	"fmt"
	"log"
	"net"
	"sort"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

func main() {
	mem := imapmemserver.New()
	user := imapmemserver.NewUser("u", "p")
	mem.AddUser(user)
	if err := user.Create("INBOX", nil); err != nil {
		log.Fatal(err)
	}
	if err := user.Create("Archive", nil); err != nil {
		log.Fatal(err)
	}

	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		InsecureAuth: true,
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	go srv.Serve(ln)
	defer srv.Close()

	unsolicited := make(chan string, 16)
	c, err := imapclient.DialInsecure(ln.Addr().String(), &imapclient.Options{
		UnilateralDataHandler: &imapclient.UnilateralDataHandler{
			Mailbox: func(d *imapclient.UnilateralDataMailbox) {
				if d.NumMessages != nil {
					unsolicited <- fmt.Sprintf("EXISTS %d", *d.NumMessages)
				}
			},
			Expunge: func(seq uint32) { unsolicited <- fmt.Sprintf("EXPUNGE %d", seq) },
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()
	if err := c.Login("u", "p").Wait(); err != nil {
		log.Fatal(err)
	}

	report := map[string]string{}
	set := func(k string, err error) {
		if err != nil {
			report[k] = "FAIL: " + err.Error()
		} else {
			report[k] = "ok"
		}
	}

	caps, err := c.Capability().Wait()
	set("CAPABILITY", err)
	var names []string
	for k := range caps {
		names = append(names, string(k))
	}
	sort.Strings(names)
	fmt.Println("advertised:", names)

	for i := 0; i < 3; i++ {
		msg := fmt.Sprintf("From: a@example.com\r\nSubject: m%d\r\n\r\nbody %d\r\n", i, i)
		cmd := c.Append("INBOX", int64(len(msg)), nil)
		_, _ = cmd.Write([]byte(msg))
		_ = cmd.Close()
		_, err := cmd.Wait()
		set(fmt.Sprintf("APPEND %d", i), err)
	}

	// CONDSTORE: SELECT (CONDSTORE) should return HIGHESTMODSEQ; FETCH should carry MODSEQ.
	sel, err := c.Select("INBOX", &imap.SelectOptions{CondStore: true}).Wait()
	set("SELECT (CONDSTORE)", err)
	if err == nil {
		fmt.Printf("select: exists=%d uidnext=%d uidvalidity=%d highestmodseq=%d\n",
			sel.NumMessages, sel.UIDNext, sel.UIDValidity, sel.HighestModSeq)
		if sel.HighestModSeq == 0 {
			report["CONDSTORE HIGHESTMODSEQ"] = "MISSING (0)"
		} else {
			report["CONDSTORE HIGHESTMODSEQ"] = "ok"
		}
	}

	if err != nil {
		// The rest of the probe needs a selected mailbox even when CONDSTORE is refused.
		if _, err := c.Select("INBOX", nil).Wait(); err != nil {
			log.Fatal(err)
		}
	}

	fetchOpts := &imap.FetchOptions{UID: true, Flags: true, ModSeq: true}
	msgs, err := c.Fetch(imap.UIDSetNum(1, 2, 3), fetchOpts).Collect()
	set("FETCH (UID FLAGS MODSEQ)", err)
	if err == nil && len(msgs) > 0 && msgs[0].ModSeq == 0 {
		report["FETCH MODSEQ value"] = "MISSING (0)"
	}

	_, err = c.Fetch(imap.UIDSetNum(1), &imap.FetchOptions{
		ChangedSince: 1,
		UID:          true,
		Flags:        true,
	}).Collect()
	set("FETCH (CHANGEDSINCE)", err)

	set("UID STORE +FLAGS \\Seen", c.Store(imap.UIDSetNum(1), &imap.StoreFlags{
		Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagSeen},
	}, nil).Close())

	// Keywords (tags): can the server store arbitrary flags?
	set("UID STORE +FLAGS $ivy-tag", c.Store(imap.UIDSetNum(2), &imap.StoreFlags{
		Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{"$ivy-tag"},
	}, nil).Close())

	// MOVE (RFC 6851): must be a real atomic MOVE with COPYUID.
	mv, err := c.Move(imap.UIDSetNum(3), "Archive").Wait()
	set("UID MOVE", err)
	if err == nil && mv != nil {
		fmt.Printf("move: uidvalidity=%d src=%v dst=%v\n", mv.UIDValidity, mv.SourceUIDs, mv.DestUIDs)
	}

	// IDLE: another connection appends, this one should see EXISTS.
	idle, err := c.Idle()
	set("IDLE start", err)
	if err == nil {
		go func() {
			time.Sleep(200 * time.Millisecond)
			msg := "From: b@example.com\r\nSubject: idle\r\n\r\nx\r\n"
			c2, err := imapclient.DialInsecure(ln.Addr().String(), nil)
			if err != nil {
				return
			}
			defer c2.Close()
			_ = c2.Login("u", "p").Wait()
			cmd := c2.Append("INBOX", int64(len(msg)), nil)
			_, _ = cmd.Write([]byte(msg))
			_ = cmd.Close()
			_, _ = cmd.Wait()
		}()
		select {
		case ev := <-unsolicited:
			report["IDLE notification"] = "ok (" + ev + ")"
		case <-time.After(2 * time.Second):
			report["IDLE notification"] = "FAIL: nothing within 2s"
		}
		set("IDLE stop", idle.Close())
	}

	// UID EXPUNGE (UIDPLUS): must expunge only the named UIDs.
	_ = c.Store(imap.UIDSetNum(1), &imap.StoreFlags{
		Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted},
	}, nil).Close()
	_, err = c.UIDExpunge(imap.UIDSetNum(1)).Collect()
	set("UID EXPUNGE", err)

	keys := make([]string, 0, len(report))
	for k := range report {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Println()
	for _, k := range keys {
		fmt.Printf("%-32s %s\n", k, report[k])
	}
}
