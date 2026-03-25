package aimodels

import (
	chatmodel "RyanDev-21.com/Chirpy/internal/chatModel"
	"google.golang.org/genai"
)

type genAiResponse struct {
	Msg string `json:"msg"`
}

type genAi struct {
	client *genai.Client
	hub    *chatmodel.Hub
}

type Message struct {
	ID      int    `json:"id"`
	Speaker string `json:"speaker"`
	Text    string `json:"text"`
}
