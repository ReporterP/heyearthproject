package provision

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/lib/pq"
	"golang.org/x/crypto/ssh"
)

// Who runs a job (provision_jobs.runner).
const (
	RunnerAPI = "api"
	RunnerCLI = "cli" // deploy --setup-exit-node
)

// SQLStore is the PostgreSQL Store (and Hooks) of the API and of deploy --setup-exit-node.
type SQLStore struct {
	DB         *sql.DB
	HubKeyPath string                       // "" = this machine has no Hub key (deploy CLI)
	Decrypt    func([]byte) (string, error) // internal/crypto.Decrypt
	// JobRunner is who runs the jobs this store creates or reopens: RunnerAPI ("" too) or
	// RunnerCLI. InterruptRunning at API start takes over a CLI job only once no run can still be
	// alive (staleRunAge).
	JobRunner string
	// BumpSubscription is Hooks.BumpSubscription; nil: biz.BumpSubscriptionVersionOn on DB.
	BumpSubscription func(ctx context.Context) error
}

// staleRunAge is the age after which no run can still own a job: JobTimeout, an SSH dial that
// started just before it (dial and handshake take no context), the failing step's
// RollbackTimeout and the final bookkeeping fit inside it (TestStaleRunAgeCoversARun). It is
// spelled out in reopenJobQuery, interruptJobsQuery and interruptStaleQuery.
const staleRunAge = 50 * time.Minute

// StepView is one provision_steps row as the admin API returns it.
type StepView struct {
	Idx        int        `json:"idx"`
	Name       string     `json:"name"`
	Title      string     `json:"title"`
	Status     string     `json:"status"`
	OutputTail string     `json:"output_tail"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

// JobView is one provision_jobs row (with its steps from SQLStore.JobView).
type JobView struct {
	ID         int64      `json:"id"`
	ServerID   int        `json:"server_id"`
	Kind       string     `json:"kind"`
	Status     string     `json:"status"`
	Error      string     `json:"error"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	Steps      []StepView `json:"steps,omitempty"`
}

// ErrNodeNotFound: no servers row with that id.
var ErrNodeNotFound = errors.New("не найдена")

// nodeMetaColumns are the public columns of a node (no credentials), shared by NodeMeta and LoadNode.
const nodeMetaColumns = `s.id, COALESCE(s.name, ''), s.role, COALESCE(s.country_code, ''), s.ip_address,
       COALESCE(s.ssh_port, 22), COALESCE(s.ssh_user, 'root'), COALESCE(s.ssh_host_key_fp, ''), COALESCE(s.ssh_host_key, ''),
       s.ssh_host_key_confirmed_at IS NOT NULL,
       s.lifecycle_state, COALESCE(s.load_weight, 1), s.mtproto_status, s.tunnel_fallback_enabled,
       COALESCE(s.relay_tunnel, FALSE)`

// nodeMetaQuery: what the admin panel needs to decide about a node — no password, no key.
const nodeMetaQuery = `SELECT ` + nodeMetaColumns + `
  FROM servers s
 WHERE s.id = $1`

// loadNodeQuery adds the credential: only the encrypted password (the plaintext column is
// migrated away at API start).
const loadNodeQuery = `SELECT ` + nodeMetaColumns + `,
       s.ssh_password_enc
  FROM servers s
 WHERE s.id = $1`

// scanNodeMeta scans nodeMetaColumns (then extra) and fills HostKeyType from the stored blob.
func scanNodeMeta(row *sql.Row, id int, extra ...any) (Node, error) {
	var n Node
	var hostKeyBlob string
	dest := append([]any{&n.ID, &n.Name, &n.Role, &n.CountryCode, &n.IP, &n.SSHPort, &n.SSHUser, &n.HostKeyFP, &hostKeyBlob,
		&n.HostKeyConfirmed, &n.Lifecycle, &n.LoadWeight, &n.MTProtoStatus, &n.FallbackEnabled, &n.RelayTunnel}, extra...)
	if err := row.Scan(dest...); errors.Is(err, sql.ErrNoRows) {
		return Node{}, fmt.Errorf("нода %d %w", id, ErrNodeNotFound)
	} else if err != nil {
		return Node{}, err
	}
	n.HostKeyType = StoredHostKeyType(hostKeyBlob, n.HostKeyFP)
	return n, nil
}

