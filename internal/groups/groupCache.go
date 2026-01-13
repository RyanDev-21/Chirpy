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
	name string
	total_mem int16
	max_mem int16
}


type GroupCache struct{

	//in case this need concurrent read and write
	GroupCache map[uuid.UUID]*CacheGroupInfo
	groupMuLock sync.Mutex
	MemberCache map[uuid.UUID]*[]uuid.UUID
	memMuLock sync.Mutex
	groupRepo GroupRepo
}

func FormatDbModel(dbModel *database.ChatGroup)*CacheGroupInfo{
	return &CacheGroupInfo{
		total_mem: dbModel.CurrentMember,
		name:dbModel.Name,
		max_mem: dbModel.MaxMember,
	}
}


func NewGroupCache(groupRepo GroupRepo)*GroupCache{
	return &GroupCache{
		GroupCache: make(map[uuid.UUID]*CacheGroupInfo,1000),
		MemberCache: make(map[uuid.UUID]*[]uuid.UUID,1000),
		groupRepo: groupRepo,
	}
}


//this will get all rows from the group and store it in the map NOTE:: need to fix the currentMember(total) value 
//NOTE:: pls remember to set up the load cache
func (cache *GroupCache)Load()error{
	ctx,cancel:=context.WithTimeout(context.Background(),10*time.Second)
	defer cancel()
	groupInfo, err :=cache.groupRepo.getAllGroupInfo(ctx)
	log.Printf("group info #%v#",groupInfo)
	if err !=nil{
		return err
	}

	for _,v:= range *groupInfo{
		groupContext := context.Background()
		go func(id uuid.UUID){
			groupMems, err := cache.groupRepo.getMemsByID(groupContext,id)
			if err !=nil{
				log.Printf("failed to get the user member ids #%s#",err)
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
func (cache *GroupCache)GetFromGroup(groupID uuid.UUID)(*CacheGroupInfo,error){
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


//this will give the memberids list in the group
func (cache *GroupCache)GetFromMember(groupID uuid.UUID)(*[]uuid.UUID,error){
	var info *[]uuid.UUID
	if v,ok := cache.MemberCache[groupID]; !ok{
		context , cancel := context.WithTimeout(context.Background(),1*time.Second)
		defer cancel()
		groupMems,err := cache.groupRepo.getMemsByID(context,groupID)
		if err !=nil{
			if err == sql.ErrNoRows{
				return nil,ErrNotFoundGroup
			}
			return nil,err
		}
		return groupMems,nil
	}else{
		info = v
	}
	return info,nil
}

func (cache *GroupCache)CheckGroupNameFromCache(name string)(bool,error){
	for _,v := range cache.GroupCache{
		log.Printf("name :%v",name)
		if v.name == name{
			return true, nil	
		}
	}	
	context,cancel := context.WithTimeout(context.Background(),1*time.Second)
	defer cancel()
	err := cache.groupRepo.getGroupInfoByName(context,name)
	if err !=nil{

		log.Printf("error #%s#",err)	
		if pgErr,ok:=err.(*pq.Error);ok{
			if pgErr.Code == "23505"{
				log.Println("are we in the duplicate state")
				return true,nil
			}
		}
		if err == pgx.ErrNoRows{
			log.Println("alright we are in the no row state")
			return false,nil
		}
		return false,err
	}	

	return false,nil
}

