package httpx

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"RyanDev-21.com/Chirpy/internal/platform/reqctx"
	"RyanDev-21.com/Chirpy/internal/platform/secure"
	"github.com/google/uuid"
)

var secret = uuid.NewString()

func TestAuthenticate(t *testing.T) {
	userID := uuid.New()
	token, _ := secure.MakeJWT(userID, secret, time.Minute)

	echoUser := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := reqctx.User(r.Context())
		if err != nil {
			t.Fatal("handler reached without a user")
		}
		_, _ = w.Write([]byte(id.String()))
	})

	tests := []struct {
		name     string
		header   string
		query    string
		wantCode int
	}{
		{"header ok", "Bearer " + token, "", 200},
		{"missing", "", "", 401},
		{"bad token", "Bearer nope", "", 401},
		{"token in URL is rejected", "", token, 401},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := "/"
			if tt.query != "" {
				url += "?token=" + tt.query
			}
			req := httptest.NewRequest(http.MethodGet, url, nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			rec := httptest.NewRecorder()
			Authenticate(secret, nil, slog.Default())(echoUser).ServeHTTP(rec, req)
			if rec.Code != tt.wantCode {
				t.Fatalf("code = %d, want %d", rec.Code, tt.wantCode)
			}
			if tt.wantCode == 200 && rec.Body.String() != userID.String() {
				t.Fatalf("body = %q", rec.Body.String())
			}
		})
	}
}

func TestDecodeRejectsHugeBodies(t *testing.T) {
	body := `{"x":"` + strings.Repeat("a", maxBodyBytes) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	var v struct{ X string }
	if err := Decode(httptest.NewRecorder(), req, &v); err == nil {
		t.Fatal("expected an error for a body over the limit")
	}
}

func TestCORS(t *testing.T) {
	h := CORS([]string{"https://app.example"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("unexpected allow-origin %q", got)
	}

	req.Header.Set("Origin", "https://app.example")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example" {
		t.Fatalf("allow-origin = %q", got)
	}
}

func TestRequestLoggerSetsRequestID(t *testing.T) {
	var seen string
	h := RequestLogger(discardLogger(), 0)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = reqctx.RequestID(r.Context())
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if seen == "" || rec.Header().Get("X-Request-Id") != seen {
		t.Fatalf("request id %q not propagated (header %q)", seen, rec.Header().Get("X-Request-Id"))
	}
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name    string
		trusted int
		xff     []string
		want    string
	}{
		{"no proxy uses the peer", 0, []string{"6.6.6.6"}, "10.0.0.9"},
		{"one proxy takes the last hop", 1, []string{"203.0.113.7"}, "203.0.113.7"},
		{"spoofed left entries are ignored", 1, []string{"1.2.3.4, 9.9.9.9, 203.0.113.7"}, "203.0.113.7"},
		{"multiple header lines are joined", 1, []string{"1.2.3.4", "203.0.113.7"}, "203.0.113.7"},
		{"two public proxies take the second public hop", 2, []string{"1.2.3.4, 203.0.113.7, 198.51.100.9"}, "203.0.113.7"},
		{"render: internal hop after the client is skipped", 1, []string{"203.0.113.7, 10.26.141.160"}, "203.0.113.7"},
		{"render: spoofed entry and several internal hops", 1, []string{"6.6.6.6, 203.0.113.7, 10.29.161.137, 172.16.0.4"}, "203.0.113.7"},
		{"a spoofed private address is skipped, not trusted", 1, []string{"203.0.113.7, 192.168.1.5"}, "203.0.113.7"},
		{"only internal hops falls back to the peer", 1, []string{"10.0.0.1, 10.0.0.2"}, "10.0.0.9"},
		{"cgnat counts as internal", 1, []string{"203.0.113.7, 100.64.3.2"}, "203.0.113.7"},
		{"missing header falls back to the peer", 1, nil, "10.0.0.9"},
		{"garbage falls back to the peer", 1, []string{"not-an-ip"}, "10.0.0.9"},
		{"fewer public hops than proxies falls back", 2, []string{"203.0.113.7, 10.1.1.1"}, "10.0.0.9"},
		{"ipv6 is normalised", 1, []string{"2001:DB8::1"}, "2001:db8::1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = "10.0.0.9:5555"
			for _, v := range tt.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := ClientIP(r, tt.trusted); got != tt.want {
				t.Fatalf("ClientIP = %q, want %q", got, tt.want)
			}
		})
	}
}
