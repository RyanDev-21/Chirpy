package mail

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
