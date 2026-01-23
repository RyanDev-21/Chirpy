package users

import (
	"context"
	"log"
	"os"
	"testing"
	"time"

	mq "RyanDev-21.com/Chirpy/internal/customMq"
	"RyanDev-21.com/Chirpy/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

func TestUserAddFriend(t *testing.T){
	err:=godotenv.Load("../../.env")
	if err !=nil{
		log.Fatal("failed to load the env")
	}
	ctx := context.Background()
	dURL := os.Getenv("DB_URL")
	// platform := os.Getenv("PLATFORM")
	// secret := os.Getenv("SECRET")
	// polkaKey := os.Getenv("POLKA_KEY")	
	db ,err := pgxpool.New(ctx,dURL)
	if err !=nil{
		log.Fatal("Failed connection to the db ")
		
	}
	dbQueries := database.New(db)
	defer db.Close()
	userRepo :=	NewUserRepo(dbQueries)
	userCache := NewUserCache(userRepo)		
	mq := &mq.MainMQ{} 
	userService := NewUserService(userRepo,userCache,mq)
		t.Run("Testing for add friend",func(t *testing.T) {
		userID := uuid.New();
		otherUserID := uuid.New();
		reqID := uuid.New();
		ctx,cancel := context.WithTimeout(context.Background(),1*time.Second)
		defer cancel()
		userService.AddFriendSend(ctx,userID,otherUserID,"pending",reqID)	
		
		rsMapUser1 :=*userCache.GetUserRs(userID)
		rsMapUser2 :=*userCache.GetUserRs(otherUserID)
		if _,ok:=rsMapUser1["send"]; !ok{
			t.Fatal("cannot access the user 1 map")
		}
		if _,ok:=rsMapUser2["pending"]; !ok{
			t.Fatal("cannot access the user 2 map")
		}
		v1 := *rsMapUser1["send"]
		v2 := *rsMapUser2["pending"]

		if rsMapUser1 !=nil && rsMapUser2 !=nil{
			if v1[0] != v2[0]{
				t.Fatalf("failed to get the same value: %v, %v",v1[0],v2[0])	
			}
		}else{

			t.Fatalf("failed to get the map value: %v , %v",rsMapUser1,rsMapUser2)
		}


	})

	t.Run("Testing for confirm friend",func(t *testing.T) {
		userID := uuid.New();
		otherUserID := uuid.New();
		reqID := uuid.New();
		ctx,cancel := context.WithTimeout(context.Background(),1*time.Second)
		defer cancel()
		userService.AddFriendSend(ctx,userID,otherUserID,"pending",reqID)	
		
		rsMapUser1 :=*userCache.GetUserRs(userID)
		rsMapUser2 :=*userCache.GetUserRs(otherUserID)
		if _,ok:=rsMapUser1["send"]; !ok{
			t.Fatal("cannot access the user 1 map")
		}
		if _,ok:=rsMapUser2["pending"]; !ok{
			t.Fatal("cannot access the user 2 map")
		}
		v1 := *rsMapUser1["send"]
		v2 := *rsMapUser2["pending"]

		if rsMapUser1 !=nil && rsMapUser2 !=nil{
			if v1[0] != v2[0]{
				t.Fatalf("failed to get the same value: %v, %v",v1[0],v2[0])	
			}
		}else{

			t.Fatalf("failed to get the map value: %v , %v",rsMapUser1,rsMapUser2)
		}

		context,cancel := context.WithTimeout(context.Background(),1*time.Second)
		defer cancel()
		userService.ConfirmFriendReq(context,userID,otherUserID,reqID,"confrim");
		
		v1 = *rsMapUser1["friend"]
		v2 = *rsMapUser2["friend"]
		if rsMapUser1 !=nil && rsMapUser2 !=nil{
			if v1[0] ==otherUserID || v2[0]!=userID {
				t.Fatalf("failed to get the same value:[%v :%v],[%v:%v]",userID,v1[0],otherUserID,v2[0])	
			}
		}else{
			t.Fatalf("failed to get the map value: %v , %v",rsMapUser1,rsMapUser2)
		}})
}

