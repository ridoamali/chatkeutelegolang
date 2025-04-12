package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

var (
	botToken          string
	spreadsheetID     string
	credentialsBase64 string
	mode              string
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
			"✅Data berhasil ditambahkan ke Google Spreadsheet.\nAnda telah memasukkan:\n💰%d\n🎯%s\n📚%s\n\nTotal Nominal: Rp. %d",
			normalizedNominal, budget, keterangan, summary,
		)
		bot.Send(tgbotapi.NewMessage(chatId, response))
	} else {
		bot.Send(tgbotapi.NewMessage(chatId, "Format salah🙅🏻‍♂️. Gunakan: Nominal, Kategori, Keterangan. \n Contoh: 10rb, Makanan, Makan Siang di Kantin"))
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

	values := [][]interface{}{{nextRow, nominal, budget, keterangan}}
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
	resp, err := srv.Spreadsheets.Values.Get(spreadsheetID, "B:B").Do()
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
