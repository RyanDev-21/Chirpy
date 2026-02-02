package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"RyanDev-21.com/Chirpy/pkg/auth"
	"RyanDev-21.com/Chirpy/pkg/response"
	"github.com/google/uuid"
)

type ContextKey string

//need to refactor this
const USERCONTEXTKEY ContextKey = "user_id"
const REQCONTEXTKEY ContextKey = "reqlog_id"
const PAYLOADCONTEXT ContextKey = "paylodContext"

type payload struct{
	userContext uuid.UUID
	reqContext uuid.UUID
}

func AuthMiddleWare(next http.HandlerFunc, secret string,logger *slog.Logger) http.Handler {
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
		var contextStruct payload
		reqID,err:= getReqContextKey(r.Context())
		if err!=nil {
			logger.Warn("failed to get the req id")
			contextStruct =payload{
				userContext:userID ,
			}
		}else{
				contextStruct = payload{
					userContext: userID,
					reqContext: reqID,
				}
		}
		
		ctx := context.WithValue(r.Context(),PAYLOADCONTEXT,contextStruct)
		next.ServeHTTP(w, r.WithContext(ctx))

	})
}

func GetContextKey(context context.Context,field string) (*uuid.UUID, error) {
	payload, ok := context.Value(PAYLOADCONTEXT).(payload)
	if !ok {
		return nil, errors.New("userkey not found in context")
	}
	switch field{
	case "user":
		return &payload.userContext, nil
	case "request": 
		return &payload.reqContext, nil

	}
	return nil,errors.New("no supported field")
}
func getReqContextKey(context context.Context)(uuid.UUID,error){
	reqID,ok:= context.Value(REQCONTEXTKEY).(uuid.UUID)
	if !ok{
		return reqID,errors.New("failed to get the request key")

	}
	return reqID,nil
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

//	func MiddleWareLog(next http.HandlerFunc) http.Handler {
//		Logger := slog.Default()
//		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
//			reqID := uuid.New().String()
//			Logger = Logger.With("req_id", reqID)
//			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), "logger", Logger)))
//		})
//	}
func MiddelWareLog(next http.HandlerFunc) http.Handler {

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqRandomID := uuid.New()	
		method := r.Method
		path := r.URL.Path
		slog.Info("incoming req from ","reqID",reqRandomID,"method",method,"path",path);
		ctx := context.WithValue(r.Context(),PAYLOADCONTEXT, payload{
			reqContext: reqRandomID,
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	});
}
