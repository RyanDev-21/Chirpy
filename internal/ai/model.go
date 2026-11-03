package ai

import (
	"log/slog"

	"google.golang.org/genai"
)

type genAi struct {
	client *genai.Client
	logger *slog.Logger
}

type Message struct {
	ID      int    `json:"id"`
	Speaker string `json:"speaker"`
	Text    string `json:"text"`
}