// NodeMeta reads a node without its credentials (no decryption, no Hub key): for decisions that
// do not connect (the admin panel's checks, host key confirmation, deletion).
func (s *SQLStore) NodeMeta(ctx context.Context, id int) (Node, error) {
	return scanNodeMeta(s.DB.QueryRowContext(ctx, nodeMetaQuery, id), id)
}

// LoadNode reads a node with its credentials (and the Hub's key when HubKeyPath is set). A
// credential that cannot be used — a password that does not decrypt (no or another
// SSH_ENCRYPTION_KEY), a corrupt Hub key — does not fail the load: the Hub-side steps of a job
// need none (taking a node out of service must work without them); it is kept in the node and
// DialNode reports it as ErrNoCredentials (needsOwner) when the login depends on it.
func (s *SQLStore) LoadNode(ctx context.Context, id int) (Node, error) {
	var enc []byte
	n, err := scanNodeMeta(s.DB.QueryRowContext(ctx, loadNodeQuery, id), id, &enc)
	if err != nil {
		return Node{}, err
	}
	var problems []error
	if len(enc) > 0 {
		switch {
		case s.Decrypt == nil:
			problems = append(problems, errors.New("нет функции расшифровки пароля SSH"))
		default:
			if n.Password, err = s.Decrypt(enc); err != nil {
				n.Password = ""
				problems = append(problems, fmt.Errorf("пароль SSH ноды %d не расшифровывается (SSH_ENCRYPTION_KEY на Hub): %w", id, err))
			}
		}
	}
	if s.HubKeyPath != "" {
		if n.HubSigner, err = LoadHubSigner(s.HubKeyPath); err != nil {
			n.HubSigner = nil
			problems = append(problems, err)
		}
	}
	n.credErr = errors.Join(problems...)
	return n, nil
}

// LoadHubSigner reads the Hub's key; nil, nil while it has not been created yet.
func LoadHubSigner(path string) (ssh.Signer, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(b)
	if err != nil {
		return nil, fmt.Errorf("ключ Hub %s повреждён: %w", path, err)
	}
	return signer, nil
}

