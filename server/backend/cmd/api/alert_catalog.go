package main

import (
	"slices"
	"strings"
)

// ─────────────── The alert catalogue ───────────────
// An alert nobody can read is noise. Every key the code can raise has an entry here: what it
// means for users, what usually causes it, and which actions make sense (action ids of the
// console — «Операции»; in phase 1 they are shown as text, the buttons come later).
// TestAlertInfoCoversAllKeys scans the code and fails on a key without an entry.
//
// inhibitedBy is the other half of "less noise": an alert whose cause is already reported is
// kept in the journal and not sent.

// alertInfo explains one alert.
type alertInfo struct {
	Meaning string   // what is happening, in the user's terms
	Usual   string   // what usually causes it
	Actions []string // action ids that make sense, most useful first
}

// alertEntry matches a key exactly (Key), or by Prefix and Suffix around a changing part
// (an id, a line, a way). The most specific match wins.
type alertEntry struct {
	Key, Prefix, Suffix string
	Info                alertInfo
}

// flapSuffix marks the «нестабильно» twin of an alert (alert_flap.go).
const flapSuffix = "~unstable"

var alertCatalog = []alertEntry{
	// ── the Hub and its database ──
	{Key: "db-unreachable", Info: alertInfo{
		Meaning: "API не может достучаться до PostgreSQL: сайт, кабинет, подписка и оплаты не работают. Уже открытые соединения VPN живы.",
		Usual:   "PostgreSQL упал или завис; кончилось место на диске; исчерпаны соединения.",
		Actions: []string{"hub.status", "hub.restart-api"}}},
	{Key: "entry-unreachable-external", Info: alertInfo{
		Meaning: "Адрес хаба не открывается из-за рубежа: ни сайт, ни рукопожатие Берёзы (взгляд через выход).",
		Usual:   "nginx на хабе; провайдер или транзит фильтрует адрес хаба.",
		Actions: []string{"probes.run", "hub.reload-nginx", "hub.address"}}},
	{Key: "entry-unreachable-ru", Info: alertInfo{
		Meaning: "Адрес хаба не открывается с зондов в домашних сетях РФ.",
		Usual:   "Адрес блокируют операторы; реже — сбой сервиса проверок.",
		Actions: []string{"entry.pathcheck", "hub.address"}}},
	{Key: "site-unreachable-ru", Info: alertInfo{
		Meaning: "Сайт не открывается из домашних сетей РФ: люди не могут войти в кабинет и оплатить.",
		Usual:   "Адрес хаба блокируют операторы; DNS сайта.",
		Actions: []string{"entry.pathcheck", "hub.address"}}},
	{Key: "subdomain-unreachable-ru", Info: alertInfo{
		Meaning: "Домен подписки не открывается из домашних сетей РФ: приложения не обновят подписку и не получат новые пути.",
		Usual:   "Вход, на который указывает домен, недоступен; запись DNS указывает на один вход.",
		Actions: []string{"entry.pathcheck", "entry.rotation-off"}}},
	{Key: "hysteria-auth-listener", Info: alertInfo{
		Meaning: "Сервер Бамбука не может спросить API, пускать ли пользователя: новые входы в Бамбук не проходят.",
		Usual:   "API не слушает служебный порт; страж исходящих соединений.",
		Actions: []string{"hub.restart-api"}}},
	{Key: "bamboo-unreachable-external", Info: alertInfo{
		Meaning: "Бамбук не отвечает снаружи: строки Бамбука не подключаются.",
		Usual:   "Сервер Бамбука на хабе; правило проброса портов; мёртвый выход, к которому он привязан.",
		Actions: []string{"probes.run", "hub.restart-bamboo"}}},
	{Key: "bamboo-probe-blind", Info: alertInfo{
		Meaning: "Проба Бамбука не может выполниться — о Бамбуке сейчас ничего не известно.",
		Usual:   "Нет живого выхода, через который смотреть; не читаются настройки.",
		Actions: []string{"exit.verify"}}},
	{Key: "tg-webproxy-readyz", Info: alertInfo{
		Meaning: "WEB-прокси Telegram не готов: ссылка на прокси у пользователей не работает.",
		Usual:   "MTProxy на выходе; туннель к выходу.",
		Actions: []string{"exit.verify"}}},
	{Key: "ddos-signals", Info: alertInfo{
		Meaning: "Похоже на атаку: растёт таблица соединений, счётчики защиты или отказы API.",
		Usual:   "Атака на адрес хаба; сканеры.",
		Actions: []string{"hub.status"}}},

	// ── xray ──
	{Key: "xray-restart-storm", Info: alertInfo{
		Meaning: "xray перезапускался слишком часто. Каждый перезапуск рвёт соединения всех пользователей.",
		Usual:   "Отсечка отключённых пользователей в режиме «сразу»; частые структурные изменения конфига; падения xray.",
		Actions: []string{"settings.xray_revoke_restart"}}},
	{Key: "xray-live-apply", Info: alertInfo{
		Meaning: "Изменение не удалось применить к работающему xray без перезапуска.",
		Usual:   "xray API не отвечает; конфиг расходится с тем, что в памяти.",
		Actions: []string{"hub.status"}}},
	{Key: "xray-sync-failed", Info: alertInfo{
		Meaning: "Новый конфиг xray не применён: новые и оплатившие пользователи, смена выходов не доходят до xray.",
		Usual:   "xray отверг конфиг на проверке; не читается база.",
		Actions: []string{"hub.status"}}},
	{Key: "revoked-still-online", Info: alertInfo{
		Meaning: "Отключённые пользователи (квота, блокировка, конец подписки) держат соединения, открытые до отключения, дольше суток. Новые соединения им закрыты.",
		Usual:   "Долгоживущая сессия: приложение не переподключалось.",
		Actions: []string{"hub.restart-xray", "settings.xray_revoke_restart"}}},
	{Key: "quota-cutoff-failopen", Info: alertInfo{
		Meaning: "Отключение по квоте трафика не работает: превысившие квоту продолжают пользоваться.",
		Usual:   "Не читается статистика трафика или настройка.",
		Actions: []string{"hub.status"}}},

	// ── exits ──
	{Key: "all-exits-down", Info: alertInfo{
		Meaning: "Ни один выход не проходит проверку: трафик пользователей никуда не уходит.",
		Usual:   "Связность хаба с зарубежьем; реже — выходы упали одновременно.",
		Actions: []string{"exit.verify", "probes.run"}}},
	{Prefix: "exit-down-", Info: alertInfo{
		Meaning: "Выход не проходит проверку: оба пути к нему мертвы. Пользователи его тарифов идут через остальные выходы.",
		Usual:   "Сервер выхода или его провайдер; фильтрация между хабом и выходом.",
		Actions: []string{"exit.verify", "exit.reprovision", "exit.out-of-service"}}},
	{Prefix: "exit-path-down-", Info: alertInfo{
		Meaning: "Один из двух путей к выходу не работает; выход обслуживается по второму, запаса нет.",
		Usual:   "UDP до выхода режется (основной путь) или сервис запасного пути на выходе.",
		Actions: []string{"exit.verify", "exit.reprovision"}}},

	// ── entries ──
	{Prefix: "relay-", Info: alertInfo{
		Meaning: "Проблема с резервным входом.",
		Usual:   "Сервер входа или его провайдер.",
		Actions: []string{"entry.pathcheck", "entry.rotation-off"}}},
	{Prefix: "relay-", Suffix: "-unreachable-external", Info: alertInfo{
		Meaning: "Резервный вход не открывается снаружи: мобильные пользователи, идущие через него, не подключаются.",
		Usual:   "Сервер входа; путь между входом и хабом; его провайдер.",
		Actions: []string{"entry.pathcheck", "entry.rotation-off"}}},
	{Prefix: "relay-", Suffix: "-bamboo", Info: alertInfo{
		Meaning: "Бамбук через резервный вход не отвечает; Берёза и Дуб через него могут работать.",
		Usual:   "Проброс UDP на входе; путь UDP между входом и хабом.",
		Actions: []string{"entry.pathcheck"}}},
	{Prefix: "relay-", Suffix: "-unstable", Info: alertInfo{
		Meaning: "Вход теряет часть подключений: у части пользователей соединение не устанавливается или зависает.",
		Usual:   "Транзит провайдера входа теряет отдельные потоки, а не сам сервер.",
		Actions: []string{"entry.pathcheck", "entry.rotation-off"}}},
	{Prefix: "relay-", Suffix: "-unreachable-ru", Info: alertInfo{
		Meaning: "Резервный вход не открывается с зондов в домашних сетях РФ.",
		Usual:   "Адрес входа блокируют операторы.",
		Actions: []string{"entry.pathcheck", "entry.rotation-off"}}},
	{Prefix: "entry-failover-", Info: alertInfo{
		Meaning: "Основной вход недоступен снаружи: подписка переключена на резервный вход.",
		Usual:   "Адрес хаба фильтруется; nginx на хабе.",
		Actions: []string{"probes.run", "subscription.urgent"}}},
	{Key: "entries-probe-blind", Info: alertInfo{
		Meaning: "Проверка входов снаружи не может выполниться — о входах сейчас ничего не известно, автоподмена не сработает.",
		Usual:   "Нет живого выхода, через который смотреть.",
		Actions: []string{"exit.verify"}}},
	{Key: "entry-failover-no-reserve", Info: alertInfo{
		Meaning: "Основной вход недоступен снаружи, а резервного входа в ротации нет: переключать подписку некуда.",
		Usual:   "Все входы выведены из ротации или не проходят проверку.",
		Actions: []string{"entry.rotation-test", "hub.address"}}},

	// ── the synthetic client ──
	{Key: "synth-all-ways", Info: alertInfo{
		Meaning: "Ни через один путь основной строки подписки данные не идут: рукопожатие может проходить, но страницы у пользователей не открываются.",
		Usual:   "Последнее изменение подписки или сети; транзит между входом и хабом; выходы.",
		Actions: []string{"probes.run", "subscription.rollback", "subscription.urgent"}}},
	{Key: "synthetic-blind", Info: alertInfo{
		Meaning: "Синтетическая проверка ничего не видит: о строках подписки сейчас нет данных, тревоги по ним не поднимаются и не снимаются.",
		Usual:   "Нет живого выхода, через который смотреть; у тестового пользователя нет подписки со всеми строками; ядро не запускается.",
		Actions: []string{"probes.run", "exit.verify"}}},
	{Prefix: settingOutsideAlertPrefix, Info: alertInfo{
		Meaning: "Настройка изменена не через пульт: SQL или другой инструмент. Изменение не прошло ни канарейку, ни аудит.",
		Usual:   "Ручная правка в базе.",
		Actions: []string{"subscription.rollback", "hub.status"}}},
	{Prefix: "synth-way:", Info: alertInfo{
		Meaning: "Через этот путь подписки данные не идут: объект в 64 КБ не скачивается целиком, хотя соединение может устанавливаться.",
		Usual:   "Вход или транзит его провайдера теряет потоки; реже — хаб или выход.",
		Actions: []string{"entry.pathcheck", "entry.rotation-off", "probes.run"}}},
	{Prefix: "synth-line:", Info: alertInfo{
		Meaning: "Строка подписки не проходит проверку глазами клиента.",
		Usual:   "Последнее изменение подписки.",
		Actions: []string{"probes.run", "subscription.rollback"}}},
	{Prefix: "synth-line:", Suffix: "/blocked", Info: alertInfo{
		Meaning: "Через строку подписки не открывается сайт, заблокированный в РФ: имя не уходит в туннель или не резолвится.",
		Usual:   "Изменение маршрутизации «по имени» или секции dns в подписке.",
		Actions: []string{"probes.run", "subscription.rollback", "subscription.urgent"}}},
	{Prefix: "synth-line:", Suffix: "/ru", Info: alertInfo{
		Meaning: "Через строку подписки не открывается российский сайт: прямой путь не работает.",
		Usual:   "Изменение списков прямых адресов или маршрутизации «по имени».",
		Actions: []string{"probes.run", "subscription.rollback"}}},
	{Prefix: "synth-line:", Suffix: "/lint", Info: alertInfo{
		Meaning: "Подписка собрана с ошибкой, которую видно без запуска: например, «по имени» без секции dns — в сетях с подменой DNS заблокированные сайты не откроются.",
		Usual:   "Флаг подписки включён всем без парного ему флага.",
		Actions: []string{"subscription.rollback", "subscription.urgent"}}},

	// ── housekeeping ──
	{Key: "tls-cert-expiry", Info: alertInfo{
		Meaning: "Сертификат сайта скоро истечёт: сайт, кабинет и подписка перестанут открываться.",
		Usual:   "Автообновление сертификата не проходит: порт 80, DNS.",
		Actions: []string{"hub.status"}}},
	{Key: "tls-cert-check-failed", Info: alertInfo{
		Meaning: "Сертификат сайта не удалось прочитать: срок его действия неизвестен.",
		Usual:   "Сайт не отвечает на 443 изнутри хаба.",
		Actions: []string{"hub.status"}}},
	{Key: "backup-stale", Info: alertInfo{
		Meaning: "Свежего бэкапа нет больше суток: при поломке базы потеряется больше дня.",
		Usual:   "Таймер бэкапа не отработал; нет места на диске.",
		Actions: []string{"backup.run"}}},
	{Key: "offsite-backup-stale", Info: alertInfo{
		Meaning: "Копия бэкапа вне хаба не обновлялась: если хаб пропадёт целиком, восстановиться не из чего.",
		Usual:   "Выход, на который уходит копия, недоступен; сменился его ключ.",
		Actions: []string{"backup.run", "exit.verify"}}},
	{Key: "backup-verify-failed", Info: alertInfo{
		Meaning: "Еженедельное учебное восстановление не прошло: бэкапы создаются, но восстановиться из последнего может не получиться.",
		Usual:   "Повреждённый или неполный дамп; не хватило места для временной базы.",
		Actions: []string{"backup.run", "hub.status"}}},
	{Key: "domain-expiry", Info: alertInfo{
		Meaning: "Заканчивается оплата домена. Если его не продлить, сайт, кабинет, подписка и оплаты перестанут открываться у всех.",
		Usual:   "Автопродление у регистратора выключено или на счёте нет денег.",
		Actions: []string{}}},
	{Key: "reboot-pending", Info: alertInfo{
		Meaning: "Обновления ядра установлены, но хаб не перезагружали больше двух недель: исправления безопасности не действуют.",
		Usual:   "Перезагрузка хаба рвёт соединения всех, поэтому её откладывают.",
		Actions: []string{"node.reboot"}}},
	{Key: "disk-usage", Info: alertInfo{
		Meaning: "Диск хаба заполнен больше чем на 85 %: при заполнении остановятся база и API.",
		Usual:   "Журналы и бэкапы.",
		Actions: []string{"hub.status"}}},
	{Key: "capacity-buy-more", Info: alertInfo{
		Meaning: "Нагрузка подходит к пределу серверов: пора докупить выход.",
		Usual:   "Рост числа пользователей.",
		Actions: []string{"servers.capacity"}}},
	{Key: "ssh-encryption-key", Info: alertInfo{
		Meaning: "На хабе не задан ключ шифрования SSH-паролей: задания на узлах не запускаются.",
		Usual:   "Потерян api.env или ключ в нём.",
		Actions: []string{"hub.status"}}},
	{Key: "ssh-ciphertexts-unreadable", Info: alertInfo{
		Meaning: "Сохранённые SSH-пароли узлов не расшифровываются: задания на этих узлах не запустятся.",
		Usual:   "Ключ шифрования сменился.",
		Actions: []string{"hub.status"}}},
	{Key: "hub-address-netplan", Info: alertInfo{
		Meaning: "После отката смены адреса хаба сетевой конфиг не вернулся на прежний источник: после перезагрузки хаб может выйти в сеть не с того адреса.",
		Usual:   "Ошибка на шаге отката мастера адреса.",
		Actions: []string{"hub.address"}}},
	{Key: "hub-address-revert", Info: alertInfo{
		Meaning: "Смена адреса хаба откатывается или откат не удался.",
		Usual:   "Новый адрес не подтвердился за отведённое время.",
		Actions: []string{"hub.address"}}},

	// ── money ──
	{Prefix: "payment-activation-failed-", Info: alertInfo{
		Meaning: "Оплата получена, но подписка не выдана: человек заплатил и остался без доступа.",
		Usual:   "Ошибка при покупке тарифа с кошелька.",
		Actions: []string{"payments.open"}}},
	{Prefix: "payment-unapplied-", Info: alertInfo{
		Meaning: "Платёж прошёл у ЮKassa, но не зачислен на кошелёк.",
		Usual:   "Не дошло уведомление о платеже; ошибка зачисления.",
		Actions: []string{"payments.open"}}},
	{Prefix: "wallet-purchase-failed-", Info: alertInfo{
		Meaning: "Кошелёк пополнен, но тариф с него не куплен: человек заплатил и остался без доступа.",
		Usual:   "Тариф изменился или отключён между созданием платежа и его зачислением.",
		Actions: []string{"payments.open"}}},
	{Prefix: "wallet-receipt-failed-", Info: alertInfo{
		Meaning: "Чек зачёта предоплаты не отправлен в ЮKassa: покупка состоялась, но закрывающего чека нет.",
		Usual:   "ЮKassa отклонила чек или недоступна.",
		Actions: []string{"payments.open"}}},
	{Prefix: "stars-payment-", Info: alertInfo{
		Meaning: "Оплата звёздами в Telegram не прошла до конца: звёзды списаны, подписка не выдана, или платёж завис.",
		Usual:   "Ошибка выдачи подписки после оплаты.",
		Actions: []string{"payments.open"}}},
	{Prefix: "refund-request-", Info: alertInfo{
		Meaning: "Пользователь просит вернуть деньги.",
		Usual:   "—",
		Actions: []string{"payments.open"}}},
	{Prefix: "referral-refunded-", Info: alertInfo{
		Meaning: "Возвращён платёж, за который уже выплачен бонус «приведи друга».",
		Usual:   "—",
		Actions: []string{"payments.open"}}},
	{Prefix: "wallet-external-refund-", Info: alertInfo{
		Meaning: "Возврат пополнения больше остатка на кошельке: баланс ушёл бы в минус.",
		Usual:   "Возврат сделан в кабинете ЮKassa, а деньги уже потрачены.",
		Actions: []string{"payments.open"}}},
}

