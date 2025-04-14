package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

type ReminderType string

const (
	Daily   ReminderType = "daily"
	Weekly  ReminderType = "weekly"
	Monthly ReminderType = "monthly"
	None    ReminderType = "none"
)

type UserPreference struct {
	ChatID       int64
	ReminderType ReminderType
	LastReminder time.Time
}

var (
	botToken          string
	spreadsheetID     string
	credentialsBase64 string
	mode              string
	editingState      = make(map[int64]int)
	userPreferences   = make(map[int64]UserPreference)
)

func init() {
	if os.Getenv("RAILWAY_ENVIRONMENT") == "" {
		err := godotenv.Load()
		if err != nil {
			log.Println("Skipping .env loading, not found.")
		}
	}

	botToken = os.Getenv("BOT_TOKEN")
	spreadsheetID = os.Getenv("SPREADSHEET_ID")
	credentialsBase64 = os.Getenv("GOOGLE_CREDENTIALS_BASE64")
	mode = os.Getenv("MODE")
	if mode == "" {
		mode = "polling"
	}

	if botToken == "" || spreadsheetID == "" || credentialsBase64 == "" {
		log.Fatal("One or more required environment variables are not set.")
	}

	// Load user preferences from spreadsheet
	loadUserPreferences()
}

func loadUserPreferences() {
	ctx := context.Background()
	srv, err := authorize(ctx)
	if err != nil {
		log.Printf("Failed to load user preferences: %v", err)
		return
	}

	resp, err := srv.Spreadsheets.Values.Get(spreadsheetID, "Preferences!A:C").Do()
	if err != nil {
		log.Printf("Failed to get preferences: %v", err)
		return
	}

	if resp != nil && resp.Values != nil {
		for _, row := range resp.Values[1:] { // Skip header
			if len(row) >= 3 {
				chatID, _ := strconv.ParseInt(fmt.Sprintf("%v", row[0]), 10, 64)
				reminderType := ReminderType(fmt.Sprintf("%v", row[1]))
				lastReminder, _ := time.Parse("2006-01-02", fmt.Sprintf("%v", row[2]))

				userPreferences[chatID] = UserPreference{
					ChatID:       chatID,
					ReminderType: reminderType,
					LastReminder: lastReminder,
				}
			}
		}
	}
}

func saveUserPreference(srv *sheets.Service, pref UserPreference) error {
	// First, try to find existing preference
	resp, err := srv.Spreadsheets.Values.Get(spreadsheetID, "Preferences!A:A").Do()
	if err != nil {
		return err
	}

	var rowNum int
	if resp != nil && resp.Values != nil {
		for i, row := range resp.Values {
			if len(row) > 0 && fmt.Sprintf("%v", row[0]) == fmt.Sprintf("%d", pref.ChatID) {
				rowNum = i + 1
				break
			}
		}
	}

	// If not found, append new row
	if rowNum == 0 {
		rowNum = len(resp.Values) + 1
	}

	values := [][]interface{}{{
		pref.ChatID,
		string(pref.ReminderType),
		pref.LastReminder.Format("2006-01-02"),
	}}
	valueRange := &sheets.ValueRange{Values: values}

	rangeToUpdate := fmt.Sprintf("Preferences!A%d:C%d", rowNum, rowNum)
	_, err = srv.Spreadsheets.Values.Update(spreadsheetID, rangeToUpdate, valueRange).ValueInputOption("USER_ENTERED").Do()
	return err
}

func sendReminder(bot *tgbotapi.BotAPI, srv *sheets.Service, chatID int64, reminderType ReminderType) {
	var summary string
	var err error

	switch reminderType {
	case Daily:
		summary, err = getDailySummary(srv)
	case Weekly:
		summary, err = getWeeklySummary(srv)
	case Monthly:
		summary, err = getMonthlySummary(srv)
	}

	if err != nil {
		log.Printf("Failed to get summary for reminder: %v", err)
		return
	}

	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("🔔 Pengingat %s:\n\n%s", reminderType, summary))
	bot.Send(msg)

	// Update last reminder time
	pref := userPreferences[chatID]
	pref.LastReminder = time.Now()
	userPreferences[chatID] = pref

	// Save to spreadsheet
	ctx := context.Background()
	srv, err = authorize(ctx)
	if err != nil {
		log.Printf("Failed to save reminder time: %v", err)
		return
	}
	saveUserPreference(srv, pref)
}

