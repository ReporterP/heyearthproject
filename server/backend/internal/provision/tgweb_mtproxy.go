package provision

import (
	"context"
	"errors"

	"github.com/heyearth/backend/internal/config"
)

// tg_mtproxy: the official MTProxy built from the pinned commit, Telegram's proxy-secret and
// proxy-multi.conf in place, the mtproxy user present.
var tgMTProxyCheckSh = `[ -x ` + config.TGMTProxyBinary + ` ] && grep -Fxq ` + config.MTProxyCommit + ` ` + config.TGMTProxyDir + `/.tproxy-commit 2>/dev/null && echo BUILT=1
[ -s ` + config.TGMTProxyEtc + `/proxy-secret ] && [ -s ` + config.TGMTProxyEtc + `/proxy-multi.conf ] && echo FILES=1
id mtproxy >/dev/null 2>&1 && echo USER=1
true
`

func tgMTProxyVerify(ctx context.Context, r Remote, _ *State) error {
	kv, err := runKV(ctx, r, tgMTProxyCheckSh)
	if err != nil {
		return err
	}
	switch {
	case kv["USER"] != "1":
		return errors.New("нет пользователя mtproxy")
	case kv["BUILT"] != "1":
		return errf("MTProxy не собран из %s", config.MTProxyCommit[:12])
	case kv["FILES"] != "1":
		return errors.New("нет proxy-secret / proxy-multi.conf от Telegram")
	}
	return nil
}

func tgMTProxyApply(ctx context.Context, r Remote, s *State) error {
	bctx, cancel := context.WithTimeout(ctx, TGWebMTProxyBuildTimeout)
	defer cancel()
	out, err := r.Run(bctx, config.TGWebMTProxyBuildScript)
	s.Printf("%s", tail(out, 2000))
	return err
}
