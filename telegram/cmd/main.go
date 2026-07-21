package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"rag/types"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type CommandHandler func(update tgbotapi.Update) tgbotapi.Chattable

type TextMessageHandler func(update tgbotapi.Update) tgbotapi.Chattable

type App struct {
	bot      *tgbotapi.BotAPI
	handlers map[string]CommandHandler
	http     *http.Client
}

func NewBot(s string) *App {
	bot, err := tgbotapi.NewBotAPI(s)
	if err != nil {
		log.Fatal("error to create bot")
	}
	timeout := 2 * time.Minute
	if v := os.Getenv("TG_REQUEST_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			timeout = d
		} else {
			log.Printf("invalid TG_REQUEST_TIMEOUT=%q, using default %s", v, timeout)
		}
	}

	fmt.Println("Bot started...")
	return &App{
		bot:      bot,
		handlers: make(map[string]CommandHandler),
		http: &http.Client{
			Timeout: timeout,
		},
	}
}

func (t *App) registerCommand(command string, handler CommandHandler) {
	t.handlers[command] = handler
}

func (t *App) registerCommands() {
	commands := map[string]CommandHandler{
		"start": func(update tgbotapi.Update) tgbotapi.Chattable {
			return tgbotapi.NewMessage(update.Message.Chat.ID, "This is the start message")
		},

		"help": func(update tgbotapi.Update) tgbotapi.Chattable {
			return tgbotapi.NewMessage(update.Message.Chat.ID, "This is the help message")
		},

		// Add more commands here

	}

	for cmd, handler := range commands {
		t.registerCommand(cmd, handler)
	}
}

func (t *App) handleUpdate() {
	t.bot.Debug = false
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := t.bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message != nil {
			if update.Message.IsCommand() {
				t.handleCommand(update)
			} else {
				t.handleTextMessage(update)
			}
		}
	}
}

func (t *App) handleCommand(update tgbotapi.Update) {
	command := update.Message.Command()
	if handler, ok := t.handlers[command]; ok {
		response := handler(update)
		t.bot.Send(response)
	} else {
		msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Unknown command")
		t.bot.Send(msg)
	}
}

func (t *App) handleTextMessage(update tgbotapi.Update) {
	chatID := update.Message.Chat.ID
	text := update.Message.Text

	if _, err := t.bot.Send(tgbotapi.NewMessage(chatID, "⌛️ В обработке...")); err != nil {
		fmt.Printf("Error sending processing message: %v", err)
	}

	go func(chatID int64, text string) {
		fmt.Println("received from tg_bot: ", text)
		prompt := types.QueryParams{
			Prompt:   text,
			UseLocal: true,
		}
		b, _ := json.Marshal(prompt)

		url := "http://localhost:3000/api/v1/request"
		ctx, cancel := context.WithTimeout(context.Background(), t.http.Timeout)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
		if err != nil {
			fmt.Printf("Error creating the request: %v", err)
			msg := tgbotapi.NewMessage(chatID, "Semething whent wrong...")
			t.bot.Send(msg)
			return
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := t.http.Do(req)
		if err != nil {
			fmt.Printf("Error to send request to host: %v", err)
			msg := tgbotapi.NewMessage(chatID, "Semething whent wrong...")
			t.bot.Send(msg)
			return
		}
		defer resp.Body.Close()

		fmt.Println("Pass LLM...")

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			fmt.Printf("error receiving response from server: %s - %s\n", resp.Status, body)
			msg := tgbotapi.NewMessage(chatID, "Semething whent wrong...")
			t.bot.Send(msg)
			return
		}

		var rsp types.SearchResponse
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			msg := tgbotapi.NewMessage(chatID, "Semething whent wrong...")
			t.bot.Send(msg)
			return
		}

		if err := json.Unmarshal(respBody, &rsp); err != nil {
			msg := tgbotapi.NewMessage(chatID, "Semething whent wrong...")
			t.bot.Send(msg)
			return
		}

		// If no pattern matched, use the default handler
		msg := tgbotapi.NewMessage(chatID, rsp.Answer)
		t.bot.Send(msg)
	}(chatID, text)
}

func (a *App) Start() {
	a.registerCommands()

	fmt.Printf("Starting bot @%s\n", a.bot.Self.UserName)
	a.handleUpdate()
}

func main() {
	b := NewBot(os.Getenv("TG_KEY"))
	b.Start()
}
