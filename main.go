package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5" // Gunakan v5
	"github.com/joho/godotenv"                                    // untuk load .env
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

// jadi var for bot token, spreadsheet ID, and credentials file path.  Good practice to define them clearly.
var (
	botToken          string // Replace with your actual bot token
	spreadsheetID     string // Replace with your spreadsheet ID
	credentialsBase64 string // Replace with your credentials file path
)

func init() {
	// Load .env if available (for local development)
	_ = godotenv.Load()

	botToken = os.Getenv("BOT_TOKEN")
	spreadsheetID = os.Getenv("SPREADSHEET_ID")
	credentialsBase64 := os.Getenv("GOOGLE_CREDENTIALS_BASE64")
	if botToken == "" || spreadsheetID == "" || credentialsBase64 == "" {
		log.Fatal("Missing env: BOT_TOKEN, SPREADSHEET_ID, or GOOGLE_CREDENTIALS_BASE64")
	}

}

func main() {

	// Create a new Telegram Bot API client.
	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		log.Panicf("failed to create bot API client: %v", err) // Use log.Panicf for non-recoverable errors
	}

	bot.Debug = true // Enable debug mode for more verbose output.  Good for development.

	// Delete the existing webhook
	_, err = bot.Request(tgbotapi.DeleteWebhookConfig{})
	if err != nil {
		log.Printf("failed to delete webhook: %v", err)
	}

	// Create a new update channel.
	updateConfig := tgbotapi.NewUpdate(0)
	updateConfig.Timeout = 60

	updates := bot.GetUpdatesChan(updateConfig)

	// Create a background context.

	// Create Sheets client
	ctx := context.Background()
	sheetsService, err := authorizeFromBase64(ctx, credentialsBase64)
	if err != nil {
		log.Fatalf("Sheets auth failed: %v", err)
	}

	// Webhook setup
	publicURL := os.Getenv("PUBLIC_URL") // e.g. https://mybot.up.railway.app
	webhookURL := publicURL + "/webhook"

	webhookConfig, err := tgbotapi.NewWebhook(webhookURL)
	if err != nil {
		log.Fatalf("Failed to create webhook config: %v", err)
	}
	_, err = bot.Request(webhookConfig)
	if err != nil {
		log.Fatalf("Failed to set webhook: %v", err)
	}

	// Listen handler
	http.HandleFunc("/webhook", func(w http.ResponseWriter, r *http.Request) {
		update, err := bot.HandleUpdate(r)
		if err != nil {
			log.Printf("Error handling update: %v", err)
			return
		}
		if update.Message == nil {
			return
		}

		handleMessage(bot, sheetsService, update.Message)
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("Listening on port %s...", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))

	// Authorize with Google Sheets API.
	srv, err := authorizeFromBase64(ctx, credentialsBase64)
	if err != nil {
		log.Fatalf("failed to authorize with Google Sheets: %v", err) // Use log.Fatalf for critical errors
	}

	// Process updates from Telegram.
	for update := range updates {
		if update.Message == nil {
			continue // Ignore non-message updates.
		}

		chatId := update.Message.Chat.ID
		text := update.Message.Text

		// Split the message text into parts.
		parts := strings.Split(text, ",")
		if len(parts) == 3 {
			nominalStr := strings.TrimSpace(parts[0]) // Trim spaces from input
			budget := strings.TrimSpace(parts[1])
			keterangan := strings.TrimSpace(parts[2])

			// Normalize the nominal value.
			normalizedNominal := normalizeNominal(nominalStr)

			// Append the data to the Google Sheet.
			err = appendData(srv, normalizedNominal, budget, keterangan)
			if err != nil {
				log.Printf("failed to append data: %v", err)
				msg := tgbotapi.NewMessage(chatId, "❌Terjadi kesalahan saat menambahkan data.")
				bot.Send(msg) //check if send was successful
				continue      // Continue to the next update, don't try to send summary
			}

			// Get the summary.
			summary := getSummary(srv)

			// Construct the response message.
			response := fmt.Sprintf("✅Data berhasil ditambahkan ke Google Spreadsheet.\nAnda telah memasukkan: \n💰%d, \n🎯%s, \n📚%s\n\nTotal Nominal: Rp. %d", normalizedNominal, budget, keterangan, summary)
			msg := tgbotapi.NewMessage(chatId, response)
			_, err = bot.Send(msg) //check if send was successful
			if err != nil {
				log.Printf("failed to send message: %v", err)
			}

		} else {
			// Handle invalid input format.
			msg := tgbotapi.NewMessage(chatId, "Format salah🙅🏻‍♂️. Gunakan: Nominal, Kategori, Keterangan untuk apa")
			_, err = bot.Send(msg)
			if err != nil {
				log.Printf("failed to send message: %v", err)
			}
		}
	}
}
func handleMessage(bot *tgbotapi.BotAPI, sheetsService *sheets.Service, message *tgbotapi.Message) {
	// Process the message here
	// For example, you can extract the text from the message and process it
	// ...
}

