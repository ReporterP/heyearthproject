package biz

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/heyearth/backend/internal/db"
)

// ─────────────────────── Support (plan TG5) ───────────────────────
//
// One conversation per person at a time: a message goes to the latest ticket unless it is
// closed (then a new ticket starts). Statuses: open (new, no answer), waiting (the person wrote
// again after an answer), answered (the last word is ours), closed.

const (
	SupportMaxBody      = 4000
	SupportSpamLimit    = 10
	SupportSpamInterval = 10 * time.Minute
)

var (
	ErrSupportEmpty     = errors.New("Напишите сообщение или приложите картинку")
	ErrSupportTooLong   = errors.New("Сообщение длиннее 4000 символов — разделите его на несколько")
	ErrSupportTooMany   = errors.New("Слишком много сообщений подряд — подождите несколько минут")
	ErrSupportDuplicate = errors.New("сообщение уже сохранено")
	ErrSupportNoTicket  = errors.New("обращение не найдено")
)

type SupportTicket struct {
	ID          int        `json:"id"`
	UserDBID    int        `json:"-"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	LastUserAt  *time.Time `json:"last_user_at"`
	LastAdminAt *time.Time `json:"last_admin_at"`
}

type SupportMessage struct {
	ID        int64      `json:"id"`
	TicketID  int        `json:"ticket_id"`
	Author    string     `json:"author"`
	AdminID   *int       `json:"admin_id,omitempty"`
	Body      string     `json:"body"`
	Image     string     `json:"image,omitempty"` // file name under uploads/support
	Via       string     `json:"via"`
	CreatedAt time.Time  `json:"created_at"`
	ReadAt    *time.Time `json:"read_at"`
}

// SupportIncoming is a message from the person.
type SupportIncoming struct {
	UserDBID  int
	Body      string
	Image     string
	Via       string // site | bot
	TGChatID  int64
	TGMessage int64
}

// SupportAdded says what an incoming message did.
type SupportAdded struct {
	Message   SupportMessage
	Ticket    SupportTicket
	NewTicket bool
	// NeedsAnswer: the ticket was not waiting for us before this message (new, or after our
	// answer) — the owner gets a notice and the bot confirms.
	NeedsAnswer bool
}

func cleanSupportBody(s string) (string, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if len([]rune(s)) > SupportMaxBody {
		return "", ErrSupportTooLong
	}
	return s, nil
}

// SupportAddUserMessage stores a message from the person (site or bot).
func SupportAddUserMessage(ctx context.Context, in SupportIncoming) (*SupportAdded, error) {
	body, err := cleanSupportBody(in.Body)
	if err != nil {
		return nil, err
	}
	if body == "" && in.Image == "" {
		return nil, ErrSupportEmpty
	}
	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	// the person's row serialises their messages (antispam count + ticket choice)
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, in.UserDBID); err != nil {
		return nil, err
	}
	var recent int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM support_messages m JOIN support_tickets t ON t.id = m.ticket_id
		 WHERE t.user_id = $1 AND m.author = 'user' AND m.created_at > NOW() - $2 * INTERVAL '1 second'`,
		in.UserDBID, int(SupportSpamInterval.Seconds())).Scan(&recent); err != nil {
		return nil, err
	}
	if recent >= SupportSpamLimit {
		return nil, ErrSupportTooMany
	}
	out := &SupportAdded{}
	t := &out.Ticket
	err = tx.QueryRowContext(ctx, `
		SELECT id, status FROM support_tickets WHERE user_id = $1 AND status <> 'closed'
		 ORDER BY created_at DESC LIMIT 1`, in.UserDBID).Scan(&t.ID, &t.Status)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if err := tx.QueryRowContext(ctx, `INSERT INTO support_tickets (user_id) VALUES ($1) RETURNING id`, in.UserDBID).Scan(&t.ID); err != nil {
			return nil, err
		}
		out.NewTicket, out.NeedsAnswer = true, true
		t.Status = "open"
	case err != nil:
		return nil, err
	default:
		out.NeedsAnswer = t.Status == "answered"
		if t.Status == "answered" {
			t.Status = "waiting"
		}
	}
	var chat, msgID any
	if in.TGMessage != 0 {
		chat, msgID = in.TGChatID, in.TGMessage
	}
	m := &out.Message
	err = tx.QueryRowContext(ctx, `
		INSERT INTO support_messages (ticket_id, author, body, image_path, via, tg_chat_id, tg_message_id)
		VALUES ($1, 'user', $2, NULLIF($3, ''), $4, $5, $6)
		RETURNING id, created_at`, t.ID, body, in.Image, in.Via, chat, msgID).Scan(&m.ID, &m.CreatedAt)
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23505" {
		return nil, ErrSupportDuplicate
	}
	if err != nil {
		return nil, err
	}
	m.TicketID, m.Author, m.Body, m.Image, m.Via = t.ID, "user", body, in.Image, in.Via
	if _, err := tx.ExecContext(ctx, `
		UPDATE support_tickets SET status = $2, updated_at = NOW(), last_user_at = NOW() WHERE id = $1`, t.ID, t.Status); err != nil {
		return nil, err
	}
	t.UserDBID = in.UserDBID
	return out, tx.Commit()
}

