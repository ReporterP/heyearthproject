package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/heyearth/backend/internal/config"
	"github.com/heyearth/backend/internal/ss"
)

const telegramMaxFileSize = 50 * 1024 * 1024 // 50 MB

const (
	cbGetSS          = "getss"
	cbNewSS          = "newss"
	cbInstruction    = "instruction"
	cbInstPhone      = "inst_phone"
	cbInstPC         = "inst_pc"
	cbPlatformAndroid = "plat_android"
	cbPlatformWindows = "plat_windows"
	cbPlatformIOS    = "plat_ios"
	cbPlatformMacOS  = "plat_macos"
	cbDownloadApp    = "download_app"
)

const (
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

• Нажми кнопку «Скачать приложение» — откроется ссылка на APK.
• Установи приложение (разреши установку из неизвестных источников при необходимости).
• Открой HeYearth, выбери «Я новый пользователь» или «У меня уже есть ID» и введи свой ID из бота (/start → твой ID покажем).`

	instructionAppWindows = `💻 *Приложение HeYearth для Windows*

• Нажми кнопку «Скачать приложение» — откроется ссылка на архив.
• Скачай zip, распакуй и запусти heyearth_vpn.exe.
• В приложении войди по ID (его можно посмотреть в боте: /start).`
)

// Клавиатура для iOS/macOS: ссылка ss:// и инструкция Happ
func linkKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔗 Получить ссылку", cbGetSS),
			tgbotapi.NewInlineKeyboardButtonData("🔄 Новая ссылка", cbNewSS),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📖 Инструкция", cbInstruction),
		),
	)
}

// Клавиатура для Android/Windows: только скачать приложение и инструкция
func appKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📲 Скачать приложение", cbDownloadApp),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📖 Инструкция", cbInstruction),
		),
	)
}

// Выбор устройства при первом заходе
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

// Клавиатура выбора устройства для инструкции (только для iOS/macOS — типы Happ)
func deviceKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📱 iOS", cbInstPhone),
			tgbotapi.NewInlineKeyboardButtonData("💻 macOS / Linux", cbInstPC),
		),
	)
}

func main() {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN not set")
	}
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
			handleCallback(bot, &update)
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
		case text == "/start":
			if !isRegistered(userID) {
				reply = "Привет! Это бот HeYearth.\n\nСначала пройди регистрацию: /register"
			} else {
				platform := loadPlatform(userID)
				if platform == "" {
					reply = "С какого устройства будешь подключаться?"
					k := deviceChoiceKeyboard()
					keyboard = &k
				} else {
					reply = firstMessageForPlatform(platform, userID)
					k := mainKeyboardForPlatform(platform)
					keyboard = &k
				}
			}
		case text == "/register":
			if isRegistered(userID) {
				platform := loadPlatform(userID)
				if platform == "" {
					reply = "С какого устройства будешь подключаться?"
					k := deviceChoiceKeyboard()
					keyboard = &k
				} else {
					reply = firstMessageForPlatform(platform, userID)
					k := mainKeyboardForPlatform(platform)
					keyboard = &k
				}
			} else {
				saveRegistered(userID)
				reply = "Регистрация прошла успешно.\n\nС какого устройства будешь подключаться?"
				k := deviceChoiceKeyboard()
				keyboard = &k
			}
		case text == "/instruction":
			platform := loadPlatform(userID)
			if platform == "" {
				reply = "С какого устройства подключаешься?"
				k := deviceChoiceKeyboard()
				keyboard = &k
			} else if platform == "android" || platform == "windows" {
				reply = instructionForApp(platform)
				k := appKeyboard()
				keyboard = &k
			} else {
				reply = "С какого устройства подключаешься?"
				k := deviceKeyboard()
				keyboard = &k
			}
		case text == "/getss":
			if loadPlatform(userID) != "ios" && loadPlatform(userID) != "macos" {
				reply = "Команда для получения ss:// ссылки — только для пользователей iOS/macOS (Happ). Используй кнопку «Скачать приложение» для Android/Windows."
			} else {
				reply = handleGetSS(userID, false)
				if strings.Contains(reply, "ss://") {
					k := linkKeyboard()
					keyboard = &k
				}
			}
		case text == "/newss":
			if loadPlatform(userID) != "ios" && loadPlatform(userID) != "macos" {
				reply = "Команда для новой ss:// ссылки — только для iOS/macOS. Для Android/Windows используй «Скачать приложение»."
			} else {
				reply = handleGetSS(userID, true)
				if strings.Contains(reply, "ss://") {
					k := linkKeyboard()
					keyboard = &k
				}
			}
		case text == "/help":
			reply = "Команды: /start — начало, /register — регистрация. После выбора устройства (Android, Windows, iOS или macOS) появятся нужные кнопки."
		default:
			continue
		}
		sendMsg(bot, msg.Chat.ID, reply, keyboard)
	}
}

func mainKeyboardForPlatform(platform string) tgbotapi.InlineKeyboardMarkup {
	if platform == "android" || platform == "windows" {
		return appKeyboard()
	}
	return linkKeyboard()
}

func firstMessageForPlatform(platform string, userID int64) string {
	switch platform {
	case "android":
		urls := getLatestAppURLs()
		if urls.Android != "" {
			return fmt.Sprintf("📲 *Скачай приложение HeYearth для Android*\n\nСсылка на APK:\n%s\n\nУстанови приложение, открой — выбери «Я новый пользователь» или войди по ID с другого устройства.", urls.Android)
		}
		return "📲 Приложение HeYearth для Android. Нажми «Скачать приложение» — ссылка появится, когда на сервере будет выложена сборка."
	case "windows":
		urls := getLatestAppURLs()
		if urls.Windows != "" {
			return fmt.Sprintf("💻 *Скачай приложение HeYearth для Windows*\n\nСсылка на архив:\n%s\n\nРаспакуй и запусти heyearth_vpn.exe. В приложении войди по ID (новый пользователь или с другого устройства).", urls.Windows)
		}
		return "💻 Приложение HeYearth для Windows. Нажми «Скачать приложение», когда на сервере будет выложена сборка."
	case "ios", "macos":
		return "Выбери действие: получить ссылку ss:// для Happ или прочитать инструкцию."
	default:
		return "Выбери действие ниже."
	}
}

func instructionForApp(platform string) string {
	if platform == "android" {
		return instructionAppAndroid
	}
	return instructionAppWindows
}

func handleCallback(bot *tgbotapi.BotAPI, update *tgbotapi.Update) {
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
		savePlatform(userID, "android")
		reply = firstMessageForPlatform("android", userID)
		keyboard = appKeyboard()
	case cbPlatformWindows:
		savePlatform(userID, "windows")
		reply = firstMessageForPlatform("windows", userID)
		keyboard = appKeyboard()
	case cbPlatformIOS:
		savePlatform(userID, "ios")
		reply = "Для iOS подключайся через Happ. Нажми «Получить ссылку» и вставь её в приложение Happ."
		keyboard = linkKeyboard()
	case cbPlatformMacOS:
		savePlatform(userID, "macos")
		reply = "Для macOS подключайся через Happ. Нажми «Получить ссылку» и вставь в Happ."
		keyboard = linkKeyboard()
	case cbGetSS:
		reply = handleGetSS(userID, false)
		keyboard = linkKeyboard()
	case cbNewSS:
		reply = handleGetSS(userID, true)
		keyboard = linkKeyboard()
	case cbInstruction:
		platform := loadPlatform(userID)
		if platform == "android" || platform == "windows" {
			reply = instructionForApp(platform)
			keyboard = appKeyboard()
		} else {
			reply = "С какого устройства подключаешься?"
			keyboard = deviceKeyboard()
		}
	case cbInstPhone:
		reply = instructionPhone
		keyboard = linkKeyboard()
	case cbInstPC:
		reply = instructionPC
		keyboard = linkKeyboard()
	case cbDownloadApp:
		platform := loadPlatform(userID)
		urls := getLatestAppURLs()
		if platform == "android" || platform == "windows" {
			if sendAppFile(bot, chatID, platform, urls.Version) {
				reply = "📲 Файл приложения отправлен выше 👆"
				if platform == "windows" {
					reply = "💻 Файл приложения отправлен выше 👆"
				}
			} else if platform == "android" && urls.Android != "" {
				reply = "📲 Скачать HeYearth для Android:\n" + urls.Android
			} else if platform == "windows" && urls.Windows != "" {
				reply = "💻 Скачать HeYearth для Windows:\n" + urls.Windows
			} else {
				reply = "Ссылка на приложение пока не подготовлена. Попробуй позже или напиши в поддержку."
			}
		} else {
			reply = "Кнопка «Скачать приложение» для Android/Windows. Ты выбрал iOS/macOS — используй «Получить ссылку» для Happ."
		}
		keyboard = mainKeyboardForPlatform(platform)
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

func sendMsg(bot *tgbotapi.BotAPI, chatID int64, text string, keyboard *tgbotapi.InlineKeyboardMarkup) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	if keyboard != nil {
		msg.ReplyMarkup = *keyboard
	}
	if _, err := bot.Send(msg); err != nil {
		log.Printf("Send error: %v", err)
	}
}

func isRegistered(userID int64) bool {
	f, err := os.Open(config.DataFile())
	if err != nil {
		return false
	}
	defer f.Close()
	idStr := strconv.FormatInt(userID, 10)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == idStr {
			return true
		}
	}
	return false
}

func saveRegistered(userID int64) {
	dir := config.AppDir()
	_ = os.MkdirAll(dir, 0755)
	f, err := os.OpenFile(config.DataFile(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(strconv.FormatInt(userID, 10) + "\n")
}

func loadPlatform(userID int64) string {
	f, err := os.Open(config.PlatformFile())
	if err != nil {
		return ""
	}
	defer f.Close()
	idStr := strconv.FormatInt(userID, 10)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) >= 2 && parts[0] == idStr {
			return strings.TrimSpace(parts[1])
		}
	}
	return ""
}

func savePlatform(userID int64, platform string) {
	platformFile := config.PlatformFile()
	// Читаем все строки, подменяем или добавляем запись для userID
	lines := make([]string, 0)
	idStr := strconv.FormatInt(userID, 10)
	if f, err := os.Open(platformFile); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := sc.Text()
			if strings.TrimSpace(line) == "" {
				continue
			}
			parts := strings.SplitN(line, " ", 2)
			if len(parts) >= 2 && parts[0] == idStr {
				continue
			}
			lines = append(lines, line)
		}
		f.Close()
	}
	_ = os.MkdirAll(config.AppDir(), 0755)
	lines = append(lines, idStr+" "+platform)
	f, err := os.Create(platformFile)
	if err != nil {
		return
	}
	defer f.Close()
	for _, l := range lines {
		_, _ = f.WriteString(l + "\n")
	}
}

type appURLs struct {
	Version string `json:"version"`
	Android string `json:"android"`
	Windows string `json:"windows"`
}

func getLatestAppURLs() appURLs {
	cli := &http.Client{Timeout: 5 * time.Second}
	resp, err := cli.Get("http://127.0.0.1:8000/api/v1/app/update")
	if err != nil {
		return appURLs{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return appURLs{}
	}
	var data appURLs
	if json.NewDecoder(resp.Body).Decode(&data) != nil {
		return appURLs{}
	}
	data.Version = strings.TrimSpace(data.Version)
	data.Android = strings.TrimSpace(data.Android)
	data.Windows = strings.TrimSpace(data.Windows)
	return data
}

// sendAppFile отправляет файл приложения в чат (APK или zip). Возвращает true, если файл отправлен.
// Telegram лимит 50 MB — при большем размере файл не отправляется.
func sendAppFile(bot *tgbotapi.BotAPI, chatID int64, platform, version string) bool {
	if version == "" {
		return false
	}
	var filename string
	if platform == "android" {
		filename = fmt.Sprintf("heyearth-%s.apk", version)
	} else if platform == "windows" {
		filename = fmt.Sprintf("heyearth-%s-windows.zip", version)
	} else {
		return false
	}
	path := filepath.Join(config.ReleasesDir(), filename)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if info.Size() > telegramMaxFileSize {
		return false
	}
	doc := tgbotapi.NewDocument(chatID, tgbotapi.FilePath(path))
	doc.Caption = fmt.Sprintf("HeYearth %s — %s", version, platform)
	if _, err := bot.Send(doc); err != nil {
		log.Printf("Send document %s: %v", filename, err)
		return false
	}
	return true
}

func handleGetSS(userID int64, forceNew bool) string {
	if !isRegistered(userID) {
		return "Сначала зарегистрируйтесь: /register"
	}
	uri, err := ss.GetOrCreateURI(strconv.FormatInt(userID, 10), forceNew)
	if err != nil {
		if os.IsNotExist(err) {
			return "Shadowsocks на сервере ещё не настроен. Обратитесь к администратору."
		}
		return "Не удалось выдать ссылку: " + err.Error()
	}
	if !strings.HasPrefix(uri, "ss://") {
		return "Неверный ответ от сервера."
	}
	if forceNew {
		return fmt.Sprintf("Новая персональная ссылка (старая больше не действует):\n\n`%s`\n\nВставь в Happ, включи глобальный режим и «DNS через прокси».", uri)
	}
	return fmt.Sprintf("Ваша *персональная* ссылка:\n\n`%s`\n\nСкопируй в Happ. *Включи глобальный режим и «DNS через прокси»*.", uri)
}
