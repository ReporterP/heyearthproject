package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/heyearth/backend/internal/pb/heyearth_v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var lastBotMessageID sync.Map // chatID (int64) -> messageID (int)

const (
	cbGetSS           = "getss"
	cbNewSS           = "newss"
	cbInstruction     = "instruction"
	cbInstPhone       = "inst_phone"
	cbInstPC          = "inst_pc"
	cbPlatformAndroid = "plat_android"
	cbPlatformWindows = "plat_windows"
	cbPlatformIOS     = "plat_ios"
	cbPlatformMacOS   = "plat_macos"
	cbDownloadApp     = "download_app"
	cbOpenInApp       = "open_in_app"
	cbGetLoginCode    = "get_login_code"
	cbProfile         = "profile"
	cbChangeDevice    = "change_device"
)

const (
	grpcBackendAddr = "127.0.0.1:50051"

	instructionPhone = `🔶 *Подключение с телефона (iOS) — через Happ*

• Скачай приложение *Happ*: App Store или https://www.happ.su/main/ru
• Нажми «Получить ссылку» в боте, скопируй ss:// и вставь в Happ.
• Включи *глобальный режим* и *«DNS через прокси»* в настройках Happ.
⚠ Ссылка — личная, никому не передавай.`

	instructionPC = `🔶 *Подключение с ПК (macOS / Linux) — через Happ*

• Скачай *Happ*: https://www.happ.su/main/ru (macOS: .dmg, Linux: .deb / .rpm)
• Нажми «Получить ссылку» в боте, скопируй ss:// и вставь в Happ.
• Включи *глобальный режим* и *«DNS через прокси»* в настройках Happ.
⚠ Ссылка — личная, никому не передавай.`

	instructionAppAndroid = `📲 *Приложение HeYearth для Android*

• Нажми кнопку «Скачать приложение».
• Затем выбери «Открыть в приложении» или введи код входа в приложении.
• Если приложение уже установлено, pairing/deep-link привяжет тебя к твоему HeYearth ID.`

	instructionAppWindows = `💻 *Приложение HeYearth для Windows*

• Нажми кнопку «Скачать приложение».
• Установи или распакуй клиент.
• Затем используй «Открыть в приложении» или «Получить код входа».`
)

type backendClient struct {
	conn      *grpc.ClientConn
	botAPI    heyearth_v1.BotApiClient
	clientAPI heyearth_v1.ClientApiClient
}

func main() {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN not set")
	}

	backend, err := newBackendClient()
	if err != nil {
		log.Fatal(err)
	}
	defer backend.conn.Close()

	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		log.Fatal(err)
	}
	bot.Debug = false

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.CallbackQuery != nil {
			handleCallback(bot, backend, &update)
			continue
		}
		if update.Message == nil {
			continue
		}

		msg := update.Message
		userID := msg.From.ID
		text := strings.TrimSpace(msg.Text)
		if text == "" {
			continue
		}

		var reply string
		var keyboard *tgbotapi.InlineKeyboardMarkup

		switch {
		case text == "/start" || text == "/register":
			profile, err := backend.ensureTelegramUser(userID, "", "ru")
			if err != nil {
				reply = "Не удалось загрузить профиль. Попробуй позже."
				break
			}
			if strings.TrimSpace(profile.GetPreferredPlatform()) == "" {
				reply = "С какого устройства будешь подключаться?"
				k := deviceChoiceKeyboard()
				keyboard = &k
			} else {
				reply = firstMessageForProfile(profile)
				k := mainKeyboardForPlatform(profile.GetPreferredPlatform())
				keyboard = &k
			}
		case text == "/instruction":
			profile, err := backend.ensureTelegramUser(userID, "", "ru")
			if err != nil {
				reply = "Не удалось загрузить профиль. Попробуй позже."
				break
			}
			platform := strings.TrimSpace(profile.GetPreferredPlatform())
			if platform == "" {
				reply = "С какого устройства подключаешься?"
				k := deviceChoiceKeyboard()
				keyboard = &k
			} else if platform == "android" || platform == "windows" {
				reply = instructionForApp(platform)
				k := mainKeyboardForPlatform(platform)
				keyboard = &k
			} else {
				reply = "С какого устройства подключаешься?"
				k := deviceKeyboard()
				keyboard = &k
			}
		case text == "/getss":
			reply, keyboard = handleLegacyConfigCommand(backend, userID, false)
		case text == "/newss":
			reply, keyboard = handleLegacyConfigCommand(backend, userID, true)
		case text == "/profile":
			profile, err := backend.ensureTelegramUser(userID, "", "ru")
			if err != nil {
				reply = "Не удалось загрузить профиль. Попробуй позже."
				break
			}
			reply = profileSummary(profile)
			k := mainKeyboardForPlatform(profile.GetPreferredPlatform())
			keyboard = &k
		case strings.HasPrefix(text, "/promo "):
			code := strings.TrimSpace(strings.TrimPrefix(text, "/promo"))
			profile, err := backend.ensureTelegramUser(userID, "", "ru")
			if err != nil {
				reply = "Не удалось загрузить профиль. Попробуй позже."
				break
			}
			ctx, cancel := contextWithTimeout()
			resp, err := backend.clientAPI.ApplyPromocode(ctx, &heyearth_v1.ApplyPromocodeRequest{
				UserId: profile.GetUserId(),
				Code:   code,
			})
			cancel()
			if err != nil {
				reply = "Не удалось применить промокод: " + err.Error()
			} else {
				reply = resp.GetMessage()
			}
			k := mainKeyboardForPlatform(profile.GetPreferredPlatform())
			keyboard = &k
		case text == "/help":
			reply = "Команды: /start, /instruction, /profile, /promo CODE. Основной вход в приложение теперь идёт через pairing из бота."
		default:
			continue
		}

		sendOrEditMsg(bot, msg.Chat.ID, reply, keyboard)
	}
}

