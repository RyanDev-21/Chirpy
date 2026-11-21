package mail

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestResendSendsTheExpectedRequest(t *testing.T) {
	var got map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"id":"x"}`))
	}))
	defer srv.Close()
	r := NewResend("re_test", "Chirpy <no-reply@mail.example>")
	r.endpoint = srv.URL
	if err := r.Send(context.Background(), Message{To: "a@b.io", Subject: "Hi", Text: "code 123456"}); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer re_test" || got["from"] != "Chirpy <no-reply@mail.example>" || got["subject"] != "Hi" {
		t.Fatalf("request = %v (auth %q)", got, auth)
	}
	if to, _ := got["to"].([]any); len(to) != 1 || to[0] != "a@b.io" {
		t.Fatalf("to = %v", got["to"])
	}
}

func TestResendReportsErrorsWithoutTheKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`{"message":"domain not verified"}`))
	}))
	defer srv.Close()
	r := NewResend("re_secret_key", "x@y")
	r.endpoint = srv.URL
	err := r.Send(context.Background(), Message{To: "a@b.io"})
	if err == nil || !strings.Contains(err.Error(), "domain not verified") || strings.Contains(err.Error(), "re_secret_key") {
		t.Fatalf("err = %v", err)
	}
}

// AWS's published Signature Version 4 example ("get-vanilla" from the
// SigV4 test suite).
func TestSignV4MatchesAWSExample(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://example.amazonaws.com/", nil)
	signV4(req, nil, "AKIDEXAMPLE", "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", "us-east-1", "service", time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC))
	want := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders=host;x-amz-date, Signature=5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31"
	if got := req.Header.Get("Authorization"); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestSESSendsTheExpectedRequest(t *testing.T) {
	var got struct {
		From        string `json:"FromEmailAddress"`
		Destination struct {
			To []string `json:"ToAddresses"`
		}
		Content struct {
			Simple struct {
				Subject struct{ Data string }
				Body    struct{ Text, Html struct{ Data string } }
			}
		}
	}
	var auth, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, path = r.Header.Get("Authorization"), r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"MessageId":"x"}`))
	}))
	defer srv.Close()
	s := NewSES("ap-southeast-1", "AKIATEST", "secret", "Chirpy <no-reply@mail.example>")
	s.endpoint = srv.URL
	if err := s.Send(context.Background(), Message{To: "a@b.io", Subject: "Hi", Text: "code 123456", HTML: "<b>123456</b>"}); err != nil {
		t.Fatal(err)
	}
	if path != "/v2/email/outbound-emails" || !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=AKIATEST/") || !strings.Contains(auth, "/ap-southeast-1/ses/aws4_request") {
		t.Fatalf("path %s auth %s", path, auth)
	}
	if got.From != "Chirpy <no-reply@mail.example>" || len(got.Destination.To) != 1 || got.Destination.To[0] != "a@b.io" ||
		got.Content.Simple.Subject.Data != "Hi" || got.Content.Simple.Body.Text.Data != "code 123456" || got.Content.Simple.Body.Html.Data != "<b>123456</b>" {
		t.Fatalf("request %+v", got)
	}
}

func TestSESErrorsDontLeakTheSecret(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"message":"Email address is not verified."}`))
	}))
	defer srv.Close()
	s := NewSES("ap-southeast-1", "AKIATEST", "super-secret-key", "x@y")
	s.endpoint = srv.URL
	err := s.Send(context.Background(), Message{To: "a@b.io"})
	if err == nil || !strings.Contains(err.Error(), "not verified") || strings.Contains(err.Error(), "super-secret-key") {
		t.Fatalf("err = %v", err)
	}
}

type failing struct{ err error }

func (f failing) Send(context.Context, Message) error { return f.err }

func TestFallback(t *testing.T) {
	mem := &Memory{}
	var failed error
	f := Fallback{Primary: failing{errors.New("ses down")}, Secondary: mem, OnFail: func(err error) { failed = err }}
	if err := f.Send(context.Background(), Message{To: "a@b.io"}); err != nil || mem.Count("a@b.io") != 1 || failed == nil {
		t.Fatalf("err %v, sent %d, failed %v", err, mem.Count("a@b.io"), failed)
	}
	both := Fallback{Primary: failing{errors.New("ses down")}, Secondary: failing{errors.New("resend down")}}
	if err := both.Send(context.Background(), Message{To: "a@b.io"}); err == nil || !strings.Contains(err.Error(), "resend down") {
		t.Fatalf("err %v", err)
	}
	ok := Fallback{Primary: mem, Secondary: failing{errors.New("unused")}}
	if err := ok.Send(context.Background(), Message{To: "c@d.io"}); err != nil || mem.Count("c@d.io") != 1 {
		t.Fatalf("primary ok: %v", err)
	}
}
