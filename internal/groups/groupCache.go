package groups

import (
	"context"
	"database/sql"
	"time"

	"RyanDev-21.com/Chirpy/internal/database"
	"github.com/google/uuid"
)

type CacheGroupInfo struct{
	total_mem int16
	name string
	max_mem int16
}

type GroupCache struct{
	GroupCache map[uuid.UUID]*CacheGroupInfo
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
		groupRepo: groupRepo,
	}
}


//this will get all rows from the group and store it in the map NOTE:: need to fix the currentMember(total) value 
func (cache *GroupCache)LoadUpGroupCache()error{
	context,cancel:=context.WithTimeout(context.Background(),1*time.Second)
	defer cancel()
	groupInfo, err :=cache.groupRepo.getAllGroupInfo(context)
	if err !=nil{
		return err
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

func (cache *GroupCache)GetGroupInfoFromGroupCache(groupID uuid.UUID)(*CacheGroupInfo,error){
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

func (cache *GroupCache)CheckGroupNameFromCache(name string)(bool,error){
	for _,v := range cache.GroupCache{
		if v.name == name{
			return false, nil	
		}
	}	
	context,cancel := context.WithTimeout(context.Background(),1*time.Second)
	defer cancel()
	_,err := cache.groupRepo.getGroupInfoByName(context,name)
	if err == sql.ErrNoRows{
			return true,nil
	}
	return false,err
}

