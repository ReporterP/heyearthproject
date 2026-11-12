package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/heyearth/backend/internal/biz"
)

// validateRelayCreate normalizes the wizard's create request of a reserve entry (role 'relay'):
// it lives in Russia and has none of the exit-only options.
func validateRelayCreate(country string, planIDs []int, mtproxy bool, loadWeight int) (string, error) {
	if c := strings.ToUpper(strings.TrimSpace(country)); c != "" && c != "RU" {
		return "", errors.New("резервный вход ставится в РФ: страна должна быть RU")
	}
	if len(planIDs) > 0 || mtproxy || loadWeight > 1 {
		return "", errors.New("у резервного входа нет тарифов, MTProxy и приоритета — это опции выхода за рубежом")
	}
	return "RU", nil
}

// validateRelaySSHPort: the relay forwards TCP 443 and 2443 to the Hub, so SSH on one of them
// would be forwarded too — the Hub could never reach the relay again.
func validateRelaySSHPort(port int) error {
	if port == 443 || port == 2443 {
		return fmt.Errorf("SSH на порту %d не подойдёт: резервный вход пересылает 443 и 2443 на Hub. Оставьте 22 или выберите другой порт", port)
	}
	return nil
}

var ErrRelayInUse = errors.New("этот резервный вход сейчас используется: на него ведут основные строки подписки. Сначала во «Входах» переключите пользователей на Hub")

var relayFailoverTarget = biz.EntryIsFailoverTarget

// relayDeprovisionGuard refuses to take away the relay the main subscription lines point at.
func relayDeprovisionGuard(ctx context.Context, id int) error {
	inUse, err := relayFailoverTarget(ctx, id)
	if err != nil {
		return fmt.Errorf("состояние переключения входов не прочитано: %w", err)
	}
	if inUse {
		return ErrRelayInUse
	}
	return nil
}

// relayRoutingMsg: a relay forwards every user to the Hub; plans and priority belong to exits.
const relayRoutingMsg = "У резервного входа нет тарифов и приоритета — это настройки выхода за рубежом"

// nodeRoleQuery: the role of a node ('exit' for old rows without one).
const nodeRoleQuery = `SELECT COALESCE(role, 'exit') FROM servers WHERE id = $1`
