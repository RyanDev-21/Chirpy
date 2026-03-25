package aimodels

import (
	"fmt"
	"net/http"

	//	chatmodel "RyanDev-21.com/Chirpy/internal/chatModel"
	//"RyanDev-21.com/Chirpy/pkg/middleware"
	"RyanDev-21.com/Chirpy/pkg/response"
)

type genAiHandler struct {
	genAiService GenAi
}

func NewGenAIHandler(genAIService GenAi) *genAiHandler {
	return &genAiHandler{
		genAiService: genAIService,
	}
}

func (h genAiHandler) GenAIDummyHandler(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		response.Error(w, 500, "streaming not supported")
		return
	}
	w.Header().Set("Content-type", "text/plain")
	// userID, err := middleware.GetContextKey(r.Context(), "user")
	// if err != nil {
	// 	response.Error(w, 500, "internal server error")
	// 	return
	// }

	// log.Printf("request from user:%v", userID)
	msgCh := make(chan string, 1000)
	go h.genAiService.GetMessage(r.Context(), msgCh)
	for msg := range msgCh {
		fmt.Fprintf(w, "%s\n", msg)
		flusher.Flush()
	}
}
