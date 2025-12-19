package middleware

import (
	"context"
	"net/http"

	"RyanDev-21.com/Chirpy/pkg/auth"
	"RyanDev-21.com/Chirpy/pkg/response"
)
type contextKey string

const USERCONTEXTKEY  contextKey= "user_id"


func AuthMiddleWare(next http.HandlerFunc,secret string)http.Handler{
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, err:= auth.GetBearerToken(r.Header)
		if err !=nil{
			response.Error(w,400,"token is required")
			return
		}

		userID ,err := auth.ValidateJWT(token,secret)
		if err !=nil{
			response.Error(w,403,"unauthorized")
			return
		}
		ctx := context.WithValue(r.Context(),USERCONTEXTKEY,userID)
		next.ServeHTTP(w,r.WithContext(ctx))
			
	})	
}