func getDailySummary(srv *sheets.Service) (string, error) {
	today := time.Now().Format("02-01-2006")
	resp, err := srv.Spreadsheets.Values.Get(spreadsheetID, "A:E").Do()
	if err != nil {
		return "", err
	}

	var total int
	var entries []string

	for _, row := range resp.Values[1:] {
		if len(row) < 5 {
			continue
		}

		date := fmt.Sprintf("%v", row[1])
		if date == today {
			nominal, _ := strconv.Atoi(fmt.Sprintf("%v", row[2]))
			total += nominal
			entries = append(entries, fmt.Sprintf("💰%v | 🎯%v | 📚%v", row[2], row[3], row[4]))
		}
	}

	if len(entries) == 0 {
		return "Tidak ada pengeluaran hari ini", nil
	}

	result := fmt.Sprintf("📊 Pengeluaran Hari Ini (Rp. %d):\n\n", total)
	for _, entry := range entries {
		result += entry + "\n"
	}
	return result, nil
}

func startReminderScheduler(bot *tgbotapi.BotAPI, srv *sheets.Service) {
	// Create a ticker that ticks every minute
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	// Create a channel to handle shutdown
	done := make(chan bool)

	// Start the scheduler in a separate goroutine
	go func() {
		for {
			select {
			case <-done:
				return
			case t := <-ticker.C:
				// Check if it's time to send reminders
				now := t
				for chatID, pref := range userPreferences {
					if pref.ReminderType == None {
						continue
					}

					var shouldSend bool
					switch pref.ReminderType {
					case Daily:
						// Send at 8 PM every day
						shouldSend = now.Hour() == 20 && now.Sub(pref.LastReminder) >= 24*time.Hour
					case Weekly:
						// Send at 8 PM every Sunday
						shouldSend = now.Weekday() == time.Sunday && now.Hour() == 20 && now.Sub(pref.LastReminder) >= 7*24*time.Hour
					case Monthly:
						// Send at 8 PM on the first day of the month
						shouldSend = now.Day() == 1 && now.Hour() == 20 && now.Sub(pref.LastReminder) >= 30*24*time.Hour
					}

					if shouldSend {
						// Start a new goroutine for each reminder to avoid blocking
						go func(chatID int64, pref UserPreference) {
							sendReminder(bot, srv, chatID, pref.ReminderType)
						}(chatID, pref)
					}
				}
			}
		}
	}()

	// Handle graceful shutdown
	go func() {
		// Wait for interrupt signal
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		<-sigChan

		// Cleanup
		done <- true
		close(done)
	}()
}

func main() {
	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		log.Panicf("failed to create bot API client: %v", err)
	}
	bot.Debug = true

	ctx := context.Background()
	srv, err := authorize(ctx)
	if err != nil {
		log.Fatalf("failed to authorize with Google Sheets: %v", err)
	}

	// Start reminder scheduler
	startReminderScheduler(bot, srv)

	switch mode {
	case "webhook":
		runWebhook(bot, srv)
	default:
		runPolling(bot, srv)
	}
}

func runWebhook(bot *tgbotapi.BotAPI, srv *sheets.Service) {
	webhookURL := os.Getenv("WEBHOOK_URL")
	port := os.Getenv("PORT")
	if webhookURL == "" || port == "" {
		log.Fatal("WEBHOOK_URL or PORT not set")
	}

	webhookConfig, err := tgbotapi.NewWebhook(webhookURL)
	if err != nil {
		log.Fatalf("Failed to create webhook config: %v", err)
	}
	_, err = bot.Request(webhookConfig)
	if err != nil {
		log.Fatalf("Failed to set webhook: %v", err)
	}

	log.Printf("📡 Running in Webhook mode... Listening on %s", port)

	http.HandleFunc("/webhook", func(w http.ResponseWriter, r *http.Request) {
		var update tgbotapi.Update
		if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
			log.Printf("Error decoding update: %v", err)
			return
		}
		log.Printf("Received update: %+v", update)
		handleUpdate(bot, srv, update)
	})

	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func runPolling(bot *tgbotapi.BotAPI, srv *sheets.Service) {
	log.Println("🔁 Running in Polling mode...")
	bot.Request(tgbotapi.DeleteWebhookConfig{})

	updateConfig := tgbotapi.NewUpdate(0)
	updateConfig.Timeout = 60

	updates := bot.GetUpdatesChan(updateConfig)
	for update := range updates {
		handleUpdate(bot, srv, update)
	}
}

