package provision

import "context"

// activateApply is the Apply of a setup recipe's last step, "activate" (Task 2.8 recipes use it
// as is): the node goes online AND healthy in one write (Hooks.Activate) — both paths were just
// checked by this job, so the Xray generator sees the exit once: exactly one restart. Once the
// node is online, a failed subscription bump or sync request only warns: the job did its part
// (failing it now would mark an online node's job failed), and the periodic sync catches up.
func activateApply(ctx context.Context, _ Remote, s *State) error {
	if err := s.Hooks.Activate(ctx, s.Node.ID); err != nil {
		return err
	}
	if s.Hooks.BumpSubscription != nil {
		if err := protect(func() error { return s.Hooks.BumpSubscription(ctx) }); err != nil {
			s.warnf("версия подписки не увеличена (%v) — клиенты узнают о новом выходе при следующем изменении", err)
		}
	}
	if s.Hooks.RequestSync != nil {
		if err := protect(func() error { s.Hooks.RequestSync(); return nil }); err != nil {
			s.warnf("синхронизация Xray не запрошена (%v) — выход подхватит периодическая синхронизация", err)
		}
	}
	s.Printf("нода включена в маршрутизацию; Xray пересоберёт конфигурацию один раз")
	return nil
}