// botPublicBaseURL returns the public HTTPS base URL for building /go/claim links (Telegram only allows http(s) in URL buttons).
func botPublicBaseURL() string {
	if v := strings.TrimSpace(os.Getenv("HEYEARTH_PUBLIC_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	if v := strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	if v := strings.TrimSpace(os.Getenv("SITE_DOMAIN")); v != "" {
		if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
			return strings.TrimRight(v, "/")
		}
		return "https://" + strings.TrimRight(v, "/")
	}
	return ""
}

func newBackendClient() (*backendClient, error) {
	conn, err := grpc.Dial(grpcBackendAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial backend grpc: %w", err)
	}
	return &backendClient{
		conn:      conn,
		botAPI:    heyearth_v1.NewBotApiClient(conn),
		clientAPI: heyearth_v1.NewClientApiClient(conn),
	}, nil
}

func contextWithTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Second)
}

func (b *backendClient) ensureTelegramUser(telegramID int64, preferredPlatform, locale string) (*heyearth_v1.UserProfileResponse, error) {
	ctx, cancel := contextWithTimeout()
	defer cancel()
	return b.botAPI.EnsureTelegramUser(ctx, &heyearth_v1.EnsureTelegramUserRequest{
		TelegramId:        telegramID,
		PreferredPlatform: preferredPlatform,
		Locale:            locale,
	})
}

func (b *backendClient) setPreferredPlatform(telegramID int64, preferredPlatform string) (*heyearth_v1.UserProfileResponse, error) {
	ctx, cancel := contextWithTimeout()
	defer cancel()
	return b.botAPI.SetPreferredPlatform(ctx, &heyearth_v1.SetPreferredPlatformRequest{
		TelegramId:        telegramID,
		PreferredPlatform: preferredPlatform,
	})
}

func (b *backendClient) createPairingCode(telegramID int64, preferredPlatform string) (*heyearth_v1.CreatePairingCodeResponse, error) {
	ctx, cancel := contextWithTimeout()
	defer cancel()
	return b.botAPI.CreatePairingCode(ctx, &heyearth_v1.CreatePairingCodeRequest{
		TelegramId:        telegramID,
		PreferredPlatform: preferredPlatform,
		Locale:            "ru",
	})
}

func (b *backendClient) getDistribution(platform string) (*heyearth_v1.GetClientDistributionResponse, error) {
	ctx, cancel := contextWithTimeout()
	defer cancel()
	return b.botAPI.GetClientDistribution(ctx, &heyearth_v1.GetClientDistributionRequest{Platform: platform})
}

