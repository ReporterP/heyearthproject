package hubnet

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/netip"
	"net/smtp"
	"strconv"
	"time"
)

// DialFunc dials like net.Dialer.DialContext.
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// BoundDialer dials from bind: the probes that prove a new address works before anything points
// at it (connect from it, the kernel picks a fresh source port each time).
func BoundDialer(bind netip.Addr, timeout time.Duration) DialFunc {
	d := &net.Dialer{Timeout: timeout, LocalAddr: &net.TCPAddr{IP: bind.AsSlice()}}
	return d.DialContext
}

// Verdicts of one outbound target.
const (
	VerdictOK            = "ok"
	VerdictFail          = "fail"
	VerdictNotIndicative = "not_indicative" // fails from the current address too: says nothing about the new one
)

// TargetResult is one row of the outbound table: successes from the current and the new address.
type TargetResult struct {
	Target  string `json:"target"`
	Cur     int    `json:"cur"`
	New     int    `json:"new"`
	Tries   int    `json:"tries"`
	Verdict string `json:"verdict"`
}

// OutboundVerdict: the new address passes a target with at least one success and no more than
// 20 % of tries worse than the current address; a target the current address cannot reach
// either is not indicative unless the new one reaches it.
func OutboundVerdict(cur, new, tries int) string {
	switch {
	case new == 0 && cur == 0:
		return VerdictNotIndicative
	case new >= 1 && float64(new) >= float64(cur)-0.2*float64(tries):
		return VerdictOK
	}
	return VerdictFail
}

// ProbeTCP connects to target tries times and returns how many connects succeeded.
func ProbeTCP(ctx context.Context, dial DialFunc, target string, tries int) int {
	ok := 0
	for i := 0; i < tries && ctx.Err() == nil; i++ {
		c, err := dial(ctx, "tcp", target)
		if err == nil {
			ok++
			c.Close()
		}
	}
	return ok
}

// ProbeSMTPAuth opens an SMTP session through dial up to a successful AUTH (no message is
// sent): implicit TLS on 465, STARTTLS when the server offers it — the same policy as the
// mail client. A refusal carries the server's reply.
func ProbeSMTPAuth(ctx context.Context, dial DialFunc, host string, port int, user, pass string) error {
	conn, err := dial(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("SMTP %s:%d: %w", host, port, err)
	}
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	} else {
		conn.SetDeadline(time.Now().Add(20 * time.Second))
	}
	if port == 465 {
		conn = tls.Client(conn, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("SMTP приветствие: %w", err)
	}
	defer c.Close()
	if err := c.Hello("hub-address-check"); err != nil {
		return fmt.Errorf("SMTP EHLO: %w", err)
	}
	if ok, _ := c.Extension("STARTTLS"); ok && port != 465 {
		if err := c.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("SMTP STARTTLS: %w", err)
		}
	}
	if err := c.Auth(smtp.PlainAuth("", user, pass, host)); err != nil {
		return fmt.Errorf("SMTP AUTH: %w", err)
	}
	return c.Quit()
}