func handleUpdate(bot *tgbotapi.BotAPI, srv *sheets.Service, update tgbotapi.Update) {
	if update.Message == nil {
		return
	}

	chatId := update.Message.Chat.ID
	text := update.Message.Text

	// Check if user is in editing state
	if editingRow, isEditing := editingState[chatId]; isEditing {
		// User is in editing state, expect new data
		parts := strings.Split(text, ",")
		if len(parts) == 3 {
			nominalStr := strings.TrimSpace(parts[0])
			budget := strings.TrimSpace(parts[1])
			keterangan := strings.TrimSpace(parts[2])

			normalizedNominal := normalizeNominal(nominalStr)
			err := editEntry(srv, editingRow, normalizedNominal, budget, keterangan)
			if err != nil {
				bot.Send(tgbotapi.NewMessage(chatId, "❌ Gagal mengedit data."))
				delete(editingState, chatId)
				return
			}

			// Show the edited entry
			editedEntry, _ := getEntryByNumber(srv, editingRow)
			msg := tgbotapi.NewMessage(chatId, fmt.Sprintf("✅ Data berhasil diedit:\n%s", editedEntry))
			bot.Send(msg)
			delete(editingState, chatId)
			return
		} else {
			bot.Send(tgbotapi.NewMessage(chatId, "Format salah🙅🏻‍♂️. Gunakan: Nominal, Kategori, Keterangan\nContoh: 10rb, Makanan, Makan Siang di Kantin"))
			return
		}
	}

	// Handle commands
	if strings.HasPrefix(text, "/") {
		switch {
		case text == "/start":
			msg := tgbotapi.NewMessage(chatId, "👋 Hai! Saya adalah bot pencatat keuangan.\n\n"+
				"📝 Untuk mencatat pengeluaran, kirim dalam format:\n"+
				"Nominal, Kategori, Keterangan\n"+
				"Contoh: 10rb, Makanan, Makan Siang di Kantin\n\n"+
				"📋 Perintah yang tersedia:\n"+
				"/help - Tampilkan bantuan\n"+
				"/summary - Tampilkan total pengeluaran\n"+
				"/weekly - Tampilkan pengeluaran minggu ini\n"+
				"/monthly - Tampilkan pengeluaran bulan ini\n"+
				"/last - Tampilkan data terakhir\n"+
				"/remove - Hapus entri terakhir\n"+
				"/edit - Edit entri berdasarkan nomor\n"+
				"/history - Tampilkan 5 transaksi terakhir\n"+
				"/reminder - Atur pengingat harian/mingguan")
			bot.Send(msg)
			return

		case text == "/help":
			msg := tgbotapi.NewMessage(chatId, "📋 Cara menggunakan bot:\n\n"+
				"1. Untuk mencatat pengeluaran:\n"+
				"   Kirim dalam format: Nominal, Kategori, Keterangan\n"+
				"   Contoh: 10rb, Makanan, Makan Siang di Kantin\n\n"+
				"2. Perintah yang tersedia:\n"+
				"   /start - Mulai bot\n"+
				"   /help - Tampilkan bantuan ini\n"+
				"   /summary - Tampilkan total pengeluaran\n"+
				"   /weekly - Tampilkan pengeluaran minggu ini\n"+
				"   /monthly - Tampilkan pengeluaran bulan ini\n"+
				"   /last - Tampilkan data terakhir\n"+
				"   /remove - Hapus entri terakhir\n"+
				"   /edit <nomor> - Edit entri berdasarkan nomor\n"+
				"   /history - Tampilkan 5 transaksi terakhir\n\n"+
				"3. Format nominal:\n"+
				"   - 10rb = 10.000\n"+
				"   - 1jt = 1.000.000\n"+
				"   - 100k = 100.000")
			bot.Send(msg)
			return

		case text == "/edit " + text:
			// Extract row number from command
			rowNumberStr := strings.TrimPrefix(text, "/edit ")
			rowNumber, err := strconv.Atoi(rowNumberStr)
			if err != nil {
				bot.Send(tgbotapi.NewMessage(chatId, "❌ Nomor entri tidak valid. Gunakan format: /edit <nomor>"))
				return
			}

			// Get the entry to show what will be edited
			entry, err := getEntryByNumber(srv, rowNumber)
			if err != nil {
				bot.Send(tgbotapi.NewMessage(chatId, "❌ Entri tidak ditemukan"))
				return
			}

			// Store the row number in editing state
			editingState[chatId] = rowNumber

			msg := tgbotapi.NewMessage(chatId, fmt.Sprintf("✏️ Edit entri #%d:\n%s\n\nKirim data baru dalam format:\nNominal, Kategori, Keterangan\nContoh: 10rb, Makanan, Makan Siang di Kantin", rowNumber, entry))
			bot.Send(msg)
			return

		case text == "/summary":
			summary := getSummary(srv)
			msg := tgbotapi.NewMessage(chatId, fmt.Sprintf("📊 Total pengeluaran saat ini: Rp. %d", summary))
			bot.Send(msg)
			return

		case text == "/weekly":
			weeklySummary, err := getWeeklySummary(srv)
			if err != nil {
				msg := tgbotapi.NewMessage(chatId, "❌ Gagal mengambil data pengeluaran mingguan")
				bot.Send(msg)
				return
			}
			msg := tgbotapi.NewMessage(chatId, weeklySummary)
			bot.Send(msg)
			return

		case text == "/monthly":
			monthlySummary, err := getMonthlySummary(srv)
			if err != nil {
				msg := tgbotapi.NewMessage(chatId, "❌ Gagal mengambil data pengeluaran bulanan")
				bot.Send(msg)
				return
			}
			msg := tgbotapi.NewMessage(chatId, monthlySummary)
			bot.Send(msg)
			return

		case text == "/last":
			lastEntry, err := getLastEntry(srv)
			if err != nil {
				msg := tgbotapi.NewMessage(chatId, "❌ Gagal mengambil data terakhir")
				bot.Send(msg)
				return
			}
			msg := tgbotapi.NewMessage(chatId, lastEntry)
			bot.Send(msg)
			return

		case text == "/remove":
			lastEntry, err := getLastEntry(srv)
			if err != nil {
				msg := tgbotapi.NewMessage(chatId, "❌ Gagal mengambil data terakhir")
				bot.Send(msg)
				return
			}

			err = removeLastEntry(srv)
			if err != nil {
				msg := tgbotapi.NewMessage(chatId, "❌ Gagal menghapus data terakhir")
				bot.Send(msg)
				return
			}

			msg := tgbotapi.NewMessage(chatId, fmt.Sprintf("✅ Data berhasil dihapus:\n%s", lastEntry))
			bot.Send(msg)
			return

		case text == "/history":
			history, err := getLastFiveEntries(srv)
			if err != nil {
				msg := tgbotapi.NewMessage(chatId, "❌ Gagal mengambil riwayat transaksi")
				bot.Send(msg)
				return
			}
			msg := tgbotapi.NewMessage(chatId, history)
			bot.Send(msg)
			return

		case text == "/reminder":
			// Create keyboard for reminder options
			keyboard := tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButton("Harian", tgbotapi.CallbackData("reminder_daily")),
					tgbotapi.NewInlineKeyboardButton("Mingguan", tgbotapi.CallbackData("reminder_weekly")),
				),
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButton("Bulanan", tgbotapi.CallbackData("reminder_monthly")),
					tgbotapi.NewInlineKeyboardButton("Matikan", tgbotapi.CallbackData("reminder_none")),
				),
			)

			msg := tgbotapi.NewMessage(chatId, "🔔 Pilih jenis pengingat:")
			msg.ReplyMarkup = keyboard
			bot.Send(msg)
			return

		default:
			msg := tgbotapi.NewMessage(chatId, "❌ Perintah tidak dikenali. Gunakan /help untuk melihat daftar perintah yang tersedia")
			bot.Send(msg)
			return
		}
	}

	// Handle data input
	parts := strings.Split(text, ",")
	if len(parts) == 3 {
		nominalStr := strings.TrimSpace(parts[0])
		budget := strings.TrimSpace(parts[1])
		keterangan := strings.TrimSpace(parts[2])

		normalizedNominal := normalizeNominal(nominalStr)
		err := appendData(srv, normalizedNominal, budget, keterangan)
		if err != nil {
			bot.Send(tgbotapi.NewMessage(chatId, "❌Terjadi kesalahan saat menambahkan data."))
			return
		}

		summary := getSummary(srv)
		response := fmt.Sprintf(
			"✅Data berhasil ditambahkan ke Google Spreadsheet.\nKamu telah memasukkan:\n💰%d\n🎯%s\n📚%s\n\nTotal Nominal: Rp. %d",
			normalizedNominal, budget, keterangan, summary,
		)
		bot.Send(tgbotapi.NewMessage(chatId, response))
	} else {
		bot.Send(tgbotapi.NewMessage(chatId, "Format salah🙅🏻‍♂️. Gunakan: Nominal, Kategori, Keterangan. \nContoh: 10rb, Makanan, Makan Siang di Kantin\n\nGunakan /help untuk melihat bantuan lengkap"))
	}
}

