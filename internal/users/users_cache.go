package users

import (
	"context"
	"errors"
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
	CleanUpUserRs(payload *CacheRsDeleteStruct)
	GetUserFriList(userID uuid.UUID)*[]uuid.UUID
	GetUserRs(userID uuid.UUID)bool
	GetUserReqList(userID uuid.UUID)*map[uuid.UUID]uuid.UUID
	GetUserSendReqList(userID uuid.UUID)*map[uuid.UUID]uuid.UUID
	GetOtherUserIDByReqID(userID,reqID uuid.UUID,lable string)*uuid.UUID
}

type Cache struct{
	UserCache map[uuid.UUID]*UserCache
	UserMuLock sync.Mutex
	UserRsCache map[uuid.UUID]map[string]*map[uuid.UUID]uuid.UUID
	UserRsMuLock sync.Mutex
	UserFriCache map[uuid.UUID]map[string]*[]uuid.UUID
	UserFriMuLock sync.Mutex
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
		UserRsCache: make(map[uuid.UUID]map[string]*map[uuid.UUID]uuid.UUID),
		UserRsMuLock: sync.Mutex{},	
		UserFriCache: make(map[uuid.UUID]map[string]*[]uuid.UUID),
		UserFriMuLock: sync.Mutex{},
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

func (c *Cache)GetUserRs(userID uuid.UUID)bool{
	c.UserRsMuLock.Lock()
	defer c.UserRsMuLock.Unlock()
	if _,ok := c.UserRsCache[userID]; ok{
		return true	
	}
	return false
}

func (c *Cache)GetOtherUserIDByReqID(userID,reqID uuid.UUID,lable string)*uuid.UUID{
	c.UserRsMuLock.Lock()
	defer c.UserRsMuLock.Unlock()
	if _,ok:=c.UserRsCache[userID];ok{
		if v,ok:= c.UserRsCache[userID][lable];ok{
			v := *v
			if newV,ok:= v[reqID];ok{
				return &newV
			}
		}
	}
	return nil

}


func (c *Cache)GetUserReqList(userID uuid.UUID)*map[uuid.UUID]uuid.UUID{
	c.UserRsMuLock.Lock()
	defer c.UserRsMuLock.Unlock()
	if v,ok := c.UserRsCache[userID]["pending"];ok{
		return v	
	}
	return nil
}

func (c *Cache)GetUserFriList(userID uuid.UUID)*[]uuid.UUID{
	c.UserFriMuLock.Lock()
	defer c.UserFriMuLock.Unlock()
	if v,ok := c.UserFriCache[userID]["friend"];ok{
		return v	
	}
	return nil
}

func (c *Cache)GetUserSendReqList(userID uuid.UUID)*map[uuid.UUID]uuid.UUID{
	c.UserRsMuLock.Lock()
	defer c.UserRsMuLock.Unlock()
	if v,ok := c.UserRsCache[userID]["send"];ok{
		log.Print("returning the address of send")
		return v
	}
	log.Print("returning nil address")
	return nil
}

func (c *Cache)Load(){
	ctx, cancel := context.WithTimeout(context.Background(),1*time.Second)
	defer cancel()
	userList, err :=c.UserRepo.GetAllUsers(ctx)	
	if err !=nil{
		log.Printf("failed to fetch the user data from db \n#%s#",err)
	}
	//this one is for the already confirmed friends rs
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
						c.UpdateUserRs(CacheUpdateStruct{
							UserID: user.ID,
							ReqID: req.ID,
							OtherUserID: req.UserID,
							Lable: "pending",
						})

					}
					
				//update the cache for current user with send label
					for _,req:=range *sendList{
						c.UpdateUserRs(CacheUpdateStruct{
							UserID: user.ID,
							ReqID: req.ID,
							OtherUserID: req.OtheruserID,
							Lable: "send",
						})
					}
				
			}
		}()
	

		go func(){

			//this one update the only friend label
			for _,userRs:= range *userRsList{
				//this update the first user
				c.UpdateUserRs(CacheUpdateFriStruct{
					UserID: userRs.UserID,
					ToID: userRs.OtheruserID,
					Lable:userRs.Label,
				})
				//this update the other user
				c.UpdateUserRs(CacheUpdateFriStruct{
					UserID: userRs.OtheruserID,
					ToID: userRs.UserID,
					Lable: userRs.Label,
				})
			}
		}()
	log.Printf("Successfully loaded the user and its relations cache ")
	for k,v:= range c.UserCache{
		log.Printf("%v : %v",k,v)	
	}
	for k,v:= range c.UserRsCache{
		log.Printf("%v : %v",k,v)	
	}
}




//NOTE::the label from the payload and from db is not the same one
//label here represents 'status'
//above function represents 'friend'(label from db)
//have to fix this one cuz if one user's cache is updated then the other one has to update too
func (c *Cache)UpdateUserRs(payload interface{}){
	switch payload:= payload.(type){
	case CacheUpdateStruct:	
			c.updateRsCache(payload.UserID,payload.OtherUserID,payload.ReqID,payload.Lable)
	case CacheUpdateFriStruct:
				c.updateFriCache(payload.UserID,payload.ToID,payload.Lable)			
	default:
		log.Printf("not a valid struct you are passing")
	}	
}
func (c *Cache)updateRsCache(userID,otherID,reqID uuid.UUID,label string){
	c.UserRsMuLock.Lock()
	defer c.UserRsMuLock.Unlock()
	if _,ok:= c.UserRsCache[userID];!ok{
		c.UserRsCache[userID] = make(map[string]*map[uuid.UUID]uuid.UUID)
	}
	c.UserRsCache[userID][label]= &map[uuid.UUID]uuid.UUID{
		reqID:otherID,
	}
}
func (c *Cache)updateFriCache(userID,otherID uuid.UUID,lable string){
			c.UserFriMuLock.Lock()	
			defer c.UserFriMuLock.Unlock()	
			if _,ok:= c.UserFriCache[userID]; !ok{
				c.UserFriCache[userID] = make(map[string]*[]uuid.UUID)		
				}		
			if _,ok:= c.UserFriCache[userID][lable];!ok{
				c.UserFriCache[userID][lable]= &[]uuid.UUID{}
			} 	
			*c.UserFriCache[userID][lable] = append(*c.UserFriCache[userID][lable],otherID)
}
//this one will clean up what ever the lable got passed 
func (c *Cache)CleanUpUserRs(payload *CacheRsDeleteStruct){
	//need to delete all the cache except the friend one
	c.UserRsMuLock.Lock()
	defer c.UserRsMuLock.Unlock()
	if _,ok:= c.UserRsCache[payload.UserID];ok{
		if v,ok:= c.UserRsCache[payload.UserID][payload.Lable];ok{
			v := *v 
			if _,ok:=v[payload.ReqID];ok{
				delete(v,payload.ReqID)	
			}
		}	
	}else{
		log.Print("cannot find the user in the userRscache map ")
	}
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

