package groups

import (
	"context"
	"database/sql"
	"log"
	"sync"
	"time"

	"RyanDev-21.com/Chirpy/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/lib/pq"
)

type CacheGroupInfo struct{
	Name string
	TotalMem int16
	MaxMem int16
}
type Cache struct{

	//in case this need concurrent read and write
	GroupCache map[uuid.UUID]*CacheGroupInfo
	groupMuLock sync.Mutex
	MemberCache map[uuid.UUID]*[]uuid.UUID
	memMuLock sync.Mutex
	groupRepo GroupRepo
}
func FormatDbModel(dbModel *database.ChatGroup)*CacheGroupInfo{
	return &CacheGroupInfo{
		TotalMem: dbModel.CurrentMember,
		Name:dbModel.Name,
		MaxMem: dbModel.MaxMember,
	}
}


func NewGroupCache(groupRepo GroupRepo)*Cache{
	return &Cache{
		GroupCache: make(map[uuid.UUID]*CacheGroupInfo,1000),
		MemberCache: make(map[uuid.UUID]*[]uuid.UUID,1000),
		groupRepo: groupRepo,
	}
}


//this will get all rows from the group and store it in the map NOTE:: need to fix the currentMember(total) value 
//NOTE:: pls remember to set up the load cache
func (cache *Cache)Load()error{
	ctx,cancel:=context.WithTimeout(context.Background(),10*time.Second)
	defer cancel()
	groupInfo, err :=cache.groupRepo.getAllGroupInfo(ctx)
	log.Printf("group info #%v#",groupInfo)
	if err !=nil{
		return err
	}

	for _,v:= range *groupInfo{
		go func(id uuid.UUID){
			groupContext,cancel := context.WithTimeout(context.Background(),10*time.Second)
			defer cancel()
			groupMems, err := cache.groupRepo.getMemsByID(groupContext,id)
			if err !=nil{
				log.Printf("failed to get the group member ids #%s#",err)
				return
			}
			cache.memMuLock.Lock()
			cache.MemberCache[v.ID] = groupMems
			cache.memMuLock.Unlock()

		}(v.ID)
	}
	for _,v := range *groupInfo{
		cache.GroupCache[v.ID]= FormatDbModel(&database.ChatGroup{
			CurrentMember: v.CurrentMember,
			MaxMember: v.MaxMember,
			Name: v.Name,
		})
		

	}	
	return nil
}


//this will give the group chat metadata needed for the cache
func (cache *Cache)GetFromGroup(groupID uuid.UUID)(*CacheGroupInfo,error){
	var info *CacheGroupInfo
	if v,ok := cache.GroupCache[groupID]; !ok{
		context , cancel := context.WithTimeout(context.Background(),1*time.Second)
		defer cancel()
		groupInfo,err := cache.groupRepo.getGroupInfoByID(context,groupID)	
		if err !=nil{
			if err == sql.ErrNoRows{
				return nil,ErrNotFoundGroup
			}
			return nil,err
		}
		formattedInfo := FormatDbModel(groupInfo)
		cache.GroupCache[groupInfo.ID] = formattedInfo
		return formattedInfo,nil
	}else{
		info = v
	}
	return info,nil
}

// func (cache *Cache)GetMemListFromGroup(groupID uuid.UUID)*[]uuid.UUID{
// 	if v,ok:=cache.MemberCache[groupID]	
// }


//this will give the memberids list in the group
func (cache *Cache)GetFromMember(groupID uuid.UUID)(*[]uuid.UUID,error){
	var info *[]uuid.UUID
	cache.memMuLock.Lock()
	defer cache.memMuLock.Unlock()
	if v,ok := cache.MemberCache[groupID]; !ok{//cache miss
		context , cancel := context.WithTimeout(context.Background(),1*time.Second)
		defer cancel()
		groupMems,err := cache.groupRepo.getMemsByID(context,groupID)
		if err !=nil{
			if err == sql.ErrNoRows{
				return nil,ErrNotFoundGroup
			}
			return nil,err
		}
		//udpate the cache with the list from db
		*cache.MemberCache[groupID] = *groupMems 
		return groupMems,nil
	}else{
		info = v //cache hit
	}
	return info,nil
}
//decided not to do this and let the db handle it (eg.on conflict do nothing)
// func (cache *Cache)CheckMemberFromGroup(groupID,memID uuid.UUID)(bool,error){
// 	cache.memMuLock.Lock()
// 	defer cache.memMuLock.Unlock()
//
// }

func (cache *Cache)CheckGroupNameFromCache(name string)(bool,error){
	for _,v := range cache.GroupCache{
		//if we have the same name then we return true 
		if v.Name == name{
			return true, nil	
		}
	}
	
	context,cancel := context.WithTimeout(context.Background(),1*time.Second)
	defer cancel()
	//check for db when can't find in the cache
	err := cache.groupRepo.getGroupInfoByName(context,name)
	if err !=nil{

		log.Printf("error #%s#",err)	
		//check for duplicate error
		if pgErr,ok:=err.(*pq.Error);ok{
			if pgErr.Code == "23505"{
				return true,nil
			}
		}
		//check for no result found
		if err == pgx.ErrNoRows{
			return false,nil
		}
		return false,err
	}	

	return false,nil
}

