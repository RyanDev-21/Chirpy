package chat

import (
	"context"
	"log"
	"os"
	"reflect"
	"testing"

	"RyanDev-21.com/Chirpy/internal/database"
	"RyanDev-21.com/Chirpy/internal/users"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

func TestAddMessage(t *testing.T){
	ctx := context.Background()	
	_=godotenv.Load("../../.env")
	dURL := os.Getenv("DB_URL")
	log.Printf("durl: %s",dURL)
	db ,_ := pgxpool.New(ctx,dURL)
	queries := database.New(db)	
	chatRepo := NewChatRepo(queries)
	userRepo := users.NewUserRepo(queries)

	//
	// res1,err := userRepo.Create(ctx,users.CreateUserInput{
	// 	Email: "htetlinzaw2004221@gmail.com",
	// 	Password: "hello123",
	// })
	// if err !=nil{
	// 	t.Fatalf("failed to create user 1\n #%s#",err)
	// }
	// toID := res1.ID
	//
	// res2,err := userRepo.Create(ctx,users.CreateUserInput{
	// 	Email: "htetlinzaw2005221@gmail.com",
	// 	Password: "hello123",
	// })
	// if err !=nil{
	// 	t.Fatalf("failed to craete user 2\n #%s#",err)
	// }
	// fromID:= res2.ID
	res1,_,err:=userRepo.GetUserByEmail(ctx,"htetlinzaw2004221@gmail.com")
	if err !=nil{
		t.Fatalf("failed to get user 1")
	}	
	toID := res1.ID
	res2,_,err:=userRepo.GetUserByEmail(ctx,"htetlinzaw2005221@gmail.com")
	if err !=nil{
		t.Fatalf("failed to get user 2")
	}	
	fromID := res2.ID
	var messageID uuid.UUID
	t.Run("Testing add message",func(t *testing.T) {
		to_id := *GetUUIDType(toID)
		from_id := *GetUUIDType(fromID)
		payload := &database.AddMessageParams{
			Content: *GetStringType("Hello"),
			ToID: to_id,
			FromID: from_id,
		}
		result := &database.Message{ Content: *GetStringType("Hello"), ToID:to_id , FromID: from_id}	
		//have to mock user to exist
		res,err:=chatRepo.AddMessage(payload)	
		if err !=nil{
			t.Fatalf("failed to get the res #%s#",err)
		}
		if reflect.DeepEqual(*res,*result){
			t.Fatalf("the content doens't match\n expect:%v \n get :%v",*result,*res)
		}
		messageID = res.ID


	})	
	t.Run("Testing with parent id",func(t *testing.T) {
		to_id := *GetUUIDType(toID)
		from_id := *GetUUIDType(fromID)
		parent_id := *GetUUIDType(messageID)
		payload := &database.AddMessageParams{
			Content: *GetStringType("Hello"),
			Parentid: parent_id,
			ToID: to_id,
			FromID: from_id,
		}
		result := &database.Message{ Content: *GetStringType("Hello"), Parentid:parent_id,ToID:to_id , FromID: from_id}	
		//have to mock user to exist
		res,err:=chatRepo.AddMessage(payload)	
		if err !=nil{
			t.Fatalf("failed to get the res #%s#",err)
		}
		if reflect.DeepEqual(*res,*result){
			log.Printf("ParentId of message 1 :%v\n ParentId of message 2:%v",res.Parentid,result.Parentid)
			t.Fatalf("the content doens't match\n expect:%v \n get :%v",*result,*res)
		}
	})
}
