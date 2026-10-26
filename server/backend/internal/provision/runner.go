package provision

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

// Job kinds (provision_jobs.kind), job statuses (provision_jobs.status) and step statuses
// (provision_steps.status).
const (
	KindProvision   = "provision"
	KindReprovision = "reprovision"
	KindDeprovision = "deprovision"
	KindVerify      = "verify"
	// KindMTProxy (re)installs an online exit's MTProxy as a job of its own: «Переустановить
	// Telegram-прокси» and the reconciler's backfill share the node's job lock (one active job per
	// node) with every other job instead of running beside them.
	KindMTProxy = "mtproxy"

	JobRunning     = "running"
	JobSucceeded   = "succeeded"
	JobFailed      = "failed"
	JobInterrupted = "interrupted"

	StepPending     = "pending"
	StepRunning     = "running"
	StepOK          = "ok"   // Verify passed, nothing touched
	StepDone        = "done" // applied and verified
	StepSkipped     = "skipped"
	StepDrift       = "drift" // verify job: the node differs, nothing touched
	StepFailed      = "failed"
	StepRolledBack  = "rolled_back"
	StepInterrupted = "interrupted" // the API restarted while the step ran

	MaxOutputTail = 8 << 10
	JobTimeout    = 40 * time.Minute // first MTProxy build alone may take 12 min
	// RollbackTimeout: a rollback gets its own time — the job's context may be what just expired.
	RollbackTimeout = 2 * time.Minute
	// StaleSweepEvery: how often the API's SweepStale frees nodes of dead CLI runs.
	StaleSweepEvery = 5 * time.Minute
)

var (
	ErrJobActive       = errors.New("для этой ноды уже выполняется задача")
	ErrJobNotResumable = errors.New("задачу нельзя продолжить: продолжить можно только последнюю задачу ноды, если она прервана или завершилась ошибкой")
	ErrNodeUnreachable = errors.New("нода недоступна по SSH")
	// ErrJobLost: this run no longer owns the job (it was interrupted, then resumed by another
	// run); the run stops without touching the job or the node's lifecycle.
	ErrJobLost = errors.New("задача больше не принадлежит этому запуску: её прервали или продолжили заново")
)

// RefusalError: a job was refused before it existed, for a reason the owner can act on (the node's
// state, the Hub's settings or assets); its text is meant for the admin panel. Any other error of
// Start / Resume (database, network) is internal and is not shown as is.
type RefusalError struct{ Err error }

func (e *RefusalError) Error() string { return e.Err.Error() }
func (e *RefusalError) Unwrap() error { return e.Err }

// Refusal marks err as a RefusalError (nil stays nil; sentinels inside stay visible to errors.Is).
func Refusal(err error) error {
	if err == nil {
		return nil
	}
	var re *RefusalError
	if errors.As(err, &re) {
		return err
	}
	return &RefusalError{Err: err}
}

// IsRefusal reports whether err is a RefusalError.
func IsRefusal(err error) bool {
	var re *RefusalError
	return errors.As(err, &re)
}

// jobTimeout is JobTimeout (tests shorten it).
var jobTimeout = JobTimeout

// Job is the part of a provision_jobs row a run needs. Token is the run's ownership of the job
// (provision_jobs.run_token), fresh on every start and resume.
type Job struct {
	ID       int64
	ServerID int
	Kind     string
	Status   string
	Token    string
}

// Store persists jobs (SQLStore; an in-memory one in tests).
type Store interface {
	LoadNode(ctx context.Context, serverID int) (Node, error)
	// CreateJob inserts a running job under a fresh run token; ErrJobActive.
	CreateJob(ctx context.Context, serverID int, kind string) (Job, error)
	// ReopenJob puts the node's latest job back to running under a fresh run token if it was
	// interrupted or failed, or is a CLI run too old to be alive; ErrJobNotResumable / ErrJobActive.
	ReopenJob(ctx context.Context, jobID int64) (Job, error)
	// SetSteps, MarkStep and FinishJob write only while job.Token owns the running job; otherwise
	// ErrJobLost.
	SetSteps(ctx context.Context, job Job, names []string) error
	MarkStep(ctx context.Context, job Job, idx int, status, outputTail string) error
	FinishJob(ctx context.Context, job Job, status, errMsg string) error
	// InterruptRunning: at API start, every job a run of the old process owned (and stale CLI runs).
	InterruptRunning(ctx context.Context) (int64, error)
	// InterruptStale: the jobs of CLI runs too old to be alive (Ctrl-C, a dropped DB tunnel).
	InterruptStale(ctx context.Context) (int64, error)
}

