package httpx

import (
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
			Authenticate(secret)(echoUser).ServeHTTP(rec, req)
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
	h := RequestLogger(discardLogger())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = reqctx.RequestID(r.Context())
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if seen == "" || rec.Header().Get("X-Request-Id") != seen {
		t.Fatalf("request id %q not propagated (header %q)", seen, rec.Header().Get("X-Request-Id"))
	}
}
