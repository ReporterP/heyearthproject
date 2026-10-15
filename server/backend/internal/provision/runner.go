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
)

var (
	ErrJobActive       = errors.New("для этой ноды уже выполняется задача")
	ErrJobNotResumable = errors.New("задачу нельзя продолжить: она не прервана и не завершилась ошибкой")
	ErrNodeUnreachable = errors.New("нода недоступна по SSH")
)

// Job is the part of a provision_jobs row the runner needs to (re)start it.
type Job struct {
	ID       int64
	ServerID int
	Kind     string
	Status   string
}

// Store persists jobs (SQLStore; an in-memory one in tests).
type Store interface {
	LoadNode(ctx context.Context, serverID int) (Node, error)
	CreateJob(ctx context.Context, serverID int, kind string) (int64, error) // ErrJobActive
	ReopenJob(ctx context.Context, jobID int64) (Job, error)                 // ErrJobActive / ErrJobNotResumable
	SetSteps(ctx context.Context, jobID int64, names []string) error
	MarkStep(ctx context.Context, jobID int64, idx int, status, outputTail string) error
	FinishJob(ctx context.Context, jobID int64, status, errMsg string) error
	InterruptRunning(ctx context.Context) (int64, error)
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
	case KindProvision, KindReprovision, KindDeprovision, KindVerify:
		return true
	}
	return false
}

// Start creates the job (one active job per node, enforced by the database) and runs it in the background.
func (r *Runner) Start(ctx context.Context, serverID int, kind string) (int64, error) {
	if !ValidKind(kind) {
		return 0, fmt.Errorf("неизвестный вид задачи %q", kind)
	}
	n, err := r.Store.LoadNode(ctx, serverID)
	if err != nil {
		return 0, err
	}
	recipe, st, err := r.Recipe(ctx, n, kind)
	if err != nil {
		return 0, err
	}
	id, err := r.Store.CreateJob(ctx, serverID, kind)
	if err != nil {
		return 0, err
	}
	if err := r.Store.SetSteps(ctx, id, stepNames(recipe)); err != nil {
		r.finish(id, JobFailed, err.Error())
		return id, err
	}
	r.spawn(func() { r.run(id, n, recipe, st) })
	return id, nil
}