func authorize(ctx context.Context) (*sheets.Service, error) {
	decodedCreds, err := base64.StdEncoding.DecodeString(credentialsBase64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode credentials: %w", err)
	}

	config, err := google.JWTConfigFromJSON(decodedCreds, sheets.SpreadsheetsScope)
	if err != nil {
		return nil, fmt.Errorf("failed to parse credentials: %w", err)
	}

	client := config.Client(ctx)
	return sheets.NewService(ctx, option.WithHTTPClient(client))
}

func appendData(srv *sheets.Service, nominal int, budget, keterangan string) error {
	resp, err := srv.Spreadsheets.Values.Get(spreadsheetID, "A:A").Do()
	if err != nil {
		return fmt.Errorf("failed to get row count: %w", err)
	}
	nextRow := 1
	if resp != nil && resp.Values != nil {
		nextRow = len(resp.Values) + 1
	}

	// Get current date in DD-MM-YYYY format
	currentDate := time.Now().Format("02-01-2006")

	values := [][]interface{}{{nextRow, currentDate, nominal, budget, keterangan}}
	valueRange := &sheets.ValueRange{Values: values}

	_, err = srv.Spreadsheets.Values.Append(spreadsheetID, "A1", valueRange).ValueInputOption("USER_ENTERED").Do()
	return err
}

