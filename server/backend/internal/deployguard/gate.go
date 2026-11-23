package deployguard

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"time"
)

// ErrRefused: the guard refused the deploy (the tools exit with code 2).
var ErrRefused = errors.New("выкатка остановлена защитой")

// Gate is one deploy's hold on the Hub: Open checked it and took the lock; Done records the
// deployed build and releases the lock; Release frees the lock on any other way out.
type Gate struct {
	run       Run
	component string
	doc       []byte
	locked    bool
}

// Holder names who holds the lock: user@host and the time, for the next deploy's refusal.
func Holder(now time.Time) string {
	who := "unknown"
	if u, err := user.Current(); err == nil {
		who = u.Username
	}
	host, _ := os.Hostname()
	return fmt.Sprintf("%s@%s %s", who, host, now.UTC().Format(time.RFC3339))
}

// Open reads the Hub's side, checks the deploy of l (component: api | admin | site; a --setup-*
// run checks against api and records nothing), prints the verdict to out and takes the lock.
// force is the --force-deploy reason ("" — none).
func Open(run Run, component, holder string, l Local, force string, now time.Time, out io.Writer) (*Gate, error) {
	raw, err := run(ReadRemoteCmd(), nil)
	if err != nil && raw == "" {
		return nil, fmt.Errorf("состояние выкатки на хабе не прочитано: %w", err)
	}
	r, doc := ParseRemote(component, raw, now)
	v := Check(l, r)
	if force != "" {
		if v, err = v.Force(force); err != nil {
			fmt.Fprintln(out, "    ОТКАЗ: "+err.Error())
			return nil, ErrRefused
		}
	}
	for _, w := range v.Warnings {
		fmt.Fprintln(out, "    ВНИМАНИЕ: "+w)
	}
	if len(v.Refusals) > 0 {
		for _, rf := range v.Refusals {
			fmt.Fprintln(out, "    ОТКАЗ: "+rf)
		}
		fmt.Fprintln(out, `    Обход — только осознанно: --force-deploy "<причина, не короче 10 символов>" (причина уйдёт в ленту)`)
		return nil, ErrRefused
	}
	takeOver := r.LockHolder != "" && (r.LockAge >= StaleLock || force != "")
	if err := Lock(run, holder, takeOver); err != nil {
		fmt.Fprintln(out, "    ОТКАЗ: "+err.Error())
		return nil, ErrRefused
	}
	if r.DeployedSHA != "" {
		fmt.Fprintf(out, "    Защита: выкачено %s, выкатывается %s — ок, замок взят\n", r.DeployedSHA, l.SHA)
	} else {
		fmt.Fprintf(out, "    Защита: выкатывается %s, замок взят\n", l.SHA)
	}
	return &Gate{run: run, component: component, doc: doc, locked: true}, nil
}

// Done records sha as the deployed build of the gate's component and releases the lock.
func (g *Gate) Done(sha string, now time.Time) error {
	if g == nil {
		return nil
	}
	err := Record(g.run, g.doc, g.component, sha, now)
	g.Release()
	return err
}

// Release frees the lock (safe to call more than once).
func (g *Gate) Release() {
	if g == nil || !g.locked {
		return
	}
	if Unlock(g.run) == nil {
		g.locked = false
	}
}
