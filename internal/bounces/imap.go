package bounces

// The IMAP side of the bounce mailbox (github.com/emersion/go-imap): connect through the
// SSRF-guarded dialer, sign in, select the folder, list unseen messages, fetch them without
// marking them read, and flag them \Seen once recorded.

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"

	"pulse/internal/safehttp"
)

const maxMessageBytes = 10 << 20

// IMAPOpener returns an Opener for real servers. allowPrivate lets it reach private addresses
// (local development); otherwise only public addresses are dialled.
func IMAPOpener(allowPrivate bool) Opener {
	return func(ctx context.Context, m Mailbox, password string) (Session, error) {
		d := safehttp.Dialer(allowPrivate)
		d.Timeout = 15 * time.Second
		addr := net.JoinHostPort(m.Host, strconv.Itoa(m.Port))
		tc := &tls.Config{ServerName: m.Host, MinVersion: tls.VersionTLS12}
		var c *client.Client
		var err error
		if m.TLS == TLSImplicit {
			c, err = client.DialWithDialerTLS(d, addr, tc)
		} else {
			c, err = client.DialWithDialer(d, addr)
		}
		if err != nil {
			if errors.Is(err, safehttp.ErrBlocked) {
				return nil, fmt.Errorf("can't connect to %s: it isn't a public address", m.Host)
			}
			return nil, fmt.Errorf("can't connect to %s: %w", addr, err)
		}
		c.Timeout = time.Minute
		if dl, ok := ctx.Deadline(); ok {
			c.Timeout = time.Until(dl)
		}
		ok := false
		defer func() {
			if !ok {
				_ = c.Logout()
			}
		}()
		if m.TLS == TLSStart {
			if can, _ := c.SupportStartTLS(); !can {
				return nil, errors.New("the server doesn't offer STARTTLS: use TLS on port 993")
			}
			if err := c.StartTLS(tc); err != nil {
				return nil, fmt.Errorf("STARTTLS failed: %w", err)
			}
		}
		if err := c.Login(m.Username, password); err != nil {
			return nil, fmt.Errorf("sign-in refused: %w", err)
		}
		if _, err := c.Select(m.Folder, false); err != nil {
			return nil, fmt.Errorf("can't open folder %q: %w", m.Folder, err)
		}
		ok = true
		return &imapSession{c: c}, nil
	}
}

type imapSession struct{ c *client.Client }

func (s *imapSession) Unseen(ctx context.Context, max int) ([]Message, error) {
	crit := imap.NewSearchCriteria()
	crit.WithoutFlags = []string{imap.SeenFlag, imap.DeletedFlag}
	uids, err := s.c.UidSearch(crit)
	if err != nil || len(uids) == 0 {
		return nil, err
	}
	if len(uids) > max {
		uids = uids[:max] // UIDs ascend: the oldest first
	}
	set := new(imap.SeqSet)
	set.AddNum(uids...)
	section := &imap.BodySectionName{Peek: true}
	ch := make(chan *imap.Message, 16)
	done := make(chan error, 1)
	go func() {
		done <- s.c.UidFetch(set, []imap.FetchItem{imap.FetchUid, imap.FetchRFC822Size, section.FetchItem()}, ch)
	}()
	var out []Message
	for msg := range ch {
		if msg.Size > maxMessageBytes {
			out = append(out, Message{UID: msg.Uid}) // too big to be a report: skipped, marked seen
			continue
		}
		var raw []byte
		if r := msg.GetBody(section); r != nil {
			raw, _ = io.ReadAll(io.LimitReader(r, maxMessageBytes))
		}
		out = append(out, Message{UID: msg.Uid, Raw: raw})
	}
	if err := <-done; err != nil {
		return nil, err
	}
	return out, ctx.Err()
}

func (s *imapSession) MarkSeen(_ context.Context, uids []uint32) error {
	if len(uids) == 0 {
		return nil
	}
	set := new(imap.SeqSet)
	set.AddNum(uids...)
	return s.c.UidStore(set, imap.FormatFlagsOp(imap.AddFlags, true), []any{imap.SeenFlag}, nil)
}

func (s *imapSession) Close() error { return s.c.Logout() }