// Resume re-runs an interrupted or failed job from its first step (steps are idempotent: the
// converged ones only verify).
func (r *Runner) Resume(ctx context.Context, jobID int64) error {
	job, err := r.Store.ReopenJob(ctx, jobID)
	if err != nil {
		return err
	}
	fail := func(err error) error { r.finish(jobID, JobFailed, err.Error()); return err }
	n, err := r.Store.LoadNode(ctx, job.ServerID)
	if err != nil {
		return fail(err)
	}
	recipe, st, err := r.Recipe(ctx, n, job.Kind)
	if err != nil {
		return fail(err)
	}
	if err := r.Store.SetSteps(ctx, jobID, stepNames(recipe)); err != nil {
		return fail(err)
	}
	r.spawn(func() { r.run(jobID, n, recipe, st) })
	return nil
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
// node locked for new jobs and not resumable — until the API restarts.
const bookkeepingAttempts = 3

var bookkeepingBackoff = time.Second

func (r *Runner) bookkeep(what string, f func(ctx context.Context) error) {
	var err error
	for i := range bookkeepingAttempts {
		if i > 0 {
			time.Sleep(time.Duration(i) * bookkeepingBackoff)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err = f(ctx)
		cancel()
		if err == nil {
			return
		}
	}
	r.logf("[provision] %s: %v", what, err)
}

func (r *Runner) finish(id int64, status, msg string) {
	r.bookkeep(fmt.Sprintf("job %d finish", id), func(ctx context.Context) error {
		return r.Store.FinishJob(ctx, id, status, msg)
	})
}

func (r *Runner) mark(id int64, idx int, status, out string) {
	r.bookkeep(fmt.Sprintf("job %d step %d", id, idx), func(ctx context.Context) error {
		return r.Store.MarkStep(ctx, id, idx, status, out)
	})
}

func (r *Runner) run(jobID int64, n Node, recipe Recipe, st *State) {
	ctx, cancel := context.WithTimeout(context.Background(), JobTimeout)
	defer cancel()
	node := &lazyRemote{dial: func() (Remote, error) { return r.Dial(n) }}
	defer node.close()
	if st.Begin != nil {
		if err := protect(func() error { return st.Begin(ctx) }); err != nil {
			r.logPanic(jobID, "begin", err, st.Secrets())
			msg := redact(err.Error(), st.Secrets())
			r.finish(jobID, JobFailed, msg)
			r.logf("[provision] job %d (node %d, %s): %s", jobID, n.ID, st.Kind, msg)
			return
		}
	}
	for i, step := range recipe {
		buf := &tailBuffer{max: 8 * MaxOutputTail}
		st.out = buf
		r.mark(jobID, i, StepRunning, "")
		status, err := runStep(ctx, st.Kind, step, node, st)
		st.out = nil
		text := buf.String()
		if err != nil {
			text += err.Error()
		}
		secrets := st.Secrets() // after the step: keys it learnt are secrets too
		r.mark(jobID, i, status, tail(redact(strings.TrimSpace(text), secrets), MaxOutputTail))
		if status == StepFailed || status == StepRolledBack {
			r.logPanic(jobID, step.Name, err, secrets)
			if st.Fail != nil {
				fctx, fcancel := context.WithTimeout(context.Background(), 10*time.Second)
				if ferr := protect(func() error { st.Fail(fctx); return nil }); ferr != nil {
					r.logPanic(jobID, "fail", ferr, secrets)
				}
				fcancel()
			}
			msg := redact(fmt.Sprintf("шаг «%s»: %v", Title(step.Name), err), secrets)
			r.finish(jobID, JobFailed, msg)
			r.logf("[provision] job %d (node %d, %s): %s", jobID, n.ID, st.Kind, msg)
			return
		}
	}
	r.finish(jobID, JobSucceeded, "")
	r.logf("[provision] job %d (node %d, %s): готово", jobID, n.ID, st.Kind)
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

// runStep: verify → (apply → verify) with rollback on failure; verify jobs never apply. A
// panicking Verify is a failed step, never "drift" or "needs apply" (the state is unknown).
func runStep(ctx context.Context, kind string, s Step, r Remote, st *State) (string, error) {
	cb := func(f func(context.Context, Remote, *State) error) error {
		return protect(func() error { return f(ctx, r, st) })
	}
	var pe *panicError
	if kind == KindVerify {
		if s.Verify == nil {
			st.Printf("в режиме проверки этот шаг не выполняется")
			return StepSkipped, nil
		}
		if err := cb(s.Verify); err != nil {
			if errors.As(err, &pe) {
				return StepFailed, err
			}
			st.Printf("расхождение: %v", err)
			return StepDrift, nil
		}
		return StepOK, nil
	}
	if s.Verify != nil {
		verr := cb(s.Verify)
		if verr == nil {
			return StepOK, nil
		}
		if s.Apply == nil || errors.As(verr, &pe) {
			return StepFailed, verr
		}
		st.Printf("нужна настройка: %v", verr)
	}
	if s.Apply == nil {
		return StepOK, nil
	}
	if err := cb(s.Apply); err != nil {
		if errors.Is(err, ErrSkipped) {
			return StepSkipped, nil
		}
		return rollback(cb, s, st, err)
	}
	if s.Verify != nil {
		if err := cb(s.Verify); err != nil {
			return rollback(cb, s, st, fmt.Errorf("после настройки проверка не прошла: %w", err))
		}
	}
	return StepDone, nil
}

func rollback(cb func(func(context.Context, Remote, *State) error) error, s Step, st *State, cause error) (string, error) {
	if s.Rollback == nil {
		return StepFailed, cause
	}
	if err := cb(s.Rollback); err != nil {
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