// alertInfoFor finds the entry of key: an exact one first, then the most specific
// prefix/suffix pair. The «нестабильно» twin of an alert builds on the alert's own entry.
// An unknown key gets the empty info.
func alertInfoFor(key string) alertInfo {
	if base, ok := strings.CutSuffix(key, flapSuffix); ok {
		info := alertInfoFor(base)
		if info.Meaning == "" {
			return info
		}
		info.Meaning = "Нестабильно: тревога поднималась три раза за шесть часов. " + info.Meaning
		info.Usual = "Причина появляется и пропадает: частичные потери, перегрузка, мигающая связность. " + info.Usual
		return info
	}
	best, bestScore := alertInfo{}, -1
	for _, e := range alertCatalog {
		if e.Key != "" {
			if e.Key == key {
				return e.Info
			}
			continue
		}
		if len(key) <= len(e.Prefix)+len(e.Suffix) || !strings.HasPrefix(key, e.Prefix) || !strings.HasSuffix(key, e.Suffix) {
			continue
		}
		if score := len(e.Prefix) + len(e.Suffix); score > bestScore {
			best, bestScore = e.Info, score
		}
	}
	return best
}

// dbDependentKeys and dbDependentPrefixes: the alerts «база недоступна» explains — their check
// reads or writes the database and fails with it. Everything else (the disk, certificates, the
// exits, the entries, Бамбук from outside) says something of its own and is sent as usual: the
// VPN lives through a database outage, and a dead exit during one is news.
var (
	dbDependentKeys = []string{"xray-sync-failed", quotaFailOpenAlertKey, synthBlindAlert, bambooBlindAlertKey, entryBlindAlert,
		"backup-stale", "offsite-backup-stale", "backup-verify-failed", capacityAlertKey, sshCiphertextsAlertKey}
	dbDependentPrefixes = []string{settingOutsideAlertPrefix, "payment-", "wallet-", "stars-payment-", "refund-request-", "referral-"}
)

