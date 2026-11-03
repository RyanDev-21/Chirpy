// Package mail sends transactional email (verification and reset codes).
package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

type Sender interface {
	Send(ctx context.Context, m Message) error
}

var ErrNotConfigured = errors.New("email sending is not configured")

// Resend sends through https://resend.com.
type Resend struct {
	apiKey   string
	from     string
	endpoint string
	client   *http.Client
}

func NewResend(apiKey, from string) *Resend {
	return &Resend{apiKey: apiKey, from: from, endpoint: "https://api.resend.com/emails", client: &http.Client{Timeout: 10 * time.Second}}
}

func (r *Resend) Send(ctx context.Context, m Message) error {
	body, err := json.Marshal(map[string]any{
		"from": r.from, "to": []string{m.To}, "subject": m.Subject, "text": m.Text, "html": m.HTML,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.apiKey)
	req.Header.Set("Content-Type", "application/json")
	res, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("resend: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		// the response body names the problem (bad key, unverified domain);
		// it never contains the message or the key
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return fmt.Errorf("resend: status %d: %s", res.StatusCode, bytes.TrimSpace(msg))
	}
	return nil
}

// Log writes messages to the log instead of sending them. Development only:
// it prints the codes.
type Log struct{ Logger *slog.Logger }

func (l Log) Send(ctx context.Context, m Message) error {
	l.Logger.InfoContext(ctx, "email (not sent, PLATFORM=dev)", "to", m.To, "subject", m.Subject, "text", m.Text)
	return nil
}

// Disabled refuses to send; used when no provider is configured outside dev.
type Disabled struct{}

func (Disabled) Send(context.Context, Message) error { return ErrNotConfigured }

// Memory records messages; for tests.
type Memory struct {
	mu   sync.Mutex
	sent []Message
}

func (m *Memory) Send(_ context.Context, msg Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return nil
}

// Last returns the most recent message sent to `to`.
func (m *Memory) Last(to string) (Message, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.sent) - 1; i >= 0; i-- {
		if m.sent[i].To == to {
			return m.sent[i], true
		}
	}
	return Message{}, false
}

// Count returns how many messages went to `to`.
func (m *Memory) Count(to string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, s := range m.sent {
		if s.To == to {
			n++
		}
	}
	return n
}
