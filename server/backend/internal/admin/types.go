package admin

import (
	"database/sql"
	"encoding/json"
	"time"
)

type dashboardOverview struct {
	UsersCount         int64 `json:"users_count"`
	ActiveUsersCount   int64 `json:"active_users_count"`
	ServersCount       int64 `json:"servers_count"`
	OnlineServersCount int64 `json:"online_servers_count"`
	GroupsCount        int64 `json:"groups_count"`
	PromocodesCount    int64 `json:"promocodes_count"`
	PaymentsCount      int64 `json:"payments_count"`
	ActiveReleases     int64 `json:"active_releases"`
	AdminsCount        int64 `json:"admins_count"`
}

type adminUserRow struct {
	ID                int           `json:"id"`
	UserID            string        `json:"user_id"`
	Username          string        `json:"username"`
	TelegramID        sql.NullInt64 `json:"-"`
	Role              string        `json:"role"`
	IsBlocked         bool          `json:"is_blocked"`
	PreferredPlatform string        `json:"preferred_platform"`
	CreatedAt         time.Time     `json:"-"`
	UpdatedAt         time.Time     `json:"-"`
	CurrentPlan       string        `json:"current_plan"`
	ValidUntil        sql.NullTime  `json:"-"`
}

type userPayload struct {
	UserID            string `json:"user_id"`
	TelegramID        *int64 `json:"telegram_id"`
	Role              string `json:"role"`
	IsBlocked         *bool  `json:"is_blocked"`
	PreferredPlatform string `json:"preferred_platform"`
}

type groupResponse struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	IsPremium   bool   `json:"is_premium"`
	CompanyID   *int   `json:"company_id,omitempty"`
	UpdatedAt   string `json:"updated_at"`
}

type groupPayload struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	IsPremium   bool   `json:"is_premium"`
	CompanyID   *int   `json:"company_id"`
}

type subscriptionResponse struct {
	UserID     int    `json:"user_db_id"`
	UserKey    string `json:"user_id"`
	GroupID    int    `json:"group_id"`
	GroupName  string `json:"group_name"`
	ValidUntil string `json:"valid_until"`
	AutoRenew  bool   `json:"auto_renew"`
}

type subscriptionPayload struct {
	UserID     int    `json:"user_db_id"`
	GroupID    int    `json:"group_id"`
	ValidUntil string `json:"valid_until"`
	AutoRenew  bool   `json:"auto_renew"`
}

type promocodeResponse struct {
	Code         string `json:"code"`
	GroupID      int    `json:"group_id_granted"`
	GroupName    string `json:"group_name"`
	DurationDays int    `json:"duration_days"`
	MaxUses      int    `json:"max_uses"`
	CurrentUses  int    `json:"current_uses"`
	ExpiresAt    string `json:"expires_at"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

type promocodePayload struct {
	Code         string `json:"code"`
	GroupID      int    `json:"group_id_granted"`
	DurationDays int    `json:"duration_days"`
	MaxUses      int    `json:"max_uses"`
	ExpiresAt    string `json:"expires_at"`
}

type paymentResponse struct {
	ID          int             `json:"id"`
	UserDBID    int             `json:"user_db_id"`
	UserID      string          `json:"user_id"`
	Amount      float64         `json:"amount"`
	Currency    string          `json:"currency"`
	Status      string          `json:"status"`
	Provider    string          `json:"provider"`
	ExternalID  string          `json:"external_id"`
	Notes       string          `json:"notes"`
	Metadata    json.RawMessage `json:"metadata"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
	ConfirmedAt string          `json:"confirmed_at"`
}

type paymentPayload struct {
	UserDBID   int             `json:"user_db_id"`
	Amount     float64         `json:"amount"`
	Currency   string          `json:"currency"`
	Status     string          `json:"status"`
	Provider   string          `json:"provider"`
	ExternalID string          `json:"external_id"`
	Notes      string          `json:"notes"`
	Metadata   json.RawMessage `json:"metadata"`
}

type serverResponse struct {
	ID          int             `json:"id"`
	Name        string          `json:"name"`
	IPAddress   string          `json:"ip_address"`
	SSHPort     int             `json:"ssh_port"`
	SSHUser     string          `json:"ssh_user"`
	SSHPassword string          `json:"ssh_password,omitempty"`
	SshKeyID    *int            `json:"ssh_key_id,omitempty"`
	SshKeyName  string          `json:"ssh_key_name,omitempty"`
	CountryCode string          `json:"country_code"`
	Status      string          `json:"status"`
	LoadMetrics json.RawMessage `json:"load_metrics"`
	GroupIDs    []int           `json:"group_ids"`
	UpdatedAt   string          `json:"updated_at"`
}

type serverPayload struct {
	Name        string `json:"name"`
	IPAddress   string `json:"ip_address"`
	SSHPort     int    `json:"ssh_port"`
	SSHUser     string `json:"ssh_user"`
	SSHPassword string `json:"ssh_password"`
	SshKeyID    *int   `json:"ssh_key_id"`
	CountryCode string `json:"country_code"`
	Status      string `json:"status"`
	GroupIDs    []int  `json:"group_ids"`
}

type serverActionPayload struct {
	Action string `json:"action"`
}