// SupportAddAdminMessage stores an answer (admin panel or a reply in the owner's chat) and
// returns it with the ticket's owner. Answering a closed ticket reopens it as «answered»: the
// person gets the answer, so the conversation is live again.
func SupportAddAdminMessage(ctx context.Context, ticketID int, adminID *int, body, via string) (*SupportMessage, int, error) {
	body, err := cleanSupportBody(body)
	if err != nil {
		return nil, 0, err
	}
	if body == "" {
		return nil, 0, ErrSupportEmpty
	}
	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback() //nolint:errcheck
	var userDBID int
	err = tx.QueryRowContext(ctx, `
		UPDATE support_tickets SET status = 'answered', updated_at = NOW(), last_admin_at = NOW()
		 WHERE id = $1 RETURNING user_id`, ticketID).Scan(&userDBID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, ErrSupportNoTicket
	}
	if err != nil {
		return nil, 0, err
	}
	m := &SupportMessage{TicketID: ticketID, Author: "admin", AdminID: adminID, Body: body, Via: via}
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO support_messages (ticket_id, author, admin_id, body, via) VALUES ($1, 'admin', $2, $3, $4)
		RETURNING id, created_at`, ticketID, adminID, body, via).Scan(&m.ID, &m.CreatedAt); err != nil {
		return nil, 0, err
	}
	// answering means the person's messages were read
	if _, err := tx.ExecContext(ctx, `UPDATE support_messages SET read_at = NOW() WHERE ticket_id = $1 AND author = 'user' AND read_at IS NULL`, ticketID); err != nil {
		return nil, 0, err
	}
	return m, userDBID, tx.Commit()
}

const supportMsgCols = `id, ticket_id, author, admin_id, body, COALESCE(image_path, ''), via, created_at, read_at`

func scanSupportMessages(rows *sql.Rows, err error) ([]SupportMessage, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SupportMessage{}
	for rows.Next() {
		var m SupportMessage
		var admin sql.NullInt64
		var read sql.NullTime
		if err := rows.Scan(&m.ID, &m.TicketID, &m.Author, &admin, &m.Body, &m.Image, &m.Via, &m.CreatedAt, &read); err != nil {
			return nil, err
		}
		if admin.Valid {
			a := int(admin.Int64)
			m.AdminID = &a
		}
		if read.Valid {
			m.ReadAt = &read.Time
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func scanTicket(row *sql.Row) (*SupportTicket, error) {
	t := &SupportTicket{}
	var lu, la sql.NullTime
	if err := row.Scan(&t.ID, &t.UserDBID, &t.Status, &t.CreatedAt, &t.UpdatedAt, &lu, &la); err != nil {
		return nil, err
	}
	if lu.Valid {
		t.LastUserAt = &lu.Time
	}
	if la.Valid {
		t.LastAdminAt = &la.Time
	}
	return t, nil
}

const ticketCols = `id, user_id, status, created_at, updated_at, last_user_at, last_admin_at`

// SupportUserThread is the person's latest ticket and its messages (nil ticket: never wrote).
func SupportUserThread(ctx context.Context, userDBID int) (*SupportTicket, []SupportMessage, error) {
	t, err := scanTicket(db.DB.QueryRowContext(ctx, `SELECT `+ticketCols+` FROM support_tickets WHERE user_id = $1 ORDER BY created_at DESC LIMIT 1`, userDBID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, []SupportMessage{}, nil
	}
	if err != nil {
		return nil, nil, err
	}
	msgs, err := scanSupportMessages(db.DB.QueryContext(ctx, `SELECT `+supportMsgCols+` FROM support_messages WHERE ticket_id = $1 ORDER BY id`, t.ID))
	return t, msgs, err
}

// SupportUnreadForUser counts answers the person has not opened yet.
func SupportUnreadForUser(ctx context.Context, userDBID int) (int, error) {
	var n int
	err := db.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM support_messages m JOIN support_tickets t ON t.id = m.ticket_id
		 WHERE t.user_id = $1 AND m.author = 'admin' AND m.read_at IS NULL`, userDBID).Scan(&n)
	return n, err
}