const (
	createJobQuery = `INSERT INTO provision_jobs (server_id, kind, status, started_at, run_token, runner)
VALUES ($1, $2, 'running', NOW(), $3, $4) RETURNING id`
	// Only the node's latest job (resuming an older one would undo what a later job did): an
	// interrupted or failed one, or a CLI run that cannot be alive any more (Ctrl-C, a dropped DB
	// tunnel), taken over under a fresh token.
	reopenJobQuery = `UPDATE provision_jobs SET status = 'running', error = '', started_at = NOW(), finished_at = NULL,
       run_token = $2, runner = $3
 WHERE id = $1
   AND (status IN ('interrupted', 'failed')
        OR (status = 'running' AND runner = 'cli' AND COALESCE(started_at, created_at) < NOW() - interval '50 minutes'))
   AND id = (SELECT max(j.id) FROM provision_jobs j WHERE j.server_id = provision_jobs.server_id)
 RETURNING server_id, kind`
	ownJobQuery      = `SELECT 1 FROM provision_jobs WHERE id = $1 AND run_token = $2 AND status = 'running' FOR UPDATE`
	deleteStepsQuery = `DELETE FROM provision_steps WHERE job_id = $1`
	insertStepsQuery = `INSERT INTO provision_steps (job_id, idx, name, status)
SELECT $1::bigint, (t.ord - 1)::int, t.name, 'pending' FROM unnest($2::text[]) WITH ORDINALITY AS t(name, ord)`
	markStepQuery = `UPDATE provision_steps SET status = $3, output_tail = $4,
       started_at = CASE WHEN $3::text = 'running' THEN NOW() ELSE COALESCE(started_at, NOW()) END,
       finished_at = CASE WHEN $3::text = 'running' THEN NULL ELSE NOW() END
 WHERE job_id = $1 AND idx = $2
   AND EXISTS (SELECT 1 FROM provision_jobs WHERE id = $1 AND run_token = $5 AND status = 'running')`
	finishJobQuery = `UPDATE provision_jobs SET status = $2, error = $3, finished_at = NOW(), run_token = NULL
 WHERE id = $1 AND run_token = $4 AND status = 'running'`
	// A live CLI run is left alone; one older than staleRunAge is dead whatever ran it.
	interruptJobsQuery = `UPDATE provision_jobs SET status = 'interrupted', run_token = NULL, finished_at = NOW(),
       error = CASE WHEN runner = 'api' THEN 'API перезапустился во время задачи — нажмите «Продолжить»'
                    ELSE 'Запуск из командной строки не завершил задачу — нажмите «Продолжить»' END
 WHERE status IN ('queued', 'running')
   AND (runner = 'api' OR COALESCE(started_at, created_at) < NOW() - interval '50 minutes')
 RETURNING id`
	// The API's periodic sweep (Runner.SweepStale): only CLI runs that cannot be alive any more,
	// so their nodes are not locked until the next API restart.
	interruptStaleQuery = `UPDATE provision_jobs SET status = 'interrupted', run_token = NULL, finished_at = NOW(),
       error = 'Запуск из командной строки не завершил задачу — нажмите «Продолжить»'
 WHERE status IN ('queued', 'running')
   AND runner = 'cli' AND COALESCE(started_at, created_at) < NOW() - interval '50 minutes'
 RETURNING id`
	interruptStepsQuery = `UPDATE provision_steps SET status = 'interrupted', finished_at = NOW() WHERE status = 'running' AND job_id = ANY($1::bigint[])`
	jobQuery            = `SELECT id, server_id, kind, status, error, created_at, started_at, finished_at FROM provision_jobs`
	jobStepsQuery       = `SELECT idx, name, status, output_tail, started_at, finished_at FROM provision_steps WHERE job_id = $1 ORDER BY idx`
	lifecycleQuery      = `SELECT lifecycle_state FROM servers WHERE id = $1`
	setLifecycleQuery   = `UPDATE servers SET lifecycle_state = $2, lifecycle_updated_at = NOW(), updated_at = NOW()
 WHERE id = $1 AND lifecycle_state = ANY($3)`
	loadTunnelQuery = `SELECT COALESCE(tunnel_uuid, ''), COALESCE(tunnel_public_key, ''), COALESCE(tunnel_short_id, ''),
       COALESCE(tunnel_sni, ''), COALESCE(tunnel_ss_password, ''), COALESCE(tunnel_cert_sha256, ''),
       COALESCE(tunnel_private_key, '') <> '' OR tunnel_protocol IS DISTINCT FROM 'hysteria2' OR tunnel_port IS DISTINCT FROM 50443
  FROM servers WHERE id = $1`
	saveTunnelQuery = `UPDATE servers
   SET tunnel_protocol = 'hysteria2', tunnel_port = 50443, tunnel_uuid = $2, tunnel_public_key = $3,
       tunnel_short_id = $4, tunnel_sni = $5, tunnel_ss_password = $6, tunnel_cert_sha256 = $7, tunnel_private_key = NULL, updated_at = NOW()
 WHERE id = $1 AND (tunnel_protocol IS DISTINCT FROM 'hysteria2' OR tunnel_port IS DISTINCT FROM 50443
       OR tunnel_uuid IS DISTINCT FROM $2 OR tunnel_public_key IS DISTINCT FROM $3 OR tunnel_short_id IS DISTINCT FROM $4
       OR tunnel_sni IS DISTINCT FROM $5 OR tunnel_ss_password IS DISTINCT FROM $6 OR tunnel_cert_sha256 IS DISTINCT FROM $7
       OR tunnel_private_key IS NOT NULL)`
	forgetTunnelQuery = `UPDATE servers SET tunnel_ss_password = NULL, tunnel_private_key = NULL, tunnel_status = 'unknown', updated_at = NOW() WHERE id = $1`
	activateQuery     = `UPDATE servers SET lifecycle_state = 'online', tunnel_status = 'healthy', tunnel_error = '',
       lifecycle_updated_at = NOW(), updated_at = NOW()
 WHERE id = $1 AND lifecycle_state = 'provisioning'`
	// activateEntryQuery: an entry that goes online through a setup job is a new path for users —
	// it starts on the canary (rotation 'test', docs/runbooks/subscription-changes.md); one the
	// owner took out of rotation stays out. Exits have no entry_state row (role filter).
	activateEntryQuery = `INSERT INTO entry_state (entry_id, rotation, rotation_reason, rotation_at, rotation_by)
SELECT id, 'test', 'новый вход: сначала тестовый пользователь', NOW(), 'установка' FROM servers WHERE id = $1 AND role = 'relay'
ON CONFLICT (entry_id) DO UPDATE SET
  rotation = CASE WHEN entry_state.rotation = 'off' THEN 'off' ELSE 'test' END,
  rotation_reason = CASE WHEN entry_state.rotation = 'off' THEN entry_state.rotation_reason ELSE EXCLUDED.rotation_reason END,
  rotation_at = CASE WHEN entry_state.rotation = 'off' THEN entry_state.rotation_at ELSE EXCLUDED.rotation_at END,
  rotation_by = CASE WHEN entry_state.rotation = 'off' THEN entry_state.rotation_by ELSE EXCLUDED.rotation_by END,
  updated_at = NOW()`
	// markMTProtoQuery never overwrites the owner's switch: MTProxy switched off ('disabled')
	// while a step runs stays off (the hook reports ErrMTProxyDisabled).
	markMTProtoQuery = `UPDATE servers SET mtproto_status = $1, mtproto_last_check = NOW(),
       mtproto_installed_at = COALESCE(mtproto_installed_at, CASE WHEN $1::text = 'healthy' THEN NOW() END), updated_at = NOW()
 WHERE id = $2 AND mtproto_status <> 'disabled'`
	paidPlansQuery   = `SELECT id FROM plans WHERE is_active = TRUE AND price_rub > 0 ORDER BY sort_order, id`
	lockServerQuery  = `SELECT 1 FROM servers WHERE id = $1 FOR UPDATE`
	unbindPlansQuery = `DELETE FROM plan_servers WHERE server_id = $1`
	bindPlansQuery   = `INSERT INTO plan_servers (plan_id, server_id) SELECT unnest($2::int[]), $1::int ON CONFLICT DO NOTHING`
)

