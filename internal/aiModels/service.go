package aimodels

import (
	"context"
	"fmt"
	"log"
	//"sync"

	chatmodel "RyanDev-21.com/Chirpy/internal/chatModel"
	//"github.com/google/uuid"
	"google.golang.org/genai"
)

type GenAi interface {
	GetMessage(ctx context.Context, msgCh chan string) error
}

func NewAiService(client *genai.Client, hub *chatmodel.Hub) GenAi {
	return &genAi{
		client: client,
		hub:    hub,
	}
}

func (ai *genAi) GetMessage(ctx context.Context, msgCh chan string) error {
	contents := genai.Text(fmt.Sprintf("%v", dummyConvo))
	config := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{
				{Text: "Below is a history of 100 messages from User X And Y. Analyze their tone, slang, and response length. Based on this pattern, suggest a reply to the following new message for User Leo:only one output format  for Leo to send and don't exceding 50words {msg:[here]} only this msg list should be responded nothing more nothing less(none of those reasoning and stuff need to respond) "},
			},
		},
	}
	// valid := ai.hub.CheckWsConnection(userID)
	// if !valid {
	// 	return nil, chatmodel.ErrNotConnectedToWs
	// }

	modelName := "gemini-3.1-flash-lite-preview"
	defer close(msgCh)
	stream := ai.client.Models.GenerateContentStream(
		ctx,
		modelName,
		contents,
		config,
	)

	for chunk, err := range stream {
		if err != nil {
			log.Printf("stream error from goroutine  %s", err)
			break
		}

		text := chunk.Candidates[0].Content.Parts[0].Text
		for _, r := range text {
			msgCh <- string(r)
		}
	}

	return nil
}
