package ai

import (
	"context"
	"fmt"
	"log"

	"google.golang.org/genai"
)

type GenAi interface {
	GetMessage(ctx context.Context, msgCh chan string) error
}

func NewAiService(client *genai.Client) GenAi {
	return &genAi{client: client}
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

		if len(chunk.Candidates) == 0 || chunk.Candidates[0].Content == nil || len(chunk.Candidates[0].Content.Parts) == 0 {
			continue
		}
		select {
		case msgCh <- chunk.Candidates[0].Content.Parts[0].Text:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}