func normalizeNominal(nominal string) int {
	nominal = strings.ToLower(strings.ReplaceAll(nominal, " ", ""))
	nominal = strings.ReplaceAll(nominal, ".", "") // remove dot
	var result int
	var err error

	switch {
	case strings.Contains(nominal, "jt"):
		nominal = strings.ReplaceAll(nominal, "jt", "")
		result, err = strconv.Atoi(nominal)
		result *= 1000000
	case strings.Contains(nominal, "rb"):
		nominal = strings.ReplaceAll(nominal, "rb", "")
		result, err = strconv.Atoi(nominal)
		result *= 1000
	case strings.Contains(nominal, "k"):
		nominal = strings.ReplaceAll(nominal, "k", "")
		result, err = strconv.Atoi(nominal)
		result *= 1000
	default:
		result, err = strconv.Atoi(nominal)
	}

	if err != nil {
		log.Printf("Error converting nominal value: %v", err)
		return 0
	}
	return result
}

func getSummary(srv *sheets.Service) int {
	resp, err := srv.Spreadsheets.Values.Get(spreadsheetID, "C:C").Do()
	if err != nil {
		log.Printf("failed to get summary: %v", err)
		return 0
	}
	total := 0
	for _, row := range resp.Values {
		if len(row) > 0 {
			switch v := row[0].(type) {
			case string:
				if val, err := strconv.Atoi(v); err == nil {
					total += val
				}
			case float64:
				total += int(v)
			}
		}
	}
	return total
}

func getLastEntry(srv *sheets.Service) (string, error) {
	resp, err := srv.Spreadsheets.Values.Get(spreadsheetID, "A:E").Do()
	if err != nil {
		return "", fmt.Errorf("failed to get last entry: %w", err)
	}

	if resp == nil || resp.Values == nil || len(resp.Values) < 2 {
		return "Belum ada data yang dimasukkan", nil
	}

	lastRow := resp.Values[len(resp.Values)-1]
	if len(lastRow) < 5 {
		return "Format data tidak valid", nil
	}

	rowNum := fmt.Sprintf("%v", lastRow[0])
	date := fmt.Sprintf("%v", lastRow[1])
	nominal := fmt.Sprintf("%v", lastRow[2])
	budget := fmt.Sprintf("%v", lastRow[3])
	keterangan := fmt.Sprintf("%v", lastRow[4])

	return fmt.Sprintf("🕘 Data terakhir: #%s - 📅%s - 💰%s | 🎯%s | 📚%s", rowNum, date, nominal, budget, keterangan), nil
}

