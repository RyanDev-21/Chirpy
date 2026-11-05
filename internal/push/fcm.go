package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// ErrDeadToken: the device token is no longer valid (app uninstalled,
// token rotated); the device should be forgotten.
var ErrDeadToken = errors.New("device token is no longer valid")

// Message is one notification to one device.
type Message struct {
	Token string
	Title string
	Body  string
	// Tag groups notifications on the phone: a newer one with the same tag
	// replaces the older one (one notification per chat).
	Tag  string
	Data map[string]string
	// DataOnly: no visible notification; the app handles it itself (an
	// incoming call shows a full-screen ringing UI). TTL drops it if it
	// can't be delivered in time.
	DataOnly bool
	TTL      time.Duration
}

// Sender delivers notifications.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// FCM sends through Firebase Cloud Messaging (HTTP v1 API) with a service
// account key.
type FCM struct {
	project string
	client  *http.Client
	url     string
}

const fcmScope = "https://www.googleapis.com/auth/firebase.messaging"

// NewFCM reads a service account key (the JSON file from Firebase project
// settings).
func NewFCM(ctx context.Context, serviceAccountJSON []byte) (*FCM, error) {
	// only a service account key is accepted: other credential types in
	// this variable could make the server fetch tokens from elsewhere
	creds, err := google.CredentialsFromJSONWithType(ctx, serviceAccountJSON, google.ServiceAccount, fcmScope)
	if err != nil {
		return nil, fmt.Errorf("read FIREBASE_SERVICE_ACCOUNT: %w", err)
	}
	if creds.ProjectID == "" {
		return nil, errors.New("FIREBASE_SERVICE_ACCOUNT has no project_id")
	}
	c := oauth2.NewClient(ctx, creds.TokenSource)
	c.Timeout = 10 * time.Second
	return &FCM{project: creds.ProjectID, client: c,
		url: "https://fcm.googleapis.com/v1/projects/" + creds.ProjectID + "/messages:send"}, nil
}

type fcmRequest struct {
	ValidateOnly bool       `json:"validate_only,omitempty"`
	Message      fcmMessage `json:"message"`
}

type fcmMessage struct {
	Token        string            `json:"token"`
	Notification map[string]string `json:"notification,omitempty"`
	Data         map[string]string `json:"data,omitempty"`
	Android      map[string]any    `json:"android,omitempty"`
	Webpush      map[string]any    `json:"webpush,omitempty"`
}

func (f *FCM) post(ctx context.Context, req fcmRequest) error {
	raw, err := json.Marshal(req)
	if err != nil {
		return err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, f.url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", "application/json")
	res, err := f.client.Do(r)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	switch {
	case res.StatusCode == http.StatusOK:
		return nil
	case res.StatusCode == http.StatusNotFound || strings.Contains(string(body), "UNREGISTERED"),
		res.StatusCode == http.StatusBadRequest && strings.Contains(string(body), "registration token"):
		return ErrDeadToken
	}
	return fmt.Errorf("fcm: %s: %s", res.Status, body)
}

func (f *FCM) Send(ctx context.Context, m Message) error {
	if m.DataOnly {
		android := map[string]any{"priority": "high"}
		web := map[string]string{"Urgency": "high"}
		if m.TTL > 0 {
			android["ttl"] = fmt.Sprintf("%ds", int(m.TTL.Seconds()))
			web["TTL"] = fmt.Sprint(int(m.TTL.Seconds()))
		}
		return f.post(ctx, fcmRequest{Message: fcmMessage{Token: m.Token, Data: m.Data, Android: android,
			Webpush: map[string]any{"headers": web}}})
	}
	return f.post(ctx, fcmRequest{Message: fcmMessage{
		Token:        m.Token,
		Notification: map[string]string{"title": m.Title, "body": m.Body},
		Data:         m.Data,
		Android: map[string]any{
			"priority":     "high",
			"collapse_key": m.Tag,
			"notification": map[string]string{"tag": m.Tag, "channel_id": "messages"},
		},
	}})
}

// Check proves the key works without sending anything: Google validates a
// message to a made-up token. A complaint about the token means the
// credentials were accepted.
func (f *FCM) Check(ctx context.Context) error {
	err := f.post(ctx, fcmRequest{ValidateOnly: true, Message: fcmMessage{
		Token: "chirpy-credentials-check", Notification: map[string]string{"title": "check"},
	}})
	if err == nil || errors.Is(err, ErrDeadToken) || strings.Contains(err.Error(), "INVALID_ARGUMENT") {
		return nil
	}
	return err
}

// Project is the Firebase project the key belongs to.
func (f *FCM) Project() string { return f.project }