// SupportMarkReadByUser marks the answers as read.
func SupportMarkReadByUser(ctx context.Context, userDBID int) error {
	_, err := db.DB.ExecContext(ctx, `
		UPDATE support_messages m SET read_at = NOW() FROM support_tickets t
		 WHERE t.id = m.ticket_id AND t.user_id = $1 AND m.author = 'admin' AND m.read_at IS NULL`, userDBID)
	return err
}

// SupportImageOwner returns the user who owns an uploaded image (0 when unknown).
func SupportImageOwner(ctx context.Context, name string) (int, error) {
	var uid int
	err := db.DB.QueryRowContext(ctx, `
		SELECT t.user_id FROM support_messages m JOIN support_tickets t ON t.id = m.ticket_id WHERE m.image_path = $1 LIMIT 1`, name).Scan(&uid)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return uid, err
}

// ── admin ──

type SupportTicketRow struct {
	SupportTicket
	UserID       string `json:"user_id"`
	Username     string `json:"username"`
	TGUsername   string `json:"telegram_username"`
	Unread       int    `json:"unread"`
	LastBody     string `json:"last_body"`
	LastAuthor   string `json:"last_author"`
	MessageCount int    `json:"message_count"`
}

type SupportCounts struct {
	Open     int `json:"open"` // open + waiting: needs an answer
	Answered int `json:"answered"`
	Closed   int `json:"closed"`
	Unread   int `json:"unread"`
}

// SupportListTickets: filter = needs_answer | answered | closed | all; q matches the HiEarth ID,
// login or Telegram username.
func SupportListTickets(ctx context.Context, filter, q string, limit, offset int) ([]SupportTicketRow, SupportCounts, error) {
	var counts SupportCounts
	if err := db.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FILTER (WHERE status IN ('open', 'waiting')), COUNT(*) FILTER (WHERE status = 'answered'),
		       COUNT(*) FILTER (WHERE status = 'closed'),
		       (SELECT COUNT(*) FROM support_messages WHERE author = 'user' AND read_at IS NULL)
		  FROM support_tickets`).Scan(&counts.Open, &counts.Answered, &counts.Closed, &counts.Unread); err != nil {
		return nil, counts, err
	}
	var where []string
	args := []any{}
	switch filter {
	case "needs_answer", "":
		where = append(where, "t.status IN ('open', 'waiting')")
	case "answered", "closed":
		args = append(args, filter)
		where = append(where, "t.status = $1")
	}
	if q = strings.TrimSpace(q); q != "" {
		args = append(args, "%"+strings.ToLower(strings.TrimPrefix(q, "@"))+"%")
		n := len(args)
		where = append(where, "(lower(u.user_id) LIKE $"+strconv.Itoa(n)+" OR lower(COALESCE(u.username, '')) LIKE $"+strconv.Itoa(n)+" OR lower(COALESCE(u.telegram_username, '')) LIKE $"+strconv.Itoa(n)+")")
	}
	cond := ""
	if len(where) > 0 {
		cond = "WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, limit, offset)
	rows, err := db.DB.QueryContext(ctx, `
		SELECT t.id, t.user_id, t.status, t.created_at, t.updated_at, t.last_user_at, t.last_admin_at,
		       u.user_id, COALESCE(u.username, ''), COALESCE(u.telegram_username, ''),
		       (SELECT COUNT(*) FROM support_messages m WHERE m.ticket_id = t.id AND m.author = 'user' AND m.read_at IS NULL),
		       COALESCE((SELECT CASE WHEN m.body = '' THEN '[картинка]' ELSE m.body END FROM support_messages m WHERE m.ticket_id = t.id ORDER BY m.id DESC LIMIT 1), ''),
		       COALESCE((SELECT m.author FROM support_messages m WHERE m.ticket_id = t.id ORDER BY m.id DESC LIMIT 1), ''),
		       (SELECT COUNT(*) FROM support_messages m WHERE m.ticket_id = t.id)
		  FROM support_tickets t JOIN users u ON u.id = t.user_id
		 `+cond+`
		 ORDER BY t.updated_at DESC
		 LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, counts, err
	}
	defer rows.Close()
	out := []SupportTicketRow{}
	for rows.Next() {
		var r SupportTicketRow
		var lu, la sql.NullTime
		if err := rows.Scan(&r.ID, &r.UserDBID, &r.Status, &r.CreatedAt, &r.UpdatedAt, &lu, &la,
			&r.UserID, &r.Username, &r.TGUsername, &r.Unread, &r.LastBody, &r.LastAuthor, &r.MessageCount); err != nil {
			return nil, counts, err
		}
		if lu.Valid {
			r.LastUserAt = &lu.Time
		}
		if la.Valid {
			r.LastAdminAt = &la.Time
		}
		if rr := []rune(r.LastBody); len(rr) > 140 {
			r.LastBody = string(rr[:140]) + "…"
		}
		out = append(out, r)
	}
	return out, counts, rows.Err()
}

