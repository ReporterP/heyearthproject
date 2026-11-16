package provision

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Hub recipes (the Telegram WEB proxy, tgweb.go) run on the Hub itself: no servers row, so no
// provision_jobs row either. RunHub is the runner's step loop without the store — the same
// runStep (verify → apply → verify, rollback), the same redaction — reporting through update.

// HubStep is one step of a Hub run as the admin panel shows it.
type HubStep struct {
	Name   string `json:"name"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Output string `json:"output"`
}

// HubRun is a Hub recipe run: Status running | succeeded | failed.
type HubRun struct {
	Kind       string     `json:"kind"`
	Status     string     `json:"status"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	Error      string     `json:"error"`
	Steps      []HubStep  `json:"steps"`
}

func (h HubRun) clone() HubRun {
	h.Steps = append([]HubStep(nil), h.Steps...)
	return h
}

// RunHub runs recipe with st.Hub as the remote of every step and calls update (when set) with a
// copy of the run after every change. Outputs and the error are redacted with every secret the
// state knows (st.Secrets, AddSecret).
func RunHub(ctx context.Context, kind string, recipe Recipe, st *State, update func(HubRun)) HubRun {
	run := HubRun{Kind: kind, Status: JobRunning, StartedAt: time.Now().UTC()}
	for _, s := range recipe {
		run.Steps = append(run.Steps, HubStep{Name: s.Name, Title: Title(s.Name), Status: StepPending})
	}
	emit := func() {
		if update != nil {
			update(run.clone())
		}
	}
	finish := func(status, msg string) HubRun {
		now := time.Now().UTC()
		run.Status, run.FinishedAt = status, &now
		var secrets secretSet
		secrets.add(st.Secrets()...)
		run.Error = redact(msg, secrets.list)
		emit()
		return run.clone()
	}
	emit()
	var secrets secretSet
	for i, step := range recipe {
		secrets.add(st.Secrets()...)
		if ctx.Err() != nil {
			run.Steps[i].Status = StepFailed
			return finish(JobFailed, fmt.Sprintf("время вышло до шага «%s»", Title(step.Name)))
		}
		run.Steps[i].Status = StepRunning
		emit()
		buf := &tailBuffer{max: 8 * MaxOutputTail}
		st.out = buf
		status, err := runStep(ctx, st.Kind, step, st.Hub, st)
		st.out = nil
		secrets.add(st.Secrets()...)
		text := buf.String()
		if err != nil {
			text += err.Error()
		}
		run.Steps[i].Status = status
		run.Steps[i].Output = redact(tail(redact(strings.TrimSpace(text), secrets.list), MaxOutputTail), secrets.list)
		emit()
		if status == StepFailed || status == StepRolledBack {
			return finish(JobFailed, fmt.Sprintf("шаг «%s»: %v", Title(step.Name), err))
		}
	}
	return finish(JobSucceeded, "")
}