// authorize function handles Google Sheets API authorization.
func authorizeFromBase64(ctx context.Context, base64Creds string) (*sheets.Service, error) {
	decoded, err := base64.StdEncoding.DecodeString(base64Creds)
	if err != nil {
		return nil, fmt.Errorf("base64 decode failed: %w", err)
	}
	config, err := google.JWTConfigFromJSON(decoded, sheets.SpreadsheetsScope)
	if err != nil {
		return nil, fmt.Errorf("JWT config from JSON failed: %w", err)
	}
	client := config.Client(ctx)
	return sheets.NewService(ctx, option.WithHTTPClient(client))
}

// appendData function appends data to the Google Sheet.
func appendData(srv *sheets.Service, nominal int, budget, keterangan string) error {
	// Get the current number of rows to determine the next row.
	resp, err := srv.Spreadsheets.Values.Get(spreadsheetID, "A:A").Do()
	if err != nil {
		return fmt.Errorf("failed to get row count: %w", err)
	}

	nextRow := 1
	if resp != nil && resp.Values != nil { // Check for nil
		nextRow = len(resp.Values) + 1
	}

	// Prepare the values to be appended.
	values := [][]interface{}{
		{nextRow, nominal, budget, keterangan},
	}
	valueRange := &sheets.ValueRange{
		Values: values,
	}

	// Append the data to the sheet.
	_, err = srv.Spreadsheets.Values.Append(spreadsheetID, "A1", valueRange).ValueInputOption("USER_ENTERED").Do()
	if err != nil {
		return fmt.Errorf("failed to append data: %w", err)
	}
	return nil
}

// normalizeNominal function normalizes the nominal value from the input string.
func normalizeNominal(nominal string) int {
	// Remove non-numeric and non-k/jt/rb characters, and convert to lowercase for easier handling.
	normalized := strings.ToLower(strings.ReplaceAll(nominal, "[^0-9kjt]", "")) // Removed the regex, simpler
	normalized = strings.ReplaceAll(normalized, " ", "")                        //remove spaces

	var result int
	var err error

	// Check for 'k', 'jt', or 'rb' suffixes and perform the appropriate multiplication.
	if strings.Contains(normalized, "k") {
		normalized = strings.ReplaceAll(normalized, "k", "")
		result, err = strconv.Atoi(normalized)
		if err == nil {
			result *= 1000
		}

	} else if strings.Contains(normalized, "jt") {
		normalized = strings.ReplaceAll(normalized, "jt", "")
		result, err = strconv.Atoi(normalized)
		if err == nil {
			result *= 1000000
		}
	} else if strings.Contains(normalized, "rb") {
		normalized = strings.ReplaceAll(normalized, "rb", "")
		result, err = strconv.Atoi(normalized)
		if err == nil {
			result *= 1000
		}
	} else {
		result, err = strconv.Atoi(normalized)
	}
	if err != nil {
		log.Printf("Error converting nominal value: %v, returning 0", err)
		return 0
	}
	return result
}

// getSummary function retrieves the sum of nominal values from the sheet.
func getSummary(srv *sheets.Service) int {
	// Get the values from the "B:B" range (Nominal column).
	resp, err := srv.Spreadsheets.Values.Get(spreadsheetID, "B:B").Do()
	if err != nil {
		log.Printf("failed to get nominal values for summary: %v", err)
		return 0 // Return 0 instead of potentially crashing.  Log the error.
	}

	total := 0
	// Iterate through the rows and sum the nominal values.
	if resp != nil && resp.Values != nil {
		for _, row := range resp.Values {
			if len(row) > 0 {
				nominalStr, ok := row[0].(string) //try to convert to string
				if ok {
					nominal, err := strconv.Atoi(nominalStr)
					if err == nil {
						total += nominal
					} else {
						log.Printf("Error converting row value to int: %v, skipping", err)
					}
				} else {
					nominalFloat, okFloat := row[0].(float64)
					if okFloat {
						total += int(nominalFloat)
					} else {
						log.Printf("value is not string or float64")
					}

				}

			}
		}
	}
	return total
}