// Runner runs jobs: one goroutine per job, steps in order, every state change persisted, so a
// lost SSH connection leaves a failed job and an API restart an interrupted one (InterruptRunning
// at startup) — both resumable from the first step.
type Runner struct {
	Store  Store
	Dial   func(Node) (Remote, error) // DialNode
	Recipe func(ctx context.Context, n Node, kind string) (Recipe, *State, error)
	Go     func(func()) // nil: go f()
	Logf   func(format string, a ...any)
}

// ValidKind reports whether k is a job kind.
func ValidKind(k string) bool {
	switch k {
	case KindProvision, KindReprovision, KindDeprovision, KindVerify, KindMTProxy:
		return true
	}
	return false
}

// Start creates the job (one active job per node, enforced by the database) and runs it in the background.
func (r *Runner) Start(ctx context.Context, serverID int, kind string) (int64, error) {
	if !ValidKind(kind) {
		return 0, Refusal(fmt.Errorf("неизвестный вид задачи %q", kind))
	}
	n, err := r.Store.LoadNode(ctx, serverID)
	if err != nil {
		return 0, err
	}
	recipe, st, err := r.Recipe(ctx, n, kind)
	if err != nil {
		return 0, err
	}
	job, err := r.Store.CreateJob(ctx, serverID, kind)
	if err != nil {
		return 0, err
	}
	if err := r.Store.SetSteps(ctx, job, stepNames(recipe)); err != nil {
		_ = r.finish(job, JobFailed, err.Error())
		return job.ID, err
	}
	r.spawn(func() { r.run(job, n, recipe, st) })
	return job.ID, nil
}

// Resume re-runs an interrupted or failed job from its first step (steps are idempotent: the
// converged ones only verify).
func (r *Runner) Resume(ctx context.Context, jobID int64) error {
	job, err := r.Store.ReopenJob(ctx, jobID)
	if err != nil {
		return err
	}
	fail := func(err error) error { _ = r.finish(job, JobFailed, err.Error()); return err }
	n, err := r.Store.LoadNode(ctx, job.ServerID)
	if err != nil {
		return fail(err)
	}
	recipe, st, err := r.Recipe(ctx, n, job.Kind)
	if err != nil {
		return fail(err)
	}
	if err := r.Store.SetSteps(ctx, job, stepNames(recipe)); err != nil {
		return fail(err)
	}
	r.spawn(func() { r.run(job, n, recipe, st) })
	return nil
}

// SweepStale interrupts, every `every` until ctx ends, the jobs of CLI runs that cannot be alive
// any more, so their nodes take new jobs and «Продолжить» without waiting for an API restart.
// The API runs it once in the background (Task 2.10: go runner.SweepStale(ctx, StaleSweepEvery)).
func (r *Runner) SweepStale(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		n, err := r.Store.InterruptStale(sctx)
		cancel()
		switch {
		case err != nil:
			r.logf("[provision] зависшие задачи командной строки: %v", err)
		case n > 0:
			r.logf("[provision] прервано зависших задач командной строки: %d — их можно продолжить", n)
		}
	}
}

func stepNames(rc Recipe) []string {
	names := make([]string, len(rc))
	for i, s := range rc {
		names[i] = s.Name
	}
	return names
}

func (r *Runner) spawn(f func()) {
	if r.Go != nil {
		r.Go(f)
		return
	}
	go f()
}

func (r *Runner) logf(format string, a ...any) {
	if r.Logf != nil {
		r.Logf(format, a...)
	}
}

// Bookkeeping writes get their own short context (they must land even when the job's expired)
// and a few attempts: a final status lost to a database blip would leave the job "running" — the
// node locked for new jobs and not resumable — until the API restarts. ErrJobLost is final.
const bookkeepingAttempts = 3

var bookkeepingBackoff = time.Second

