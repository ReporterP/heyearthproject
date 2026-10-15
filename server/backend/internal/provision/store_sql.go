package provision

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/lib/pq"
	"golang.org/x/crypto/ssh"
)

// SQLStore is the PostgreSQL Store (and Hooks) of the API and of deploy --setup-exit-node.
type SQLStore struct {
	DB         *sql.DB
	HubKeyPath string                       // "" = this machine has no Hub key (deploy CLI)
	Decrypt    func([]byte) (string, error) // internal/crypto.Decrypt
}

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

// loadNodeQuery reads only the encrypted password (the plaintext column is migrated away at API
// start) and the stored host key blob, whose type DialNode negotiates when it has the fingerprint.
const loadNodeQuery = `
SELECT s.id, COALESCE(s.name, ''), s.role, COALESCE(s.country_code, ''), s.ip_address,
       COALESCE(s.ssh_port, 22), COALESCE(s.ssh_user, 'root'), s.ssh_password_enc,
       COALESCE(sk.private_key, ''), COALESCE(s.ssh_host_key_fp, ''), COALESCE(s.ssh_host_key, ''),
       s.ssh_host_key_confirmed_at IS NOT NULL,
       s.lifecycle_state, COALESCE(s.load_weight, 1), s.mtproto_status, s.tunnel_fallback_enabled
  FROM servers s LEFT JOIN ssh_keys sk ON sk.id = s.ssh_key_id
 WHERE s.id = $1`

// LoadNode reads a node with its credentials decrypted (and the Hub's key when HubKeyPath is set).
func (s *SQLStore) LoadNode(ctx context.Context, id int) (Node, error) {
	var n Node
	var enc []byte
	var keyPEM, hostKeyBlob string
	err := s.DB.QueryRowContext(ctx, loadNodeQuery, id).Scan(&n.ID, &n.Name, &n.Role, &n.CountryCode, &n.IP,
		&n.SSHPort, &n.SSHUser, &enc, &keyPEM, &n.HostKeyFP, &hostKeyBlob, &n.HostKeyConfirmed, &n.Lifecycle, &n.LoadWeight,
		&n.MTProtoStatus, &n.FallbackEnabled)
	if errors.Is(err, sql.ErrNoRows) {
		return Node{}, fmt.Errorf("нода %d не найдена", id)
	}
	if err != nil {
		return Node{}, err
	}
	n.HostKeyType = StoredHostKeyType(hostKeyBlob, n.HostKeyFP)
	if len(enc) > 0 {
		if s.Decrypt == nil {
			return Node{}, errors.New("нет функции расшифровки пароля SSH")
		}
		if n.Password, err = s.Decrypt(enc); err != nil {
			return Node{}, fmt.Errorf("пароль SSH ноды %d не расшифровывается (SSH_ENCRYPTION_KEY на Hub): %w", id, err)
		}
	}
	if keyPEM != "" {
		if signer, err := ssh.ParsePrivateKey([]byte(keyPEM)); err == nil {
			n.KeySigner = signer
		}
	}
	if s.HubKeyPath != "" {
		if n.HubSigner, err = LoadHubSigner(s.HubKeyPath); err != nil {
			return Node{}, err
		}
	}
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
	createJobQuery = `INSERT INTO provision_jobs (server_id, kind, status, started_at) VALUES ($1, $2, 'running', NOW()) RETURNING id`
	reopenJobQuery = `UPDATE provision_jobs SET status = 'running', error = '', started_at = NOW(), finished_at = NULL
 WHERE id = $1 AND status IN ('interrupted', 'failed') RETURNING server_id, kind`
	deleteStepsQuery = `DELETE FROM provision_steps WHERE job_id = $1`
	insertStepsQuery = `INSERT INTO provision_steps (job_id, idx, name, status)
SELECT $1::bigint, (t.ord - 1)::int, t.name, 'pending' FROM unnest($2::text[]) WITH ORDINALITY AS t(name, ord)`
	markStepQuery = `UPDATE provision_steps SET status = $3, output_tail = $4,
       started_at = CASE WHEN $3::text = 'running' THEN NOW() ELSE COALESCE(started_at, NOW()) END,
       finished_at = CASE WHEN $3::text = 'running' THEN NULL ELSE NOW() END
 WHERE job_id = $1 AND idx = $2`
	finishJobQuery     = `UPDATE provision_jobs SET status = $2, error = $3, finished_at = NOW() WHERE id = $1`
	interruptJobsQuery = `UPDATE provision_jobs SET status = 'interrupted', finished_at = NOW(),
       error = 'API перезапустился во время задачи — нажмите «Продолжить»'
 WHERE status IN ('queued', 'running') RETURNING id`
	interruptStepsQuery = `UPDATE provision_steps SET status = 'interrupted', finished_at = NOW() WHERE status = 'running' AND job_id = ANY($1::bigint[])`
	jobQuery            = `SELECT id, server_id, kind, status, error, created_at, started_at, finished_at FROM provision_jobs`
	jobStepsQuery       = `SELECT idx, name, status, output_tail, started_at, finished_at FROM provision_steps WHERE job_id = $1 ORDER BY idx`
	lifecycleQuery      = `SELECT lifecycle_state FROM servers WHERE id = $1`
	setLifecycleQuery   = `UPDATE servers SET lifecycle_state = $2, lifecycle_updated_at = NOW(), updated_at = NOW()
 WHERE id = $1 AND lifecycle_state = ANY($3)`
	loadTunnelQuery = `SELECT COALESCE(tunnel_uuid, ''), COALESCE(tunnel_public_key, ''), COALESCE(tunnel_short_id, ''),
       COALESCE(tunnel_sni, ''), COALESCE(tunnel_ss_password, ''),
       COALESCE(tunnel_private_key, '') <> '' OR tunnel_protocol IS DISTINCT FROM 'hysteria2' OR tunnel_port IS DISTINCT FROM 50443
  FROM servers WHERE id = $1`
	saveTunnelQuery = `UPDATE servers
   SET tunnel_protocol = 'hysteria2', tunnel_port = 50443, tunnel_uuid = $2, tunnel_public_key = $3,
       tunnel_short_id = $4, tunnel_sni = $5, tunnel_ss_password = $6, tunnel_private_key = NULL, updated_at = NOW()
 WHERE id = $1 AND (tunnel_protocol IS DISTINCT FROM 'hysteria2' OR tunnel_port IS DISTINCT FROM 50443
       OR tunnel_uuid IS DISTINCT FROM $2 OR tunnel_public_key IS DISTINCT FROM $3 OR tunnel_short_id IS DISTINCT FROM $4
       OR tunnel_sni IS DISTINCT FROM $5 OR tunnel_ss_password IS DISTINCT FROM $6 OR tunnel_private_key IS NOT NULL)`
	forgetTunnelQuery = `UPDATE servers SET tunnel_ss_password = NULL, tunnel_private_key = NULL, tunnel_status = 'unknown', updated_at = NOW() WHERE id = $1`
	activateQuery     = `UPDATE servers SET lifecycle_state = 'online', tunnel_status = 'healthy', tunnel_error = '',
       lifecycle_updated_at = NOW(), updated_at = NOW()
 WHERE id = $1 AND lifecycle_state = 'provisioning'`
	markMTProtoQuery = `UPDATE servers SET mtproto_status = $1, mtproto_last_check = NOW(),
       mtproto_installed_at = COALESCE(mtproto_installed_at, CASE WHEN $1::text = 'healthy' THEN NOW() END), updated_at = NOW()
 WHERE id = $2`
	bumpSubscriptionQuery = `UPDATE app_settings SET value = (COALESCE(NULLIF(value, '')::bigint, 0) + 1)::text WHERE key = 'subscription_version'`
	paidPlansQuery        = `SELECT id FROM plans WHERE is_active = TRUE AND price_rub > 0 ORDER BY sort_order, id`
	unbindPlansQuery      = `DELETE FROM plan_servers WHERE server_id = $1`
	bindPlansQuery        = `INSERT INTO plan_servers (plan_id, server_id) SELECT unnest($2::int[]), $1::int ON CONFLICT DO NOTHING`
)

