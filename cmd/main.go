package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	authClient "RyanDev-21.com/Chirpy/internal/auth"
	"RyanDev-21.com/Chirpy/internal/chat"
	chatmodel "RyanDev-21.com/Chirpy/internal/chat/chatModel"
	mq "RyanDev-21.com/Chirpy/internal/customMq"
	"RyanDev-21.com/Chirpy/internal/database"
	"RyanDev-21.com/Chirpy/internal/groups"
//	rabbitmq "RyanDev-21.com/Chirpy/internal/rabbitMq"
	"RyanDev-21.com/Chirpy/internal/users"
	"RyanDev-21.com/Chirpy/pkg/auth"
	"RyanDev-21.com/Chirpy/pkg/middleware"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

var CreateGroup = "CreateGroup"

const (
	Port = ":8080"
)

type apiConfig struct{
	fileServerHits atomic.Int32	
	queries *database.Queries
	platform string	
	secret string
	polkaKey string
}

type User struct{
	ID uuid.UUID  `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Email string `json:"email"`
	IsRED bool `json:"is_chirpy_red"`
}

type Chirp struct{
	ID uuid.UUID `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Body string `json:"body"`
	UserID uuid.UUID `json:"user_id"`

}

type AuthUser struct{
	User
	Token string `json:"token"`
	RefreshToken string `json:"refresh_token"`
} 

func APIHandle(w http.ResponseWriter,r *http.Request){
	w.Header().Set("Content-Type","text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func (cfg *apiConfig) middlewareMeticsInc(next http.Handler)http.Handler{
	return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		cfg.fileServerHits.Add(1)
		log.Printf("server hits: %v\n",cfg.fileServerHits.Load())
		next.ServeHTTP(w,r)
	})
	 	
}
	
func middleWareLog(next http.Handler)http.Handler{
	return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		log.Printf("%s %s",r.Method,r.URL.Path)
		next.ServeHTTP(w,r)
	})
}

func (cfg *apiConfig)HitHandle(w http.ResponseWriter,r *http.Request){
	w.Header().Set("Content-Type","text/html")
	w.WriteHeader(http.StatusOK)
	hits := cfg.fileServerHits.Load() 
	fmt.Fprintf(w,`<html>
  <body>
    <h1>Welcome, Chirpy Admin</h1>
    <p>Chirpy has been visited %d times!</p>
  </body>
</html>`,hits) 
}

func (cfg *apiConfig)ResetHandle(w http.ResponseWriter,r *http.Request){
	w.Header().Set("Content-Type","text/plain")
	w.WriteHeader(http.StatusOK)
	old :=cfg.fileServerHits.Swap(0)
	hits := cfg.fileServerHits.Load() 
	fmt.Fprintf(w,"Old Hits: %v , New Hits :%v",old,hits)
}
type responseError struct{
			Error string `json:"error"`
}



const InternalError = "Something went wrong"

var keywords = []string{
	"kerfuffle",
	"sharbert",
	"fornax",
}
type returnVals struct{
		Body string `json:"cleanded_body"`
}
func replaceAsterids(body string)string{
	var updatedBody string
	turnLower := strings.ToLower(body)
	var keyword string
	for _,v:= range keywords{
		if strings.Contains(turnLower,v){
			idx := strings.Index(turnLower,v)
				
			keyword = body[idx: idx+len(v)]
			
			break
		}

	}
	if keyword == ""{
		return body
	}

	
	updatedBody= strings.ReplaceAll(body,keyword,"****")
	return updatedBody
}