func (r *Runner) bookkeep(what string, f func(ctx context.Context) error) error {
	var err error
	for i := range bookkeepingAttempts {
		if i > 0 {
			time.Sleep(time.Duration(i) * bookkeepingBackoff)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err = f(ctx)
		cancel()
		if err == nil || errors.Is(err, ErrJobLost) {
			return err
		}
	}
	r.logf("[provision] %s: %v", what, err)
	return err
}

func (r *Runner) finish(job Job, status, msg string) error {
	return r.bookkeep(fmt.Sprintf("job %d finish", job.ID), func(ctx context.Context) error {
		return r.Store.FinishJob(ctx, job, status, msg)
	})
}

func (r *Runner) mark(job Job, idx int, status, out string) error {
	return r.bookkeep(fmt.Sprintf("job %d step %d", job.ID, idx), func(ctx context.Context) error {
		return r.Store.MarkStep(ctx, job, idx, status, out)
	})
}

// secretSet is every secret a run has seen: a value rotated by a step (the tunnel password, keys)
// stays masked in later output.
type secretSet struct {
	seen map[string]bool
	list []string
}

func (s *secretSet) add(v ...string) {
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	for _, x := range v {
		if !s.seen[x] {
			s.seen[x] = true
			s.list = append(s.list, x)
		}
	}
}

func (r *Runner) run(job Job, n Node, recipe Recipe, st *State) {
	ctx, cancel := context.WithTimeout(context.Background(), jobTimeout)
	defer cancel()
	node := &lazyRemote{dial: func() (Remote, error) { return r.Dial(n) }}
	defer node.close()
	st.logf = r.logf
	var secrets secretSet
	secrets.add(st.Secrets()...)
	logJob := func(msg string) { r.logf("[provision] job %d (node %d, %s): %s", job.ID, n.ID, st.Kind, msg) }
	lost := func(err error) bool {
		if errors.Is(err, ErrJobLost) {
			logJob("задача перешла к другому запуску — этот запуск остановлен")
			return true
		}
		return false
	}
	// failJob: the step's own status is already stored (so this run still owned the job).
	failJob := func(msg string) {
		if st.Fail != nil {
			fctx, fcancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err := protect(func() error { st.Fail(fctx); return nil }); err != nil {
				r.logPanic(job.ID, "fail", err, secrets.list)
			}
			fcancel()
		}
		msg = redact(msg, secrets.list)
		if !lost(r.finish(job, JobFailed, msg)) {
			logJob(msg)
		}
	}
	if st.Begin != nil {
		if err := protect(func() error { return st.Begin(ctx) }); err != nil {
			r.logPanic(job.ID, "begin", err, secrets.list)
			msg := redact(err.Error(), secrets.list)
			if !lost(r.finish(job, JobFailed, msg)) {
				logJob(msg)
			}
			return
		}
	}
	for i, step := range recipe {
		secrets.add(st.Secrets()...)
		if ctx.Err() != nil { // out of time: no Verify→Apply on a node the job no longer watches
			msg := fmt.Sprintf("время задачи (%s) истекло до шага «%s»", jobTimeout, Title(step.Name))
			if lost(r.mark(job, i, StepFailed, msg)) {
				return
			}
			failJob(msg)
			return
		}
		buf := &tailBuffer{max: 8 * MaxOutputTail}
		st.out = buf
		if lost(r.mark(job, i, StepRunning, "")) {
			st.out = nil
			return
		}
		status, err := runStep(ctx, st.Kind, step, node, st)
		st.out = nil
		secrets.add(st.Secrets()...) // keys the step learnt are secrets too
		text := buf.String()
		if err != nil {
			text += err.Error()
		}
		out := redact(tail(redact(strings.TrimSpace(text), secrets.list), MaxOutputTail), secrets.list)
		if lost(r.mark(job, i, status, out)) {
			return
		}
		if status == StepFailed || status == StepRolledBack {
			r.logPanic(job.ID, step.Name, err, secrets.list)
			failJob(fmt.Sprintf("шаг «%s»: %v", Title(step.Name), err))
			return
		}
	}
	if !lost(r.finish(job, JobSucceeded, "")) {
		logJob("готово")
	}
}

// panicError is a recipe callback that panicked: the job fails at that step (rollback included)
// instead of the API going down — and again on every «Продолжить».
type panicError struct {
	val   any
	stack []byte
}

func (p *panicError) Error() string {
	return fmt.Sprintf("внутренняя ошибка: %v", p.val)
}

// protect runs f, turning a panic into a *panicError.
func protect(f func() error) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = &panicError{val: v, stack: debug.Stack()}
		}
	}()
	return f()
}

