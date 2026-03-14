package setup

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync/atomic"

	"RyanDev-21.com/Chirpy/internal/database"
	"RyanDev-21.com/Chirpy/pkg/response"
)

func (cfg *APIConfig) MiddlewareMetricsInc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg.fileServerHits.Add(1)
		fmt.Printf("server hits: %v\n", cfg.fileServerHits.Load())
		next.ServeHTTP(w, r)
	})
}

func (cfg *APIConfig) ResetHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	old := cfg.fileServerHits.Swap(0)
	hits := cfg.fileServerHits.Load()
	fmt.Fprintf(w, "Old Hits: %v , New Hits :%v", old, hits)
}

// need to refactor this guy move it to admin or smth
func (cfg *APIConfig) UserResetHandle(w http.ResponseWriter, r *http.Request) {
	if cfg.Platform != "dev" {
		response.Error(w, 403, "Forbidden")
		return
	}
	err := cfg.Queries.DeleteUser(r.Context())
	if err != nil {
		fmt.Printf("failed to delete all users %s", err)
		response.Error(w, 500, "Something went wrong")
		return
	}

	respondStruct := struct {
		Msg string `json:"msg"`
	}{
		Msg: "Successfully deleted",
	}
	response.JSON(w, 200, respondStruct)
}

// maybe i could encapsulate it but
type APIConfig struct {
	fileServerHits atomic.Int32
	Queries        *database.Queries
	Platform       string
	Secret         string
	Logger         slog.Logger
}

func InitApiConfig(Queries *database.Queries, Platform string, secret string) *APIConfig {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	return &APIConfig{
		Queries:  Queries,
		Platform: Platform,
		Secret:   secret,
		Logger:   *logger,
	}
}