// mapActive: a unique violation here can only be uq_provision_jobs_active (one active job per node).
func mapActive(err error) error {
	var pe *pq.Error
	if errors.As(err, &pe) && pe.Code == "23505" {
		return ErrJobActive
	}
	return err
}

// newRunToken is a fresh run's ownership of its job.
func newRunToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// newRun is the token and the runner of a run about to own a job.
func (s *SQLStore) newRun() (token, runner string, err error) {
	switch s.JobRunner {
	case "", RunnerAPI:
		runner = RunnerAPI
	case RunnerCLI:
		runner = RunnerCLI
	default:
		return "", "", fmt.Errorf("неизвестный исполнитель задач %q", s.JobRunner)
	}
	token, err = newRunToken()
	return token, runner, err
}

func (s *SQLStore) CreateJob(ctx context.Context, serverID int, kind string) (Job, error) {
	token, runner, err := s.newRun()
	if err != nil {
		return Job{}, err
	}
	j := Job{ServerID: serverID, Kind: kind, Status: JobRunning, Token: token}
	if err := s.DB.QueryRowContext(ctx, createJobQuery, serverID, kind, token, runner).Scan(&j.ID); err != nil {
		return Job{}, mapActive(err)
	}
	return j, nil
}

// ReopenJob puts the node's latest job back to running under a fresh run token if it was
// interrupted or failed (ErrJobActive while another job of the node is active).
func (s *SQLStore) ReopenJob(ctx context.Context, jobID int64) (Job, error) {
	token, runner, err := s.newRun()
	if err != nil {
		return Job{}, err
	}
	j := Job{ID: jobID, Status: JobRunning, Token: token}
	err = s.DB.QueryRowContext(ctx, reopenJobQuery, jobID, token, runner).Scan(&j.ServerID, &j.Kind)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrJobNotResumable
	}
	if err != nil {
		return Job{}, mapActive(err)
	}
	return j, nil
}