// logPanic logs the stack of a panic found in err (the value redacted; a stack trace prints
// pointers, not string contents).
func (r *Runner) logPanic(jobID int64, where string, err error, secrets []string) {
	var pe *panicError
	if errors.As(err, &pe) {
		r.logf("[provision] job %d %s: %s\n%s", jobID, where, redact(pe.Error(), secrets), pe.stack)
	}
}

// inconclusive: a Verify error that says nothing about the node — Verify panicked, the node
// could not be reached or the connection dropped, the job ran out of time, a check from outside
// could not run at all (ErrProbeUnavailable). Neither drift nor a reason to Apply.
//
// Recipes: a Verify that times out on its own (a sub-context of its own, the node reachable) is
// caught here too when it returns or wraps context.DeadlineExceeded. If that case should lead to
// Apply (e.g. "the service did not answer in 5 s" = not set up yet), return a recipe error that
// does not wrap the context error.
func inconclusive(ctx context.Context, err error) bool {
	var pe *panicError
	return errors.As(err, &pe) || ctx.Err() != nil ||
		errors.Is(err, ErrNodeUnreachable) || errors.Is(err, ErrConnectionLost) || errors.Is(err, ErrProbeUnavailable) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

type stepCall func(ctx context.Context, f func(context.Context, Remote, *State) error) error

// runStep: verify → (apply → verify) with rollback on failure; verify jobs never apply.
func runStep(ctx context.Context, kind string, s Step, r Remote, st *State) (string, error) {
	call := func(ctx context.Context, f func(context.Context, Remote, *State) error) error {
		return protect(func() error { return f(ctx, r, st) })
	}
	if kind == KindVerify {
		if s.Verify == nil {
			st.Printf("в режиме проверки этот шаг не выполняется")
			return StepSkipped, nil
		}
		if err := call(ctx, s.Verify); err != nil {
			if inconclusive(ctx, err) {
				return StepFailed, err
			}
			st.Printf("расхождение: %v", err)
			return StepDrift, nil
		}
		return StepOK, nil
	}
	if s.Verify != nil {
		verr := call(ctx, s.Verify)
		if verr == nil {
			return StepOK, nil
		}
		if s.Apply == nil || inconclusive(ctx, verr) {
			return StepFailed, verr
		}
		st.Printf("нужна настройка: %v", verr)
	}
	if s.Apply == nil {
		return StepOK, nil
	}
	if err := call(ctx, s.Apply); err != nil {
		switch {
		case errors.Is(err, ErrSkipped):
			return StepSkipped, nil
		case errors.Is(err, ErrNoRollback):
			return StepFailed, err
		}
		return rollback(call, s, st, err)
	}
	if s.Verify != nil {
		if err := call(ctx, s.Verify); err != nil {
			return rollback(call, s, st, fmt.Errorf("после настройки проверка не прошла: %w", err))
		}
	}
	return StepDone, nil
}

func rollback(call stepCall, s Step, st *State, cause error) (string, error) {
	if s.Rollback == nil {
		return StepFailed, cause
	}
	rctx, cancel := context.WithTimeout(context.Background(), RollbackTimeout)
	defer cancel()
	if err := call(rctx, s.Rollback); err != nil {
		return StepFailed, fmt.Errorf("%w; откат не удался: %w", cause, err)
	}
	st.Printf("изменения шага откачены")
	return StepRolledBack, cause
}

// lazyRemote dials the node on first use, so Hub-only steps (and deprovision of a dead VPS)
// work without it; a failed dial is ErrNodeUnreachable for the rest of the run.
type lazyRemote struct {
	dial func() (Remote, error)
	once sync.Once
	r    Remote
	err  error
}

func (l *lazyRemote) get() (Remote, error) {
	l.once.Do(func() {
		l.r, l.err = l.dial()
		if l.err != nil {
			l.err = fmt.Errorf("%w: %w", ErrNodeUnreachable, l.err)
		}
	})
	return l.r, l.err
}

func (l *lazyRemote) Run(ctx context.Context, s string) (string, error) {
	r, err := l.get()
	if err != nil {
		return "", err
	}
	return r.Run(ctx, s)
}

func (l *lazyRemote) PutFile(ctx context.Context, p string, d []byte, m uint32) (bool, error) {
	r, err := l.get()
	if err != nil {
		return false, err
	}
	return r.PutFile(ctx, p, d, m)
}

func (l *lazyRemote) close() {
	if l.r != nil {
		Close(l.r)
	}
}