func respondWithError(w http.ResponseWriter,code int,msg string){
	responseBody := responseError{
		Error: msg,
	}	
	res,err:= json.Marshal(responseBody)
	if err!=nil{
		log.Printf("Error marshaling json:%s",err)
		http.Error(w,"Internal Server Error",http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type","application/json")
	w.WriteHeader(code)
	w.Write(res)

}


//Marshal the struct or any interface into json and write it to the respond
//takes io.write ,statusCode and any interface
func respondWithJSON(w http.ResponseWriter,code int,payload any){
	w.Header().Set("Content-Type","application/json")
	w.WriteHeader(code)
	res , err:= json.Marshal(payload)
	if err !=nil{
		log.Printf("error mashaling json: %s",err)	
		http.Error(w,"Internal Server Error",http.StatusInternalServerError)
		return
	}
	w.Write(res)

}

func (cfg *apiConfig)GetChirpHandle(w http.ResponseWriter,r *http.Request){
	items := []Chirp{}
	var chirps []database.Chirp
	s:= r.URL.Query().Get("author_id")
	if s != ""{
		uuid, err := uuid.Parse(s)
		if err !=nil{
			respondWithError(w,400,"invalid author id")
			return
		}
		singleChirp,err := cfg.queries.GetRecordByID(r.Context(),uuid)	
		if err !=nil{
			if err == sql.ErrNoRows{
				respondWithError(w,400,"no author found")
				return
			}
			respondWithError(w,500,"Internal server error")
			return
		}
		chirps = []database.Chirp{singleChirp}	

	}else{
		chirpsList,err:= cfg.queries.GetAllRecord(r.Context())
		if err !=nil{
			log.Printf("failed to get the record from chrip table %s",err)
			respondWithError(w,500,"Something went wrong")
			return	
		}
		chirps = chirpsList	

	}
	
	for _,chirp:= range chirps{
		items = append(items, 
			Chirp{
				ID: chirp.ID,
				CreatedAt: chirp.CreatedAt,
				UpdatedAt: chirp.UpdatedAt,
				Body: chirp.Body,
				UserID: chirp.UserID,
			})	
	}
	respondWithJSON(w,200,items)

}


func ValidateHandle(w http.ResponseWriter,r *http.Request){
	type parameters struct{
		Body string `json:"body"`
	}

	decoder := json.NewDecoder(r.Body)
	params := parameters{}
	err := decoder.Decode(&params)
	if err !=nil{
		log.Printf("Error decoding the parameters: %s",err)
		respondWithError(w,500,"Something went wrong")	
		return
	}
	if len(params.Body) >140 {
		log.Printf("Length of the body params: %d",len(params.Body))
		respondWithError(w,400,"Chirpy is too long")
		return
	}

	payload := returnVals{
		Body:replaceAsterids(params.Body),
	}

	respondWithJSON(w,200,payload)	

}



func(cfg *apiConfig) UserHandle(w http.ResponseWriter,r *http.Request){
	type parameters struct{
		Email string `json:"email"`
		Password string `json:"password"`
	}	
	decoder := json.NewDecoder(r.Body)
	params := parameters{}
	err := decoder.Decode(&params)
	if err !=nil{
		log.Printf("Error decoding the paramters %s",err)
		respondWithError(w,400,"Invalid json fields")
		return
	}
	hashpassword, err:= auth.HashPassword(params.Password)
	if err !=nil{
		log.Printf("Error hashing the password %s",err)
		respondWithError(w,500,"Something went wrong")
		return
	}
	payload := struct{
		Email string 
		Password string 
	}{
		Email: params.Email,
		Password:hashpassword,
	}
	user , err:=  cfg.queries.CreateUser(r.Context(),database.CreateUserParams(payload))
	if err !=nil{
		log.Printf("Error creating the user:%s",err)
		respondWithError(w,500,"Soemthing went wrong")
		return
	}
	respondUser := User{
		ID: user.ID,
		CreatedAt: user.CreatedAt,
		UpdatedAt: user.UpdatedAt,
		Email: user.Email,
		IsRED: user.IsChirpyRed.Bool,
	}	

	respondWithJSON(w,200,respondUser)	 

}


func (cfg *apiConfig)UserResetHandle(w http.ResponseWriter,r *http.Request){
	if cfg.platform != "dev"{
		respondWithError(w,403,"Forbidden")
		return	
	}
	err:= cfg.queries.DeleteUser(r.Context())
	if err!=nil{
		log.Printf("failed to delete all users %s",err)
		respondWithError(w,500,"Something went wrong")
		return	
	}
	
	respondStruct := struct{
		Msg string `json:"msg"`
	}{
		Msg: "Successfully deleted",
	}
	respondWithJSON(w,200,respondStruct)
	
}


func (cfg *apiConfig)ChirpHandle(w http.ResponseWriter,r *http.Request){
	type paramters struct{
		Body string `json:"body"`
	}
	decoder := json.NewDecoder(r.Body)
	params := paramters{}
	err := decoder.Decode(&params)
	if err !=nil{
		log.Printf("failed to decode the parameters %s",err)
		respondWithError(w,500,"Something went wrong")
		return
	}
	token ,err := auth.GetBearerToken(r.Header)
	if err !=nil{
		respondWithError(w,401,"The user is not authenticated")
		return	
	}
	userID,err:= auth.ValidateJWT(token,cfg.secret)
	if err!=nil{
		respondWithError(w,401,"The user is not authorized")
		return
	}

	payload := struct{
		Body string `json:"body"`
		UserID uuid.UUID `json:"user_id"`
	}{
		Body:params.Body,
		UserID:userID,
	}	
	chirp ,err:= cfg.queries.CreateRecord(r.Context(),database.CreateRecordParams(payload))	
	if err !=nil{
		log.Printf("failed to create the record %s",err)
		respondWithError(w,500,"Something went wrong")
		return
	}	
	respondChirp := Chirp{
		ID: chirp.ID,
		CreatedAt: chirp.CreatedAt,
		UpdatedAt: chirp.UpdatedAt,
		Body:chirp.Body,
		UserID: chirp.UserID,
	}

	respondWithJSON(w,200,respondChirp)

	

}

//refactored and move this guy to auth service
// func (cfg *apiConfig)UserLoginHandle(w http.ResponseWriter,r *http.Request){
// 	type parameters struct{
// 		Email string `json:"email"`
// 		Password string `json:"password"`
// 	}
// 	decoder := json.NewDecoder(r.Body)
// 	params := &parameters{}
// 	err:= decoder.Decode(params)
// 	if err !=nil{
// 		respondWithError(w,400,"Invalid parameters")
// 		return
// 	}
//
// 	userPayload,err := cfg.queries.GetUserInfoByEmail(r.Context(),params.Email)
// 	if err!=nil{
// 		if err == sql.ErrNoRows{
// 			respondWithError(w,404,"Can't find the user")
// 			return
// 		}
// 		log.Printf("failed to get the user from database %s",err)
// 		respondWithError(w,500,"Something went wrong")
// 		return
// 	}
// 	if valid,_:=auth.CheckPassword(params.Password,userPayload.Password); !valid{
// 		respondWithError(w,401,"Invalid ceredentials")
// 		return
// 	}
// 	accessToken,refreshToken,err:= GetAccessTokenAndRefreshToken(r,userPayload.ID,cfg)
// 	if err !=nil{
// 		respondWithError(w,500,"Something went wrong")	
// 		return
// 	}
// 	respondWithJSON(w,200,AuthUser{
// 		User : User{
// 			ID: userPayload.ID,
// 			CreatedAt: userPayload.CreatedAt,
// 			UpdatedAt: userPayload.UpdatedAt,
// 			Email: userPayload.Email,
// 			IsRED: userPayload.IsChirpyRed.Bool,
// 		},
// 		Token: accessToken,
// 		RefreshToken: refreshToken,
// 	})
// }
//


//this func is now belongs to auth service now
// func (cfg *apiConfig) RefreshHandle(w http.ResponseWriter,r *http.Request){
// 	token ,err:= auth.GetBearerToken(r.Header)
// 	if err!=nil{
// 		respondWithError(w,400,"Invalid token")
// 		return
// 	}
// 	response,err:= cfg.queries.GetRefreshToken(r.Context(),token)
// 	if err !=nil{
// 		if err == sql.ErrNoRows{
// 			respondWithError(w,401,"Unauthorized")
// 			return
// 		}
// 		log.Printf("failed to get the refreshToken #%s#",err)
// 		respondWithError(w,500,"Something went wrong")
// 		return
// 	}	
// 	if time.Now().After(response.ExpireAt){
// 		respondWithError(w,401,"Token expired")	
// 		return
// 	}	
// 	err=cfg.queries.RevokeRefreshToken(r.Context(),token)		
// 	if err !=nil{
// 		log.Printf("failed to revoke the refreshToken #%s#",err)
// 		respondWithError(w,500,"Something went wrong")
// 		return
// 	}
// 	accessToken,_,err:= GetAccessTokenAndRefreshToken(r,response.UserID,cfg)
// 	if err !=nil{
// 		respondWithError(w,500,"Something went wrong")
// 		return
// 	}
//
// 		respondStruct := struct{
// 		Token string `json:"Token"`
// 	}{
// 		Token: accessToken,
// 	}
// 	respondWithJSON(w,200,respondStruct)
// }

func GetAccessTokenAndRefreshToken(r *http.Request,userID uuid.UUID,cfg  *apiConfig)(string,string,error){
	expireIn := 60*time.Minute
	accessToken ,err:= auth.MakeJWT(userID,cfg.secret,expireIn)
	if err !=nil{
		log.Printf("failed to make accessToken %s",err)
		return 	"","",err
	}
	refreshToken, err:= auth.MakeRefreshToken()
	if err !=nil{
		log.Printf("failed to make a refreshToken %s",err)
		return "","",err	
	}
	refreshTokenExpireDate := 30*(24*time.Hour)
	_,err= cfg.queries.CreateARefreshToken(r.Context(),database.CreateARefreshTokenParams{
		Token: refreshToken,
		UserID:userID,
		ExpireAt:time.Now().Add(refreshTokenExpireDate) ,
	})
	if err!=nil{
		log.Printf("failed to insert into db #%s#",err)
		return "","",err	
	}

	return accessToken,refreshToken,nil

}

//this business login belongs to the auth service now
//don't like where i am going but anyway
// func (cfg *apiConfig)RevokeHandle(w http.ResponseWriter,r *http.Request){
// 	token, err:= auth.GetBearerToken(r.Header)
// 	if err!=nil{
// 		respondWithError(w,400,"Bad request")
// 		return
// 	}
// 	err= cfg.queries.RevokeRefreshToken(r.Context(),token)
// 	if err !=nil{
// 		if err == sql.ErrNoRows{
// 			respondWithError(w,401,"Unauthorized")
// 			return
// 		}
// 		log.Printf("failed to revoke the refresh tokne #%s#",err)
// 		respondWithError(w,500,"Something went wrong")
// 		return
// 	}
// 	 w.WriteHeader(204)
//
// }

func (cfg *apiConfig) GetChirpWithIDHandle(w http.ResponseWriter,r *http.Request){
	stringIDParam := r.PathValue("chirp_id")
	uuidParam,err:= uuid.Parse(stringIDParam)
	if err !=nil{
		log.Printf("failed to get the id from url %s",err)
		respondWithError(w,400,"Invalid id")
		return	
	}
	chirp ,err := cfg.queries.GetRecordByID(r.Context(),uuidParam)
	if err !=nil{
		if err == sql.ErrNoRows{
			respondWithError(w,404,"Cannot find the matching record")
			return
		}
		log.Printf("failed to get the id from db %s",err)
		respondWithError(w,500,"Soemthing went wrong")	
		return
	}
	respondWithJSON(w,200,chirp)

}

func (cfg *apiConfig)ChirpDeleteHandle(w http.ResponseWriter,r *http.Request){
	stringIDParam := r.PathValue("chirpID")
	uuidChirp,err:= uuid.Parse(stringIDParam)
	if err!=nil{
		respondWithError(w,400,"invalid id")
		return
	}
	accessToken,err:= auth.GetBearerToken(r.Header)
	if err!=nil{
		respondWithError(w,401,"Invalid token")
		return
	}

	userID,err:= auth.ValidateJWT(accessToken,cfg.secret)		
	if err!=nil{
		respondWithError(w,401,"Invalid token")
		return
	}

	chirp ,err:=cfg.queries.GetRecordByID(r.Context(),uuidChirp)
	if err!=nil{
		if err==sql.ErrNoRows{
			respondWithError(w,404,"Cannot find the matching chirp")
			return
		}
		log.Printf("failed to get the record from chirp #%s#",err)
		respondWithError(w,500,"Something went wrong")
		return
	}
	if userID != chirp.UserID{
		respondWithError(w,403,"Forbidden")	
		return
	}
	err=cfg.queries.DeleteRecordByID(r.Context(),uuidChirp)
	if err!=nil{
		log.Printf("failed to delete the chirp #%s#",err)
		respondWithError(w,500,"Something went wrong")
		return	
	}
	w.WriteHeader(204)
}

func (cfg *apiConfig)UserPutHandle(w http.ResponseWriter,r *http.Request){
	type parameters struct{
		Email string `json:"email"`
		Password string `json:"password"`
	}		
	decoder := json.NewDecoder(r.Body)
	params := &parameters{}
	err:= decoder.Decode(params)
	if err!=nil{
		respondWithError(w,400,"Invalid parameters")
		return
	}

	accessToken ,err:= auth.GetBearerToken(r.Header)
	if err !=nil{
		respondWithError(w,401,"Invalid token")
		return
	}
	userID,err:= auth.ValidateJWT(accessToken,cfg.secret)
	if err!=nil{
		log.Printf("failed to validate jwt #%s#",err)
		respondWithError(w,401,"Invalid token")
		return

	}
	userInfo,err:= cfg.queries.GetUserInfoByID(r.Context(),userID)
	if err!=nil{
		if err == sql.ErrNoRows{
			respondWithError(w,404,"User not found")
			return
		}
		respondWithError(w,500,"Something went wrong")
		return
	}
	if userInfo.Email != params.Email{
		respondWithError(w,401,"Unauthorized")
		return
	}
	hashPassword,err:= auth.HashPassword(params.Password)
	if err!=nil{
		log.Printf("hashPassword function doesn't work #%s#",err)
		respondWithError(w,500,"Something went wrong")
		return
	}	
	err=cfg.queries.UpdatePassword(r.Context(),database.UpdatePasswordParams{
		Password: hashPassword,
		ID: userID,
	})
	if err !=nil{
		log.Printf("failed to update the password #%s#",err)
		respondWithError(w,500,"Something went wrong")
		return
	}

	user,err:= cfg.queries.GetUserInfoByID(r.Context(),userID)
	if err!=nil{
		log.Printf("the GetUserInfoByID funciton isn't working #%s#",err)
		respondWithError(w,500,"Something went wrong")
		return
	}
	respondWithJSON(w,200,User{
		ID: user.ID,
		CreatedAt: user.CreatedAt,
		UpdatedAt: user.UpdatedAt,

		Email: user.Email,
		IsRED: user.IsChirpyRed.Bool,
	})
}



func(cfg *apiConfig)WebHookHandle(w http.ResponseWriter,r *http.Request){
	type parameters struct{
		Event string `json:"event"`
		Data struct{
			UserID string `json:"user_id"`
		}`json:"data"`
	}

	decoder := json.NewDecoder(r.Body)
	params := &parameters{}
	err := decoder.Decode(params)
	if err !=nil{
		respondWithError(w,400,"Invalid body")
		return
	}
	if params.Event != "user.upgraded"{
		w.WriteHeader(204)
		return
	}
	userUUID,err := uuid.Parse(params.Data.UserID)
	if err !=nil{
		respondWithError(w,400,"Invalid user id")
		return
	}

	key,err:= auth.GetAPIKEY(r.Header)
	if err !=nil{
		respondWithError(w,401,"Invalid apiKey")
		return
	}
	if key != cfg.polkaKey{
		respondWithError(w,401,"ApiKey doesn't match")
		return
	}

	result,err :=cfg.queries.UpdateIsRedById(r.Context(),userUUID) 
	if err !=nil{
		log.Printf("failed to update the user is_red #%s#",err)
		respondWithError(w,500,"Internal server error")
		return
	}	
	if rows,_:= result.RowsAffected(); rows==0{
		respondWithError(w,404,"Not found user")
		return 	
	}
	w.WriteHeader(204)
	
}		

func (cfg *apiConfig)SwitchProtocol(w http.ResponseWriter,r *http.Request){
	chatID := r.URL.Query().Get("chatID")	
	if chatID == ""{
		respondWithError(w,400,"invalid chatID")
		return
	}
	
}



func main(){

	err:=godotenv.Load("../.env")
	if err !=nil{
		log.Fatal("failed to load the env")
	}
	dURL := os.Getenv("DB_URL")
	platform := os.Getenv("PLATFORM")
	secret := os.Getenv("SECRET")
	polkaKey := os.Getenv("POLKA_KEY")	
	db,err := sql.Open("postgres",dURL)

	if err !=nil{
		log.Fatal("Failed connection to the db ")
		
	}
	dbQueries := database.New(db)
	apicfg := apiConfig{queries: dbQueries,platform: platform,secret:secret,polkaKey: polkaKey}
	mux := http.NewServeMux()
	handlerChain := apicfg.middlewareMeticsInc(http.FileServer(http.Dir("./")))
	finalHanlder := http.StripPrefix("/app/",handlerChain)
	assetChain := apicfg.middlewareMeticsInc(http.FileServer(http.Dir("./assets/")))
	assetHandler := http.StripPrefix("/app/assets/",assetChain)
	
	//init rabbitmq queue
	mq := mq.NewMainMQ(&map[string]chan *mq.Channel{},10)
	//init hub
	hub := chatmodel.NewHub()
	go hub.Run()	
	//Create Repositories
	userRepo := users.NewUserRepo(dbQueries)
	authRepo := authClient.NewAuthRepo(dbQueries)
	chatRepo := chat.NewChatRepo(dbQueries)
	groupRepo := groups.NewGroupRepo(dbQueries)	

	//Create Services
	userService := users.NewUserService(userRepo)
	authService := authClient.NewAuthService(userRepo,authRepo,apicfg.secret)
	chatService := chat.NewChatService(chatRepo,hub,mq)
	groupService := groups.NewGroupService(groupRepo,hub,mq)

	//Create Hanlders
	userHandler := users.NewUserHandler(userService)
	authHandler := authClient.NewAuthHandler(authService)
	chatHandler := chat.NewChatHandler(chatService)
	groupHandler := groups.NewGroupHandler(groupService)

	//startup workers for each event
	// run the message queue
	go mq.Run()
	go mq.ListeningForTheChannels("createGroup",100,groupService.StartWorkerForCreateGroup)


	//Main app route
	mux.Handle("/app/",middleWareLog(finalHanlder))
	//Asset route
	mux.Handle("/app/assets/",middleWareLog(assetHandler))


	//Get Route
	mux.HandleFunc("GET /admin/metrics",apicfg.HitHandle)
	mux.HandleFunc("GET /api/healthz",APIHandle)
	mux.HandleFunc("GET /api/chirps",apicfg.GetChirpHandle)	
	
	//Get route with url params
	mux.HandleFunc("GET /api/chirps/{chirp_id}",apicfg.GetChirpWithIDHandle)

	//POST route 
	mux.HandleFunc("POST /admin/metrics/reset",apicfg.ResetHandle)
	mux.HandleFunc("POST /api/chirps",apicfg.ChirpHandle)
	mux.HandleFunc("POST /api/users",userHandler.Register)
	mux.HandleFunc("POST /admin/reset",apicfg.UserResetHandle)
	mux.HandleFunc("POST /api/login",authHandler.Login)
	mux.HandleFunc("POST /api/refresh",authHandler.RefreshToken)
	mux.HandleFunc("POST /api/revoke",authHandler.Revoke)

	//NOTE FOR webhooks
	/*"Client must request with json of '{
						  "event": "user.upgraded",
						  "data": {
							"user_id": "3311741c-680c-4546-99f3-fc9efac2036c"
						  }
	}'"*/
	mux.HandleFunc("POST /api/polka/webhooks",apicfg.WebHookHandle)

	//PUT route
	mux.HandleFunc("PUT /api/users",apicfg.UserPutHandle)

	//UpdatePassword route
	//uses middleware to parse the userID
	mux.Handle("POST /api/users/password",middleware.AuthMiddleWare(userHandler.UpdatePassword,apicfg.secret))

	//DELETE route
	mux.HandleFunc("DELETE /api/chirps/{chirpID}",apicfg.ChirpDeleteHandle)

	
	//Maybe endpoint for chat
	mux.Handle("GET /api/chats",middleware.AuthMiddleWare(chatHandler.ServeWs,apicfg.secret))	

	//create a group
	mux.Handle("POST /api/chats/groups",middleware.AuthMiddleWare(groupHandler.CreateGroup,apicfg.secret))

	//need to update these dummy function
	//join group
	mux.Handle("POST /api/chats/groups/{group_id}/memebers",middleware.AuthMiddleWare(groupHandler.JoinGroup,apicfg.secret))

	//leave group
	mux.Handle("DELETE /api/chats/groups/{group_id}/members",middleware.AuthMiddleWare(groupHandler.LeaveGroup,apicfg.secret))

	//TODO::still have to write this one
	//kick or add the user from or to the group
	mux.HandleFunc("PATCH /api/chats/groups/{group_id}/members",groupHandler.CreateGroup)
	
	//TODO:and this one
	//change group setting or anything
	mux.HandleFunc("PATCH /api/chats/groups/{group_id}",groupHandler.CreateGroup)
	
	//NOTE:: i should really consider making the server as my own config and graceful shutdown
	server := http.Server{
		Addr: Port,
		Handler: mux,
	}
	log.Printf("The server is running on %q\n",Port)
	log.Fatal(server.ListenAndServe())
	
}
