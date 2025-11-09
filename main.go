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

	"RyanDev-21.com/Chirpy/internal/database"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)


const (
	Port = ":8080"
)

type apiConfig struct{
	fileServerHits atomic.Int32	
	queries *database.Queries
	platform string	
}

type User struct{
	ID uuid.UUID  `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Email string `json:"email"`
}

type Chirp struct{
	ID uuid.UUID `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Body string `json:"body"`
	UserID uuid.UUID `json:"user_id"`

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
	}	
	decoder := json.NewDecoder(r.Body)
	params := parameters{}
	err := decoder.Decode(&params)
	if err !=nil{
		log.Printf("Error decoding the paramters %s",err)
		respondWithError(w,500,"Something went wrong")
		return
	}

	user , err:=  cfg.queries.CreateUser(r.Context(),params.Email)
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
		UserID string `json:"user_id"`
	}
	decoder := json.NewDecoder(r.Body)
	params := paramters{}
	err := decoder.Decode(&params)
	if err !=nil{
		log.Printf("failed to decode the parameters %s",err)
		respondWithError(w,500,"Something went wrong")
	}
	userID, err:= uuid.Parse(params.UserID)
	if err !=nil{
		log.Printf("failed to parse the uuid from string input %s",err)
		respondWithError(w,400,"Invalid uuid string")
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


func main(){

	err:=godotenv.Load()
	if err !=nil{
		log.Fatal("failed to load the env")
	}
	dURL := os.Getenv("DB_URL")
	platform := os.Getenv("PLATFORM")

	db,err := sql.Open("postgres",dURL)

	if err !=nil{
		log.Fatal("Failed connection to the db ")
		
	}
	dbQueries := database.New(db)
	apicfg := apiConfig{queries: dbQueries,platform: platform}
	mux := http.NewServeMux()
	handlerChain := apicfg.middlewareMeticsInc(http.FileServer(http.Dir("./")))
	finalHanlder := http.StripPrefix("/app/",handlerChain)
	mux.Handle("/app/",middleWareLog(finalHanlder))
	assetChain := apicfg.middlewareMeticsInc(http.FileServer(http.Dir("./assets/")))
	assetHandler := http.StripPrefix("/app/assets/",assetChain)
	mux.Handle("/app/assets/",middleWareLog(assetHandler))
	mux.HandleFunc("GET /admin/metrics",apicfg.HitHandle)
	mux.HandleFunc("GET /api/healthz",APIHandle)
	mux.HandleFunc("POST /admin/metrics/reset",apicfg.ResetHandle)
	mux.HandleFunc("POST /api/chirps",apicfg.ChirpHandle)
	mux.HandleFunc("POST /api/users",apicfg.UserHandle)
	mux.HandleFunc("POST /admin/reset",apicfg.UserResetHandle)

	server := http.Server{
		Addr: Port,
		Handler: mux,
	}
	log.Printf("The server is running on %q\n",Port)
	log.Fatal(server.ListenAndServe())
	
}