func (b *backendClient) issueLegacyAppleConfig(telegramID int64, forceNew bool) (*heyearth_v1.ConnectionBundleResponse, error) {
	ctx, cancel := contextWithTimeout()
	defer cancel()
	return b.botAPI.IssueLegacyAppleConfig(ctx, &heyearth_v1.IssueLegacyAppleConfigRequest{
		TelegramId: telegramID,
		ForceNew:   forceNew,
	})
}

func handleCallback(bot *tgbotapi.BotAPI, backend *backendClient, update *tgbotapi.Update) {
	cb := update.CallbackQuery
	if cb == nil || cb.Message == nil {
		return
	}
	chatID := cb.Message.Chat.ID
	messageID := cb.Message.MessageID
	userID := cb.From.ID

	if _, err := bot.Request(tgbotapi.NewCallback(cb.ID, "")); err != nil {
		log.Printf("Callback answer: %v", err)
	}

	var reply string
	var keyboard tgbotapi.InlineKeyboardMarkup

	switch cb.Data {
	case cbPlatformAndroid:
		profile, err := backend.setPreferredPlatform(userID, "android")
		if err != nil {
			reply = "Не удалось сохранить платформу. Попробуй позже."
			break
		}
		reply = firstMessageForProfile(profile)
		keyboard = mainKeyboardForPlatform("android")
	case cbPlatformWindows:
		profile, err := backend.setPreferredPlatform(userID, "windows")
		if err != nil {
			reply = "Не удалось сохранить платформу. Попробуй позже."
			break
		}
		reply = firstMessageForProfile(profile)
		keyboard = mainKeyboardForPlatform("windows")
	case cbPlatformIOS:
		profile, err := backend.setPreferredPlatform(userID, "ios")
		if err != nil {
			reply = "Не удалось сохранить платформу. Попробуй позже."
			break
		}
		reply = firstMessageForProfile(profile)
		keyboard = mainKeyboardForPlatform("ios")
	case cbPlatformMacOS:
		profile, err := backend.setPreferredPlatform(userID, "macos")
		if err != nil {
			reply = "Не удалось сохранить платформу. Попробуй позже."
			break
		}
		reply = firstMessageForProfile(profile)
		keyboard = mainKeyboardForPlatform("macos")
	case cbGetSS:
		replyText, k := handleLegacyConfigCommand(backend, userID, false)
		reply = replyText
		if k != nil {
			keyboard = *k
		}
	case cbNewSS:
		replyText, k := handleLegacyConfigCommand(backend, userID, true)
		reply = replyText
		if k != nil {
			keyboard = *k
		}
	case cbInstruction:
		profile, err := backend.ensureTelegramUser(userID, "", "ru")
		if err != nil {
			reply = "Не удалось загрузить профиль. Попробуй позже."
			break
		}
		platform := strings.TrimSpace(profile.GetPreferredPlatform())
		if platform == "android" || platform == "windows" {
			reply = instructionForApp(platform)
			keyboard = mainKeyboardForPlatform(platform)
		} else {
			reply = "С какого устройства подключаешься?"
			keyboard = deviceKeyboard()
		}
	case cbInstPhone:
		reply = instructionPhone
		keyboard = mainKeyboardForPlatform("ios")
	case cbInstPC:
		reply = instructionPC
		keyboard = mainKeyboardForPlatform("macos")
	case cbDownloadApp:
		profile, err := backend.ensureTelegramUser(userID, "", "ru")
		if err != nil {
			reply = "Не удалось загрузить профиль. Попробуй позже."
			break
		}
		reply = downloadMessageForProfile(backend, profile)
		keyboard = mainKeyboardForPlatform(profile.GetPreferredPlatform())
	case cbOpenInApp:
		profile, err := backend.ensureTelegramUser(userID, "", "ru")
		if err != nil {
			reply = "Не удалось загрузить профиль. Попробуй позже."
			break
		}
		pairing, err := backend.createPairingCode(userID, profile.GetPreferredPlatform())
		if err != nil {
			reply = "Не удалось подготовить вход в приложение. Попробуй позже."
			break
		}
		reply = "🚀 *Открыть в приложении*\n\nНажми кнопку ниже — откроется приложение и устройство привяжется к твоему HeYearth ID.\n\nКод действителен до `" + pairing.GetExpiresAt() + "`."
		base := botPublicBaseURL()
		openURL := ""
		if base != "" {
			openURL = base + "/go/claim?token=" + url.QueryEscape(pairing.GetClaimToken())
		}
		if openURL != "" && strings.HasPrefix(openURL, "https://") {
			keyboard = mainKeyboardWithDeeplink(profile.GetPreferredPlatform(), openURL)
		} else {
			if base == "" {
				log.Printf("HEYEARTH_PUBLIC_URL / SITE_DOMAIN not set: URL button for open-in-app disabled")
			}
			reply += "\n\n_Ссылка для открытия:_ `" + pairing.GetDeeplinkUrl() + "`"
			keyboard = mainKeyboardForPlatform(profile.GetPreferredPlatform())
		}
	case cbGetLoginCode:
		profile, err := backend.ensureTelegramUser(userID, "", "ru")
		if err != nil {
			reply = "Не удалось загрузить профиль. Попробуй позже."
			break
		}
		pairing, err := backend.createPairingCode(userID, profile.GetPreferredPlatform())
		if err != nil {
			reply = "Не удалось подготовить вход в приложение. Попробуй позже."
			break
		}
		reply = "🔑 *Код входа*\n\nВведи этот код в приложении HeYearth:\n\n`" + pairing.GetCode() + "`\n\nДействует до `" + pairing.GetExpiresAt() + "`."
		keyboard = mainKeyboardForPlatform(profile.GetPreferredPlatform())
	case cbProfile:
		profile, err := backend.ensureTelegramUser(userID, "", "ru")
		if err != nil {
			reply = "Не удалось загрузить профиль. Попробуй позже."
			break
		}
		reply = profileSummary(profile)
		keyboard = mainKeyboardForPlatform(profile.GetPreferredPlatform())
	case cbChangeDevice:
		reply = "С какого устройства будешь подключаться?"
		keyboard = deviceChoiceKeyboard()
	default:
		return
	}

	edit := tgbotapi.NewEditMessageTextAndMarkup(chatID, messageID, reply, keyboard)
	edit.ParseMode = "Markdown"
	if _, err := bot.Send(edit); err != nil {
		log.Printf("Edit message: %v, fallback to new message", err)
		sendMsg(bot, chatID, reply, &keyboard)
	}
}

