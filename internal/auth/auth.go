package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/alexedwards/argon2id"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)
//For extracting apiKey from authorization
func GetAPIKEY(headers http.Header)(string,error){
	headerString := headers.Get("Authorization")
	if headerString == ""{
		return "",errors.New("invalid apiKey")
	}
	key := strings.TrimPrefix(headerString,"ApiKey ")
	
	return key,nil
}



//For hashing and checking
func HashPassword(password string)(string,error){
	hash,err:=argon2id.CreateHash(password,argon2id.DefaultParams)
	if err !=nil{
		return "",err
	}
	return hash,nil
}
func CheckPassword(password string,hash string)(bool,error){
	valid,err:=	argon2id.ComparePasswordAndHash(password,hash)
	if err!=nil{
		return valid,err
	}
	return valid,nil
}

//For making jwt
func MakeJWT(userID uuid.UUID,tokenSecret string,expiresIn time.Duration)(string,error){
	signedKey := []byte(tokenSecret)
	claims := jwt.RegisteredClaims{
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(expiresIn)),
		Issuer: "chirpy",
		IssuedAt: jwt.NewNumericDate(time.Now()),
		Subject:fmt.Sprintf("%v",userID) ,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256,claims)
	accessToken,err := token.SignedString(signedKey)
	if err !=nil{
		return accessToken,err
	}
	return accessToken,nil
}
func ValidateJWT(tokenString,tokenSecret string) (uuid.UUID,error){
	registerdClaims := &jwt.RegisteredClaims{}
	token,err := jwt.ParseWithClaims(tokenString,registerdClaims,func(t *jwt.Token) (any, error) {
	
		return []byte(tokenSecret),nil
	})
	if err!=nil{
		if errors.Is(err,jwt.ErrTokenExpired){
			log.Printf("expired token %s",err)
			return uuid.Nil,errors.New("expired token")
		}
		log.Printf("failed to parse the token %s",err)
		return uuid.Nil,errors.New("invalid token")
	}
	if !token.Valid {
		log.Print("failed to match the token claim type ")
		return uuid.Nil,errors.New("invalid token claim type")
	}
	userIDstring,err:= token.Claims.GetSubject()
	if err !=nil{
		return uuid.Nil,errors.New("jwt subject field is mssing")
	}
 
	userID ,err:=uuid.Parse(userIDstring)
	if err !=nil{
		return uuid.Nil,err
	}
	return userID,nil

}


//Get Token From the header
func GetBearerToken(headers http.Header)(string,error){

   tokenHeader :=headers.Get("Authorization")	
	if tokenHeader == ""{
	
		log.Printf("token header not set")
		return "",errors.New("invalid token header")
	}
	token := strings.TrimSpace(strings.TrimPrefix(tokenHeader,"Bearer"))
	return token,nil
}

//For refreshToken
func MakeRefreshToken()(string,error){
	key := make([]byte,32)
	_,err := rand.Read(key)
	if err !=nil{
		return "",errors.New("failed to generate the random data")
	}
	hexString := hex.EncodeToString(key)
	return hexString,nil
	
}
