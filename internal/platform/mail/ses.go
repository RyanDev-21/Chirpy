package mail

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// SES sends through Amazon SES (API v2, SendEmail), signed with AWS
// Signature Version 4. No SDK: one signed POST is all it takes.
type SES struct {
	from, region      string
	accessKey, secret string
	endpoint          string // https://email.<region>.amazonaws.com
	client            *http.Client
	now               func() time.Time
}

func NewSES(region, accessKey, secret, from string) *SES {
	return &SES{from: from, region: region, accessKey: accessKey, secret: secret,
		endpoint: "https://email." + region + ".amazonaws.com", client: &http.Client{Timeout: 10 * time.Second}, now: time.Now}
}

type sesContent struct {
	Data    string `json:"Data"`
	Charset string `json:"Charset"`
}

func (s *SES) Send(ctx context.Context, m Message) error {
	body := map[string]any{"Text": sesContent{m.Text, "UTF-8"}}
	if m.HTML != "" {
		body["Html"] = sesContent{m.HTML, "UTF-8"}
	}
	payload, err := json.Marshal(map[string]any{
		"FromEmailAddress": s.from,
		"Destination":      map[string][]string{"ToAddresses": {m.To}},
		"Content":          map[string]any{"Simple": map[string]any{"Subject": sesContent{m.Subject, "UTF-8"}, "Body": body}},
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint+"/v2/email/outbound-emails", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	signV4(req, payload, s.accessKey, s.secret, s.region, "ses", s.now())
	res, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("ses: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		// names the problem (unverified address, sandbox, throttling); never
		// the message or the key
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return fmt.Errorf("ses: status %d: %s", res.StatusCode, redactEmails(string(bytes.TrimSpace(msg))))
	}
	return nil
}

func hmacSHA(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

// signV4 adds X-Amz-Date and Authorization (AWS Signature Version 4,
// signing every header set so far plus host). The path must already be
// URI-encoded; the query is used as given (sorted).
func signV4(req *http.Request, payload []byte, accessKey, secret, region, service string, now time.Time) {
	now = now.UTC()
	amzDate := now.Format("20060102T150405Z")
	day := now.Format("20060102")
	req.Header.Set("X-Amz-Date", amzDate)

	hdrs := map[string]string{"host": req.URL.Host}
	for k, v := range req.Header {
		hdrs[strings.ToLower(k)] = strings.TrimSpace(strings.Join(v, ","))
	}
	names := make([]string, 0, len(hdrs))
	for k := range hdrs {
		names = append(names, k)
	}
	sort.Strings(names)
	var canon strings.Builder
	for _, k := range names {
		canon.WriteString(k + ":" + hdrs[k] + "\n")
	}
	signed := strings.Join(names, ";")
	path := req.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	q := req.URL.Query()
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var query []string
	for _, k := range keys {
		for _, v := range q[k] {
			query = append(query, awsEscape(k)+"="+awsEscape(v))
		}
	}
	bodySum := sha256.Sum256(payload)
	canonical := strings.Join([]string{req.Method, path, strings.Join(query, "&"), canon.String(), signed, hex.EncodeToString(bodySum[:])}, "\n")
	reqSum := sha256.Sum256([]byte(canonical))
	scope := day + "/" + region + "/" + service + "/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(reqSum[:])
	key := hmacSHA([]byte("AWS4"+secret), day)
	key = hmacSHA(key, region)
	key = hmacSHA(key, service)
	key = hmacSHA(key, "aws4_request")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+accessKey+"/"+scope+", SignedHeaders="+signed+", Signature="+hex.EncodeToString(hmacSHA(key, toSign)))
}

// awsEscape percent-encodes everything but A-Z a-z 0-9 - _ . ~.
func awsEscape(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || '0' <= c && c <= '9' || c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// Fallback tries Primary, then Secondary if that fails (for example SES
// first, Resend if SES is down or not out of its sandbox yet).
type Fallback struct {
	Primary, Secondary Sender
	OnFail             func(err error)
}

func (f Fallback) Send(ctx context.Context, m Message) error {
	err := f.Primary.Send(ctx, m)
	if err == nil || f.Secondary == nil {
		return err
	}
	if f.OnFail != nil {
		f.OnFail(err)
	}
	if err2 := f.Secondary.Send(ctx, m); err2 != nil {
		return fmt.Errorf("%w; fallback: %w", err, err2)
	}
	return nil
}