// sendOrEditMsg tries to edit the last bot message in the chat to avoid clutter; falls back to sending new.
func sendOrEditMsg(bot *tgbotapi.BotAPI, chatID int64, text string, keyboard *tgbotapi.InlineKeyboardMarkup) {
	if v, ok := lastBotMessageID.Load(chatID); ok {
		if msgID, ok := v.(int); ok && msgID > 0 {
			edit := tgbotapi.NewEditMessageText(chatID, msgID, text)
			edit.ParseMode = "Markdown"
			if keyboard != nil {
				edit.ReplyMarkup = &tgbotapi.InlineKeyboardMarkup{InlineKeyboard: keyboard.InlineKeyboard}
			}
			if _, err := bot.Send(edit); err == nil {
				return
			}
		}
	}
	sendMsg(bot, chatID, text, keyboard)
}

func sendMsg(bot *tgbotapi.BotAPI, chatID int64, text string, keyboard *tgbotapi.InlineKeyboardMarkup) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	if keyboard != nil {
		msg.ReplyMarkup = *keyboard
	}
	if sent, err := bot.Send(msg); err != nil {
		log.Printf("Send error: %v", err)
	} else if sent.MessageID != 0 {
		lastBotMessageID.Store(chatID, sent.MessageID)
	}
}

func deviceChoiceKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📱 Android", cbPlatformAndroid),
			tgbotapi.NewInlineKeyboardButtonData("💻 Windows", cbPlatformWindows),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🍎 iOS", cbPlatformIOS),
			tgbotapi.NewInlineKeyboardButtonData("🍎 macOS", cbPlatformMacOS),
		),
	)
}

func deviceKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📱 iOS", cbInstPhone),
			tgbotapi.NewInlineKeyboardButtonData("💻 macOS / Linux", cbInstPC),
		),
	)
}