// SupportTicketDetail is one ticket with its messages and the person.
type SupportTicketDetail struct {
	Ticket   SupportTicket    `json:"ticket"`
	Messages []SupportMessage `json:"messages"`
	User     map[string]any   `json:"user"`
}

func SupportGetTicket(ctx context.Context, id int) (*SupportTicketDetail, error) {
	t, err := scanTicket(db.DB.QueryRowContext(ctx, `SELECT `+ticketCols+` FROM support_tickets WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSupportNoTicket
	}
	if err != nil {
		return nil, err
	}
	msgs, err := scanSupportMessages(db.DB.QueryContext(ctx, `SELECT `+supportMsgCols+` FROM support_messages WHERE ticket_id = $1 ORDER BY id`, id))
	if err != nil {
		return nil, err
	}
	var userID, username, tgUser, emailAddr string
	var tgLinked, blocked bool
	if err := db.DB.QueryRowContext(ctx, `
		SELECT user_id, COALESCE(username, ''), COALESCE(telegram_username, ''), telegram_id IS NOT NULL,
		       CASE WHEN email_verified_at IS NOT NULL THEN COALESCE(email, '') ELSE '' END, COALESCE(is_blocked, FALSE)
		  FROM users WHERE id = $1`, t.UserDBID).Scan(&userID, &username, &tgUser, &tgLinked, &emailAddr, &blocked); err != nil {
		return nil, err
	}
	return &SupportTicketDetail{Ticket: *t, Messages: msgs, User: map[string]any{
		"id": t.UserDBID, "user_id": userID, "username": username, "telegram_username": tgUser, "telegram_linked": tgLinked,
		"email": emailAddr, "is_blocked": blocked,
	}}, nil
}

// SupportMarkReadByAdmin marks the person's messages in a ticket as read.
func SupportMarkReadByAdmin(ctx context.Context, id int) error {
	_, err := db.DB.ExecContext(ctx, `UPDATE support_messages SET read_at = NOW() WHERE ticket_id = $1 AND author = 'user' AND read_at IS NULL`, id)
	return err
}

// SupportSetStatus closes or reopens a ticket.
func SupportSetStatus(ctx context.Context, id int, closed bool) error {
	q := `UPDATE support_tickets SET status = 'closed', updated_at = NOW() WHERE id = $1`
	if !closed {
		q = `UPDATE support_tickets SET status = CASE WHEN last_admin_at IS NOT NULL AND last_admin_at >= COALESCE(last_user_at, last_admin_at) THEN 'answered' ELSE 'open' END, updated_at = NOW() WHERE id = $1`
	}
	res, err := db.DB.ExecContext(ctx, q, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrSupportNoTicket
	}
	return nil
}

// ── the owner's alerts chat ──

// UserLabel reads the HiEarth ID and Telegram username of a user (for the owner's notices).
func UserLabel(ctx context.Context, userDBID int, userID, tgUsername *string) error {
	return db.DB.QueryRowContext(ctx, `SELECT user_id, COALESCE(telegram_username, '') FROM users WHERE id = $1`, userDBID).Scan(userID, tgUsername)
}

func SupportLinkOwnerMessage(ctx context.Context, chatID string, messageID int64, ticketID int) error {
	_, err := db.DB.ExecContext(ctx, `
		INSERT INTO support_owner_msgs (alert_chat_id, alert_message_id, ticket_id) VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING`, chatID, messageID, ticketID)
	return err
}

// SupportTicketByOwnerMessage maps a reply in the owner's chat to its ticket (0 when unknown).
func SupportTicketByOwnerMessage(ctx context.Context, chatID string, messageID int64) (int, error) {
	var id int
	err := db.DB.QueryRowContext(ctx, `SELECT ticket_id FROM support_owner_msgs WHERE alert_chat_id = $1 AND alert_message_id = $2`, chatID, messageID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}
