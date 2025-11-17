package auth

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
)


func TestHashPassword(t *testing.T){
	
	password := "htetlinzaw2004"
	got,err := HashPassword(password)	
	if err !=nil{
		t.Fatal(err)
	}
	t.Run("testing with correct password",func(t *testing.T) {

	valid ,_:= CheckPassword(password,got)

	if !valid{
		fmt.Printf("got %s  ",got)
		t.Fatal()
	}
	})

	t.Run("testing with incorrect password",func(t *testing.T) {
		password = "zaw2004"
		valid,_:= CheckPassword(password,got)
		if valid{
			fmt.Printf("has to return false got %v",valid)
			t.Fatal()
		}
	})

}

//Need to refactor the token generate thing too many resuable code
func TestJWTauth(t *testing.T){
	secretKey := "verygoodpractice"
	userID,_ := uuid.NewUUID()

	expireIn := 100*time.Second
	t.Run("Testing with valid uuid and valid time duration",func(t *testing.T) {

		token, err:= MakeJWT(userID,secretKey,expireIn)		
		if err !=nil{
			t.Fatalf("failed to create the jwt token %s",err)
		}
		got,err := ValidateJWT(token,secretKey)
		if err!=nil{
			t.Fatalf("failed to validate the jwt token %s",err)

		}
		if got!= userID{
			log.Fatalf("got %v want %v",got,userID)
		}

	})

	t.Run("Testing with invalid time duration",func(t *testing.T) {
		expireIn = 1*time.Second
		token, err:= MakeJWT(userID,secretKey,expireIn)		
		log.Printf("token %s",token)
		if err !=nil{
			t.Fatalf("failed to create the jwt token %s",err)
		}
		log.Printf("token is going to be epxired in %v",expireIn)
		time.Sleep(2*expireIn)
		userID,err = ValidateJWT(token,secretKey)
		if err==nil{
			t.Fatalf("failed to get  the jwt token error ")

		}

		wantErr := errors.New("expired token")		
		if wantErr.Error() != err.Error(){
			t.Fatalf("want error of %v,got error of %v",wantErr,err)
			
		}
	})

	t.Run("Testing with the wrong secretKey",func(t *testing.T) {
		token,err:=MakeJWT(userID,secretKey,expireIn)
		if err !=nil{
			t.Fatalf("failed to create the jwt token %s",err)
		}
		secretKey = "wrongone"
		userID, err = ValidateJWT(token,secretKey)
		if err ==nil{
			t.Fatalf("failed to get the 'Invalid token error'")
		}
		wantErr:= errors.New("invalid token")
		if wantErr.Error()!= err.Error(){
			t.Fatalf("want error of %v,got error of %v",wantErr,err)
		}
		})
	
}


func TestGetToken(t *testing.T){
	userID,_ := uuid.NewUUID()
	expireIn := 30*time.Minute
	secretKey := "something"	
	token,_:=MakeJWT(userID,secretKey,expireIn)
	headers := http.Header{}
	headers.Set("Authorization",fmt.Sprintf("Bearer %v",token))
	
    _,err:=	GetBearerToken(headers)
	if err !=nil{
		t.Fatalf("failed to get the errro %s",err)
	}
	
}