func mainKeyboardForPlatform(platform string) tgbotapi.InlineKeyboardMarkup {
	return mainKeyboardForPlatformWithExtra(platform, nil)
}

// mainKeyboardWithDeeplink returns main keyboard with a first row: URL button to open the app.
func mainKeyboardWithDeeplink(platform, deeplinkURL string) tgbotapi.InlineKeyboardMarkup {
	urlRow := tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonURL("🚀 Открыть HeYearth", deeplinkURL),
	)
	return mainKeyboardForPlatformWithExtra(platform, &urlRow)
}

func mainKeyboardForPlatformWithExtra(platform string, firstRow *[]tgbotapi.InlineKeyboardButton) tgbotapi.InlineKeyboardMarkup {
	var rows [][]tgbotapi.InlineKeyboardButton
	if firstRow != nil {
		rows = append(rows, *firstRow)
	}
	if platform == "android" || platform == "windows" {
		rows = append(rows,
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("📲 Скачать приложение", cbDownloadApp),
				tgbotapi.NewInlineKeyboardButtonData("🚀 Открыть в приложении", cbOpenInApp),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔑 Получить код входа", cbGetLoginCode),
				tgbotapi.NewInlineKeyboardButtonData("👤 Мой профиль", cbProfile),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("📖 Инструкция", cbInstruction),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔄 Сменить устройство", cbChangeDevice),
			),
		)
	} else {
		rows = append(rows,
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔗 Получить ссылку", cbGetSS),
				tgbotapi.NewInlineKeyboardButtonData("🔄 Новая ссылка", cbNewSS),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🚀 Открыть в приложении", cbOpenInApp),
				tgbotapi.NewInlineKeyboardButtonData("🔑 Код входа", cbGetLoginCode),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("👤 Мой профиль", cbProfile),
				tgbotapi.NewInlineKeyboardButtonData("📖 Инструкция", cbInstruction),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔄 Сменить устройство", cbChangeDevice),
			),
		)
	}
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func firstMessageForProfile(profile *heyearth_v1.UserProfileResponse) string {
	userID := profile.GetUserId()
	plan := strings.TrimSpace(profile.GetCurrentPlan())
	if plan == "" {
		plan = "Free"
	}
	switch profile.GetPreferredPlatform() {
	case "android":
		return fmt.Sprintf("📲 *HeYearth для Android*\n\nТвой HeYearth ID:\n`%s`\n\nПлан: *%s*\n\nСкачай приложение и используй pairing через кнопку «Открыть в приложении» или «Получить код входа».", userID, plan)
	case "windows":
		return fmt.Sprintf("💻 *HeYearth для Windows*\n\nТвой HeYearth ID:\n`%s`\n\nПлан: *%s*\n\nСкачай приложение и используй pairing через кнопку «Открыть в приложении» или «Получить код входа».", userID, plan)
	case "ios", "macos":
		return fmt.Sprintf("🍎 *Apple fallback через Happ*\n\nТвой HeYearth ID:\n`%s`\n\nПлан: *%s*\n\nПока для %s используем Happ как fallback. При желании можешь уже подготовить pairing для будущего входа в приложение.", userID, plan, profile.GetPreferredPlatform())
	default:
		return fmt.Sprintf("Твой HeYearth ID:\n`%s`\n\nВыбери устройство и способ входа.", userID)
	}
}

func instructionForApp(platform string) string {
	if platform == "android" {
		return instructionAppAndroid
	}
	return instructionAppWindows
}