// SetSteps replaces the job's steps with names, all pending (one transaction; the job row locked
// and still owned by job.Token, else ErrJobLost).
func (s *SQLStore) SetSteps(ctx context.Context, job Job, names []string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var one int
	if err := tx.QueryRowContext(ctx, ownJobQuery, job.ID, job.Token).Scan(&one); errors.Is(err, sql.ErrNoRows) {
		return ErrJobLost
	} else if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, deleteStepsQuery, job.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, insertStepsQuery, job.ID, pq.Array(names)); err != nil {
		return err
	}
	return tx.Commit()
}

// owned: an UPDATE guarded by the run token that changed nothing means the run lost the job.
func owned(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrJobLost
	}
	return nil
}

func (s *SQLStore) MarkStep(ctx context.Context, job Job, idx int, status, out string) error {
	return owned(s.DB.ExecContext(ctx, markStepQuery, job.ID, idx, status, out, job.Token))
}

func (s *SQLStore) FinishJob(ctx context.Context, job Job, status, msg string) error {
	return owned(s.DB.ExecContext(ctx, finishJobQuery, job.ID, status, msg, job.Token))
}

// InterruptRunning is called once at API start: a job that was running died with the old process
// (a CLI run only once it is older than staleRunAge).
func (s *SQLStore) InterruptRunning(ctx context.Context) (int64, error) {
	return s.interrupt(ctx, interruptJobsQuery)
}

// InterruptStale interrupts the jobs of CLI runs older than staleRunAge (Runner.SweepStale).
func (s *SQLStore) InterruptStale(ctx context.Context) (int64, error) {
	return s.interrupt(ctx, interruptStaleQuery)
}

// interrupt runs one of the interrupt queries and marks the running steps of exactly those jobs
// interrupted, in one transaction.
func (s *SQLStore) interrupt(ctx context.Context, jobsQuery string) (int64, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, jobsQuery)
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	if len(ids) == 0 {
		return 0, tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, interruptStepsQuery, pq.Array(ids)); err != nil {
		return 0, err
	}
	return int64(len(ids)), tx.Commit()
}

func scanJob(sc interface{ Scan(...any) error }) (JobView, error) {
	var j JobView
	var st, fin sql.NullTime
	err := sc.Scan(&j.ID, &j.ServerID, &j.Kind, &j.Status, &j.Error, &j.CreatedAt, &st, &fin)
	j.StartedAt, j.FinishedAt = timePtr(st), timePtr(fin)
	return j, err
}

func timePtr(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}

// JobView is one job with its steps (titles included).
func (s *SQLStore) JobView(ctx context.Context, jobID int64) (JobView, error) {
	j, err := scanJob(s.DB.QueryRowContext(ctx, jobQuery+` WHERE id = $1`, jobID))
	if err != nil {
		return j, err
	}
	rows, err := s.DB.QueryContext(ctx, jobStepsQuery, jobID)
	if err != nil {
		return j, err
	}
	defer rows.Close()
	for rows.Next() {
		var v StepView
		var st, fin sql.NullTime
		if err := rows.Scan(&v.Idx, &v.Name, &v.Status, &v.OutputTail, &st, &fin); err != nil {
			return j, err
		}
		v.Title, v.StartedAt, v.FinishedAt = Title(v.Name), timePtr(st), timePtr(fin)
		j.Steps = append(j.Steps, v)
	}
	return j, rows.Err()
}