func dbDependent(key string) bool {
	if slices.Contains(dbDependentKeys, key) {
		return true
	}
	for _, p := range dbDependentPrefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

// inhibitedBy returns the active alert that already explains key — its cause — or "".
// active reports whether an alert is active right now.
//
//	db-unreachable              mutes what cannot work without the database (dbDependent)
//	all-exits-down              mutes exit-down-*, exit-path-down-*, synth-*, synthetic-blind, bamboo-*, tg-webproxy-readyz
//	entry-unreachable-external  mutes bamboo-unreachable-external, synth-*, relay-*
//	exit-down-<N>               mutes exit-path-down-<N>-*
//	synth-all-ways              mutes synth-way:*, synth-line:*
func inhibitedBy(key string, active func(string) bool) string {
	key = strings.TrimSuffix(key, flapSuffix)
	has := func(prefixes ...string) bool {
		for _, p := range prefixes {
			if strings.HasPrefix(key, p) {
				return true
			}
		}
		return false
	}
	if key != dbAlertKey && dbDependent(key) && active(dbAlertKey) {
		return dbAlertKey
	}
	const allExits, entryExternal = "all-exits-down", "entry-unreachable-external"
	if key != allExits && active(allExits) &&
		(has("exit-down-", "exit-path-down-", "synth-", "bamboo-") || key == synthBlindAlert || key == tgWebAlertKey) {
		return allExits
	}
	if key != entryExternal && active(entryExternal) && (key == bambooAlertKey || has("synth-", "relay-")) {
		return entryExternal
	}
	if rest, ok := strings.CutPrefix(key, "exit-path-down-"); ok {
		if id, _, _ := strings.Cut(rest, "-"); id != "" && active("exit-down-"+id) {
			return "exit-down-" + id
		}
	}
	if has("synth-way:", "synth-line:") && active(synthAllWaysAlert) {
		return synthAllWaysAlert
	}
	return ""
}
