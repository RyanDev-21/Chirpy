package response

import (
	"encoding/json"
	"net/http"
	"log"
)



func JSON(w http.ResponseWriter,code int,payload any){
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

//respond with error is basically jsut reponsding with differnt code and 
//same format so we use the JSON again
func Error(w http.ResponseWriter,code int,msg string){
	JSON(w,code,struct{Error string `json:"error"`}{Error: msg})
}
