package users

import (
	"context"
	"errors"
	//"fmt"
	"log"
	"slices"
	"sync"
	"time"

	"RyanDev-21.com/Chirpy/internal/database"
	"github.com/google/uuid"
)

type UserCacheItf interface{
	Load()
	UpdateUserRs(payload interface{})
	CleanUpUserRs(payload *CacheUpdateStruct)
	GetUserRs(userID uuid.UUID)*map[string]*[]uuid.UUID
}

//i could do like the primary key of the rscache to be the req row id so that when it comes to like update the cache i can just use that id and then do the logic
type Cache struct{
	UserCache map[uuid.UUID]*UserCache
	UserMuLock sync.Mutex
	//for now using the userId as the primary id
	//need to replace this guy with friend_request id or smth
	UserRsCache map[uuid.UUID]map[string]*[]uuid.UUID
	UserRsMuLock sync.Mutex
	UserRepo UserRepo
}


type UserCache struct{
	Info *User			
	IsActive bool 
}

func NewUserCache(userRepo UserRepo)UserCacheItf{
	return &Cache{
		UserCache: make(map[uuid.UUID]*UserCache),
		UserMuLock: sync.Mutex{},
		UserRepo: userRepo,
		UserRsCache: make(map[uuid.UUID]map[string]*[]uuid.UUID),
		UserRsMuLock: sync.Mutex{},	
	}
}


func formatToUser(user *database.User)*User{
	return &User{
		ID: user.ID,
		Name: user.Name,
		Email: user.Email,
		CreatedAt: user.CreatedAt,
		UpdatedAt: user.UpdatedAt,
		IsRED: user.IsChirpyRed.Bool,	
	}
}

func (c *Cache)GetUserRs(userID uuid.UUID)*map[string]*[]uuid.UUID{
	c.UserRsMuLock.Lock()
	defer c.UserRsMuLock.Unlock()
	v:= c.UserRsCache[userID]
	return &v
}


func (c *Cache)Load(){
	ctx, cancel := context.WithTimeout(context.Background(),1*time.Second)
	defer cancel()
	userList, err :=c.UserRepo.GetAllUsers(ctx)	
	if err !=nil{
		log.Printf("failed to fetch the user data from db \n#%s#",err)
	}
	userRsList, err:= c.UserRepo.GetAllUsersRs(ctx)
	if err !=nil{
		log.Printf("failed to fetch the userRs from db \n #%s#",err)
	}

	
	go func(){
		for _,user:= range *userList{
			c.UserMuLock.Lock()
			c.UserCache[user.ID]=&UserCache{
				Info:formatToUser(&user),
				IsActive: false,
			} 
			c.UserMuLock.Unlock()
		
			context,cancel:= context.WithTimeout(context.Background(),10*time.Second)
			defer cancel()
			//fetcht the req list of current user
			list,err:= c.UserRepo.GetMyFriReqList(context,user.ID)
			if err !=nil{
				if err == NoRecordFoundErr{
					log.Printf("no friend request list found for user(%v)",user.ID)
					continue
				}
				log.Printf("failed to get the fri req list for user(%v)",user.ID)
				continue
				
			}
			
			sendList, err:= c.UserRepo.GetMySendFirReqList(context,user.ID)
			if err !=nil{
				if err == NoRecordFoundErr{
					log.Printf("no send record found for user(%v)",user.ID)
					continue
				}	
				log.Printf("failed to get the send req list from user(%v)",user.ID)
				continue
			}
			// update the cache for current user with pending label
			for _,req := range *list{
				//this one fetches the pending data 
				c.UpdateUserRs(&CacheUpdateStruct{
					UserID: user.ID,
					ReqID: req.ID,
					Lable: "pending",
				})

			}
			//update the cache for current user with send label
			for _,req:=range *sendList{
				c.UpdateUserRs(&CacheUpdateStruct{
					UserID: user.ID,
					ReqID: req.ID,
					Lable: "send",
				})
			}
		}
	}()
	go func(){
		//this one update the only friend label
		for _,userRs:= range *userRsList{
			c.UpdateUserRs(&CacheUpdateFriStruct{
				UserID: userRs.UserID,
				ToID: userRs.OtheruserID,
				Lable:userRs.Label,
			})
		}
	}()
	log.Printf("Successfully loaded the user and its relations cache \n#%v#\n#%v#",c.UserRsCache,c.UserCache)
}




//NOTE::the label from the payload and from db is not the same one
//label here represents 'status'
//above function represents 'friend'(label from db)
//have to fix this one cuz if one user's cache is updated then the other one has to update too
func (c *Cache)UpdateUserRs(payload interface{}){
	switch payload:= payload.(type){
	case CacheUpdateStruct:	
			c.updateFriCache(payload.UserID,payload.ReqID,payload.Lable)
	case CacheUpdateFriStruct:
				c.updateFriCache(payload.UserID,payload.ToID,payload.Lable)			
	default:
		log.Printf("not a valid struct you are passing")
	}	
}
func (c *Cache)updateFriCache(userID,otherID uuid.UUID,lable string){
			c.UserRsMuLock.Lock()	
			if _,ok:= c.UserRsCache[userID]; !ok{
				c.UserRsCache[userID] = make(map[string]*[]uuid.UUID)		
			}		
			if _,ok:= c.UserRsCache[userID][lable];!ok{
				c.UserRsCache[userID][lable]= &[]uuid.UUID{}
			} 	
			*c.UserRsCache[userID][lable] = append(*c.UserRsCache[userID][lable],otherID)
	
			c.UserRsMuLock.Unlock()	
}
func (c *Cache)CleanUpUserRs(payload *CacheUpdateStruct){
	//need to delete all the cache except the friend one
	go func(fromID,toID uuid.UUID){
		c.UserRsMuLock.Lock()	
		if _,ok:= c.UserRsCache[fromID]; ok{
			if v,ok:= c.UserRsCache[fromID]["pending"];ok{
				if index:=slices.Index(*v,toID);index!=-1{
					updatedList,err:= removeEleFromSlice(c.UserRsCache[fromID]["pending"],toID)				
					if err !=nil{
						log.Fatal("failed to remove ele from slice")
					}
					c.UserRsCache[fromID]["pending"]= updatedList	
				}

				}	
			if v,ok:= c.UserRsCache[fromID]["send"];ok{
				if index:=slices.Index(*v,toID);index!=-1{
				updatedList,err:= removeEleFromSlice(c.UserRsCache[fromID]["send"],toID)				
				if err !=nil{
					log.Fatal("failed to remove ele from slice")
				}
				c.UserRsCache[fromID]["send"]= updatedList	
			}
		}

		}else{
			log.Printf("cannot find the userID#%v#",fromID)
		}
		c.UserRsMuLock.Unlock()
		
	}(payload.UserID,payload.ReqID)
}

func removeEleFromSlice(slice *[]uuid.UUID,ele uuid.UUID)(*[]uuid.UUID,error){
	orgList := *slice
	index := slices.Index(orgList,ele)
	if index == -1{
		return nil,errors.New("failed to get the index")	
	}
	var newSlice []uuid.UUID
	orgList[index] = orgList[len(orgList)-1]	
	newSlice = orgList[:len(orgList)-1]
	return &newSlice,nil
}