func getWeeklySummary(srv *sheets.Service) (string, error) {
	resp, err := srv.Spreadsheets.Values.Get(spreadsheetID, "A:E").Do()
	if err != nil {
		return "", fmt.Errorf("failed to get weekly summary: %w", err)
	}

	if resp == nil || resp.Values == nil || len(resp.Values) < 2 {
		return "Belum ada data yang dimasukkan", nil
	}

	now := time.Now()
	weekStart := now.AddDate(0, 0, -int(now.Weekday()))
	weekEnd := weekStart.AddDate(0, 0, 6)

	total := 0
	var entries []string

	for _, row := range resp.Values[1:] { // Skip header
		if len(row) < 5 {
			continue
		}

		dateStr := fmt.Sprintf("%v", row[1])
		date, err := time.Parse("02-01-2006", dateStr)
		if err != nil {
			continue
		}

		if date.After(weekStart) && date.Before(weekEnd.AddDate(0, 0, 1)) {
			nominal, _ := strconv.Atoi(fmt.Sprintf("%v", row[2]))
			total += nominal
			entries = append(entries, fmt.Sprintf("📅%s - 💰%v | 🎯%v | 📚%v", dateStr, row[2], row[3], row[4]))
		}
	}

	if len(entries) == 0 {
		return "Tidak ada pengeluaran minggu ini", nil
	}

	result := fmt.Sprintf("📊 Pengeluaran Minggu Ini (Rp. %d):\n\n", total)
	for _, entry := range entries {
		result += entry + "\n"
	}
	return result, nil
}

func getMonthlySummary(srv *sheets.Service) (string, error) {
	resp, err := srv.Spreadsheets.Values.Get(spreadsheetID, "A:E").Do()
	if err != nil {
		return "", fmt.Errorf("failed to get monthly summary: %w", err)
	}

	if resp == nil || resp.Values == nil || len(resp.Values) < 2 {
		return "Belum ada data yang dimasukkan", nil
	}

	now := time.Now()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	monthEnd := monthStart.AddDate(0, 1, -1)

	total := 0
	var entries []string

	for _, row := range resp.Values[1:] { // Skip header
		if len(row) < 5 {
			continue
		}

		dateStr := fmt.Sprintf("%v", row[1])
		date, err := time.Parse("02-01-2006", dateStr)
		if err != nil {
			continue
		}

		if date.After(monthStart.AddDate(0, 0, -1)) && date.Before(monthEnd.AddDate(0, 0, 1)) {
			nominal, _ := strconv.Atoi(fmt.Sprintf("%v", row[2]))
			total += nominal
			entries = append(entries, fmt.Sprintf("📅%s - 💰%v | 🎯%v | 📚%v", dateStr, row[2], row[3], row[4]))
		}
	}

	if len(entries) == 0 {
		return "Tidak ada pengeluaran bulan ini", nil
	}

	result := fmt.Sprintf("📊 Pengeluaran Bulan Ini (Rp. %d):\n\n", total)
	for _, entry := range entries {
		result += entry + "\n"
	}
	return result, nil
}

func removeLastEntry(srv *sheets.Service) error {
	resp, err := srv.Spreadsheets.Values.Get(spreadsheetID, "A:A").Do()
	if err != nil {
		return fmt.Errorf("failed to get row count: %w", err)
	}

	if resp == nil || resp.Values == nil || len(resp.Values) < 2 {
		return fmt.Errorf("no entries to remove")
	}

	lastRow := len(resp.Values)
	rangeToClear := fmt.Sprintf("A%d:E%d", lastRow, lastRow)

	// Create a clear request
	clearRequest := &sheets.ClearValuesRequest{}
	_, err = srv.Spreadsheets.Values.Clear(spreadsheetID, rangeToClear, clearRequest).Do()
	return err
}