// mapActive: a unique violation here can only be uq_provision_jobs_active (one active job per node).
func mapActive(err error) error {
	var pe *pq.Error
	if errors.As(err, &pe) && pe.Code == "23505" {
		return ErrJobActive
	}
	return err
}

func (s *SQLStore) CreateJob(ctx context.Context, serverID int, kind string) (int64, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx, createJobQuery, serverID, kind).Scan(&id)
	return id, mapActive(err)
}

// ReopenJob puts an interrupted or failed job back to running (ErrJobActive while another job of
// the node is active).
func (s *SQLStore) ReopenJob(ctx context.Context, jobID int64) (Job, error) {
	j := Job{ID: jobID, Status: JobRunning}
	err := s.DB.QueryRowContext(ctx, reopenJobQuery, jobID).Scan(&j.ServerID, &j.Kind)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrJobNotResumable
	}
	if err != nil {
		return Job{}, mapActive(err)
	}
	return j, nil
}

// SetSteps replaces the job's steps with names, all pending (one transaction).
func (s *SQLStore) SetSteps(ctx context.Context, jobID int64, names []string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, deleteStepsQuery, jobID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, insertStepsQuery, jobID, pq.Array(names)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLStore) MarkStep(ctx context.Context, jobID int64, idx int, status, out string) error {
	_, err := s.DB.ExecContext(ctx, markStepQuery, jobID, idx, status, out)
	return err
}

func (s *SQLStore) FinishJob(ctx context.Context, jobID int64, status, msg string) error {
	_, err := s.DB.ExecContext(ctx, finishJobQuery, jobID, status, msg)
	return err
}

// InterruptRunning is called once at API start: a job that was running died with the old process.
func (s *SQLStore) InterruptRunning(ctx context.Context) (int64, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, interruptJobsQuery)
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
			err := s.DB.QueryRowContext(ctx, loadTunnelQuery, id).Scan(&t.UUID, &t.PublicKey, &t.ShortID, &t.SNI, &t.Password, &t.Stale)
			return t, err
		},
		SaveTunnel: func(ctx context.Context, id int, t Tunnel) (bool, error) {
			res, err := s.DB.ExecContext(ctx, saveTunnelQuery, id, t.UUID, t.PublicKey, t.ShortID, t.SNI, t.Password)
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
		Activate: func(ctx context.Context, id int) error {
			res, err := s.DB.ExecContext(ctx, activateQuery, id)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return errors.New("нода не в состоянии «настраивается» — включение пропущено")
			}
			return nil
		},
		MarkMTProto: func(ctx context.Context, id int, status string) error {
			_, err := s.DB.ExecContext(ctx, markMTProtoQuery, status, id)
			return err
		},
		BumpSubscription: func(ctx context.Context) error {
			_, err := s.DB.ExecContext(ctx, bumpSubscriptionQuery)
			return err
		},
		RequestSync: requestSync,
		ProbeAgent:  probeAgent,
	}
}

// BindPlans makes planIDs exactly the plans that route through serverID (one transaction).
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
