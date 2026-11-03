package ai

import "google.golang.org/genai"

type genAi struct {
	client *genai.Client
}

type Message struct {
	ID      int    `json:"id"`
	Speaker string `json:"speaker"`
	Text    string `json:"text"`
}
