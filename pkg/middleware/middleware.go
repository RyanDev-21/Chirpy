package middleware

import (
	"context"
	"errors"
	"net/http"

	"RyanDev-21.com/Chirpy/pkg/auth"
	"RyanDev-21.com/Chirpy/pkg/response"
	"github.com/google/uuid"
)

type contextKey string

const USERCONTEXTKEY contextKey = "user_id"

func AuthMiddleWare(next http.HandlerFunc, secret string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, err := auth.GetBearerToken(r.Header)
		if err != nil {
			response.Error(w, 400, "token is required")
			return
		}

		userID, err := auth.ValidateJWT(token, secret)
		if err != nil {
			response.Error(w, 403, "unauthorized")
			return
		}
		ctx := context.WithValue(r.Context(), USERCONTEXTKEY, userID)
		next.ServeHTTP(w, r.WithContext(ctx))

	})
}

func GetUserContextKey(context context.Context) (*uuid.UUID, error) {
	userID, ok := context.Value(USERCONTEXTKEY).(uuid.UUID)
	if !ok {
		return nil, errors.New("userID not found in context")
	}
	return &userID, nil
}

func GetPathValue(pathName string, req *http.Request) (*uuid.UUID, error) {
	stringPathID := req.PathValue(pathName)
	if stringPathID == "" {
		return nil, errors.New("path value not found")
	}
	pathID, err := uuid.Parse(stringPathID)
	if err != nil {
		return nil, errors.New("failed to parse into uuid")
	}
	return &pathID, nil
}

// func MiddleWareLog(next http.HandlerFunc) http.Handler {
// 	Logger := slog.Default()
// 	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 		reqID := uuid.New().String()
// 		Logger = Logger.With("req_id", reqID)
// 		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), "logger", Logger)))
// 	})
// }