func handleLegacyConfigCommand(backend *backendClient, telegramID int64, forceNew bool) (string, *tgbotapi.InlineKeyboardMarkup) {
	profile, err := backend.ensureTelegramUser(telegramID, "", "ru")
	if err != nil {
		return "Не удалось загрузить профиль. Попробуй позже.", nil
	}
	platform := profile.GetPreferredPlatform()
	if platform != "ios" && platform != "macos" {
		k := mainKeyboardForPlatform(platform)
		return "Legacy ss:// ссылка сейчас доступна только для iOS/macOS через Happ. Для Android/Windows используй pairing и приложение HeYearth.", &k
	}
	resp, err := backend.issueLegacyAppleConfig(telegramID, forceNew)
	if err != nil && !forceNew {
		// Первое нажатие «Получить ссылку» иногда падает (нода ещё не готова); повторяем с forceNew
		time.Sleep(1 * time.Second)
		resp, err = backend.issueLegacyAppleConfig(telegramID, true)
		forceNew = true
	}
	if err != nil {
		k := mainKeyboardForPlatform(platform)
		return "Не удалось выдать ss:// ссылку: " + err.Error(), &k
	}
	text := fmt.Sprintf("Твоя персональная ссылка:\n\n`%s`\n\nТвой HeYearth ID:\n`%s`\n\nИмпортируй её в Happ и включи глобальный режим с DNS через прокси.", resp.GetSsUri(), resp.GetUserId())
	if forceNew {
		text = fmt.Sprintf("Ссылка обновлена:\n\n`%s`\n\nТвой HeYearth ID:\n`%s`\n\nИмпортируй её в Happ и включи глобальный режим с DNS через прокси.", resp.GetSsUri(), resp.GetUserId())
	}
	k := mainKeyboardForPlatform(platform)
	return text, &k
}

func downloadMessageForProfile(backend *backendClient, profile *heyearth_v1.UserProfileResponse) string {
	dist, err := backend.getDistribution(profile.GetPreferredPlatform())
	if err != nil {
		return "Не удалось получить ссылку на приложение. Попробуй позже."
	}
	userID := profile.GetUserId()
	switch profile.GetPreferredPlatform() {
	case "android":
		if strings.TrimSpace(dist.GetAndroidUrl()) == "" {
			return fmt.Sprintf("📲 Ссылка на Android-сборку пока не подготовлена.\n\nТвой HeYearth ID:\n`%s`", userID)
		}
		return fmt.Sprintf("📲 Android build `%s`\n\nСкачать:\n%s\n\nПосле установки выбери pairing через кнопку «Открыть в приложении» или введи код входа.", dist.GetVersion(), dist.GetAndroidUrl())
	case "windows":
		if strings.TrimSpace(dist.GetWindowsUrl()) == "" {
			return fmt.Sprintf("💻 Ссылка на Windows-сборку пока не подготовлена.\n\nТвой HeYearth ID:\n`%s`", userID)
		}
		return fmt.Sprintf("💻 Windows build `%s`\n\nСкачать:\n%s\n\nПосле установки используй pairing или код входа.", dist.GetVersion(), dist.GetWindowsUrl())
	default:
		return "Для iOS/macOS пока используется Happ fallback. Если приложение для твоей платформы появится, бот начнёт показывать ссылку здесь."
	}
}

func profileSummary(profile *heyearth_v1.UserProfileResponse) string {
	plan := strings.TrimSpace(profile.GetCurrentPlan())
	if plan == "" {
		plan = "Free"
	}
	validUntil := strings.TrimSpace(profile.GetValidUntil())
	if validUntil == "" {
		validUntil = "не ограничен"
	}
	status := strings.TrimSpace(profile.GetAccessStatus())
	if status == "" {
		status = "active"
	}
	telegramStatus := "не привязан"
	if profile.GetTelegramLinked() {
		telegramStatus = "привязан"
	}
	return fmt.Sprintf(
		"👤 *Профиль HeYearth*\n\nID:\n`%s`\n\nПлан: *%s*\nДоступ до: `%s`\nСтатус: `%s`\nПлатформа: `%s`\nTelegram: *%s*",
		profile.GetUserId(),
		plan,
		validUntil,
		status,
		firstNonEmpty(strings.TrimSpace(profile.GetPreferredPlatform()), "не выбрана"),
		telegramStatus,
	)
}

func pairingMessage(resp *heyearth_v1.CreatePairingCodeResponse) string {
	return fmt.Sprintf(
		"🚀 *Вход в приложение HeYearth*\n\nHeYearth ID:\n`%s`\n\nDeep link:\n`%s`\n\nОдноразовый код:\n`%s`\n\nКод действует до `%s`.\nЕсли deep-link не откроется автоматически, введи код вручную в приложении.",
		resp.GetProfile().GetUserId(),
		resp.GetDeeplinkUrl(),
		resp.GetCode(),
		resp.GetExpiresAt(),
	)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