func editEntry(srv *sheets.Service, rowNumber int, nominal int, budget, keterangan string) error {
	// Get current date in DD-MM-YYYY format
	currentDate := time.Now().Format("02-01-2006")

	// Prepare the range to update (A:E columns of the specified row)
	rangeToUpdate := fmt.Sprintf("A%d:E%d", rowNumber, rowNumber)
	values := [][]interface{}{{rowNumber, currentDate, nominal, budget, keterangan}}
	valueRange := &sheets.ValueRange{Values: values}

	_, err := srv.Spreadsheets.Values.Update(spreadsheetID, rangeToUpdate, valueRange).ValueInputOption("USER_ENTERED").Do()
	return err
}

func getEntryByNumber(srv *sheets.Service, rowNumber int) (string, error) {
	resp, err := srv.Spreadsheets.Values.Get(spreadsheetID, fmt.Sprintf("A%d:E%d", rowNumber, rowNumber)).Do()
	if err != nil {
		return "", fmt.Errorf("failed to get entry: %w", err)
	}

	if resp == nil || resp.Values == nil || len(resp.Values) == 0 {
		return "", fmt.Errorf("entry not found")
	}

	row := resp.Values[0]
	if len(row) < 5 {
		return "", fmt.Errorf("invalid entry format")
	}

	date := fmt.Sprintf("%v", row[1])
	nominal := fmt.Sprintf("%v", row[2])
	budget := fmt.Sprintf("%v", row[3])
	keterangan := fmt.Sprintf("%v", row[4])

	return fmt.Sprintf("📅%s - 💰%s | 🎯%s | 📚%s", date, nominal, budget, keterangan), nil
}

func getLastFiveEntries(srv *sheets.Service) (string, error) {
	resp, err := srv.Spreadsheets.Values.Get(spreadsheetID, "A:E").Do()
	if err != nil {
		return "", fmt.Errorf("failed to get entries: %w", err)
	}

	if resp == nil || resp.Values == nil || len(resp.Values) < 2 {
		return "Belum ada data yang dimasukkan", nil
	}

	// Get the last 5 entries (skip header row)
	startIdx := len(resp.Values) - 5
	if startIdx < 1 {
		startIdx = 1
	}
	entries := resp.Values[startIdx:]

	var result strings.Builder
	result.WriteString("🧾 5 Transaksi Terakhir:\n\n")

	for i, row := range entries {
		if len(row) < 5 {
			continue
		}

		nominal := fmt.Sprintf("%v", row[2])
		budget := fmt.Sprintf("%v", row[3])
		keterangan := fmt.Sprintf("%v", row[4])

		// Format nominal with thousand separator
		nominalInt, _ := strconv.Atoi(nominal)
		formattedNominal := formatRupiah(nominalInt)

		result.WriteString(fmt.Sprintf("%d. Rp %s - %s - %s\n", i+1, formattedNominal, budget, keterangan))
	}

	return result.String(), nil
}

func formatRupiah(nominal int) string {
	str := strconv.Itoa(nominal)
	var result strings.Builder
	length := len(str)

	for i := 0; i < length; i++ {
		if (length-i)%3 == 0 && i != 0 {
			result.WriteString(".")
		}
		result.WriteByte(str[i])
	}

	return result.String()
}

func handleCallbackQuery(bot *tgbotapi.BotAPI, srv *sheets.Service, callbackQuery *tgbotapi.CallbackQuery) {
	chatID := callbackQuery.Message.Chat.ID
	data := callbackQuery.Data

	if strings.HasPrefix(data, "reminder_") {
		reminderType := ReminderType(strings.TrimPrefix(data, "reminder_"))
		
		// Update user preference
		pref := UserPreference{
			ChatID:       chatID,
			ReminderType: reminderType,
			LastReminder: time.Now(),
		}
		userPreferences[chatID] = pref

		// Save to spreadsheet
		err := saveUserPreference(srv, pref)
		if err != nil {
			bot.Send(tgbotapi.NewMessage(chatID, "❌ Gagal menyimpan pengaturan pengingat"))
			return
		}

		var response string
		switch reminderType {
		case Daily:
			response = "✅ Pengingat harian diaktifkan. Kamu akan menerima ringkasan pengeluaran setiap hari."
		case Weekly:
			response = "✅ Pengingat mingguan diaktifkan. Kamu akan menerima ringkasan pengeluaran setiap minggu."
		case Monthly:
			response = "✅ Pengingat bulanan diaktifkan. Kamu akan menerima ringkasan pengeluaran setiap bulan."
		case None:
			response = "✅ Pengingat dimatikan."
		}

		bot.Send(tgbotapi.NewMessage(chatID, response))
	}
}
