package ai

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"RyanDev-21.com/Chirpy/internal/platform/httpx"
)

type Handler struct {
	svc GenAi
}

func NewGenAIHandler(svc GenAi) *Handler {
	return &Handler{svc: svc}
}

// Suggest streams a suggested reply as plain text, one chunk per line.
func (h *Handler) Suggest(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpx.ServerError(w, r, errors.New("response writer does not support streaming"))
		return
	}
	// streaming outlives the server's default write timeout
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(2 * time.Minute))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	msgCh := make(chan string, 64)
	go h.svc.GetMessage(r.Context(), msgCh)
	for msg := range msgCh {
		fmt.Fprintf(w, "%s\n", msg)
		flusher.Flush()
	}
}