// ListJobs: the latest jobs of a node, newest first, without steps.
func (s *SQLStore) ListJobs(ctx context.Context, serverID, limit int) ([]JobView, error) {
	rows, err := s.DB.QueryContext(ctx, jobQuery+` WHERE server_id = $1 ORDER BY id DESC LIMIT $2`, serverID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []JobView{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// Hooks binds recipe side effects to this database.
func (s *SQLStore) Hooks(requestSync func(), probeAgent func(context.Context, string) error) Hooks {
	return Hooks{
		Lifecycle: func(ctx context.Context, id int) (string, error) {
			var v string
			err := s.DB.QueryRowContext(ctx, lifecycleQuery, id).Scan(&v)
			return v, err
		},
		SetLifecycle: func(ctx context.Context, id int, from []string, to string) error {
			res, err := s.DB.ExecContext(ctx, setLifecycleQuery, id, to, pq.Array(from))
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				var cur string
				if err := s.DB.QueryRowContext(ctx, lifecycleQuery, id).Scan(&cur); err != nil {
					return err
				}
				if cur != to {
					return fmt.Errorf("нода в состоянии «%s», переход в «%s» невозможен", cur, to)
				}
			}
			return nil
		},
		LoadTunnel: func(ctx context.Context, id int) (Tunnel, error) {
			var t Tunnel
			err := s.DB.QueryRowContext(ctx, loadTunnelQuery, id).Scan(&t.UUID, &t.PublicKey, &t.ShortID, &t.SNI, &t.Password, &t.CertPin, &t.Stale)
			return t, err
		},
		SaveTunnel: func(ctx context.Context, id int, t Tunnel) (bool, error) {
			res, err := s.DB.ExecContext(ctx, saveTunnelQuery, id, t.UUID, t.PublicKey, t.ShortID, t.SNI, t.Password, t.CertPin)
			if err != nil {
				return false, err
			}
			n, _ := res.RowsAffected()
			return n > 0, nil
		},
		ForgetTunnel: func(ctx context.Context, id int) error {
			_, err := s.DB.ExecContext(ctx, forgetTunnelQuery, id)
			return err
		},
		SetRelayTunnel: func(ctx context.Context, id int, on bool) error {
			res, err := s.DB.ExecContext(ctx, `UPDATE servers SET relay_tunnel = $2, updated_at = NOW() WHERE id = $1 AND role = 'relay'`, id, on)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return errors.New("вход не найден — выбор туннеля не сохранён")
			}
			return nil
		},
		Activate: func(ctx context.Context, id int) error {
			// One transaction: an entry is never online without its rotation (the subscription
			// would publish a new entry to everyone).
			tx, err := s.DB.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer func() { _ = tx.Rollback() }()
			res, err := tx.ExecContext(ctx, activateQuery, id)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return errors.New("нода не в состоянии «настраивается» — включение пропущено")
			}
			if _, err := tx.ExecContext(ctx, activateEntryQuery, id); err != nil {
				return err
			}
			return tx.Commit()
		},
		MarkMTProto: func(ctx context.Context, id int, status string) error {
			res, err := s.DB.ExecContext(ctx, markMTProtoQuery, status, id)
			if err != nil {
				return err
			}
			if n, err := res.RowsAffected(); err != nil {
				return err
			} else if n == 0 {
				return ErrMTProxyDisabled // the owner switched it off meanwhile (a job holds the node: it exists)
			}
			return nil
		},
		BumpSubscription: s.bumpSubscription(),
		RequestSync:      requestSync,
		ProbeAgent:       probeAgent,
	}
}

func (s *SQLStore) bumpSubscription() func(context.Context) error {
	if s.BumpSubscription != nil {
		return s.BumpSubscription
	}
	return func(ctx context.Context) error { return biz.BumpSubscriptionVersionOn(ctx, s.DB) }
}

// BindPlans makes planIDs exactly the plans that route through serverID (one transaction; the
// server row is locked first, so two bindings of one node cannot interleave).
func BindPlans(ctx context.Context, db *sql.DB, serverID int, planIDs []int) error {
	ids := make([]int64, len(planIDs))
	for i, p := range planIDs {
		ids[i] = int64(p)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var one int
	if err := tx.QueryRowContext(ctx, lockServerQuery, serverID).Scan(&one); errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("нода %d не найдена", serverID)
	} else if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, unbindPlansQuery, serverID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, bindPlansQuery, serverID, pq.Array(ids)); err != nil {
		return err
	}
	return tx.Commit()
}

// PaidPlanIDs is the wizard's default binding: every active paid plan.
func PaidPlanIDs(ctx context.Context, db *sql.DB) ([]int, error) {
	rows, err := db.QueryContext(ctx, paidPlansQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
