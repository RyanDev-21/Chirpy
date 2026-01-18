package chatmodel

import (
	"context"
	"fmt"
	"time"
	"github.com/google/uuid"
)

func GetChatKey(firstID,secondID uuid.UUID)string{
	if firstID.String()<secondID.String(){
		return fmt.Sprintf("%v_%v",firstID,secondID)
	}	
	return fmt.Sprintf("%v_%v",firstID,secondID)
}

func HandlePrivateMsg(c *Client,msg *Message,parseID uuid.UUID){
	context,cancel := context.WithTimeout(context.Background(),1*time.Second)
		defer cancel()
	//generate  for the chatID
		key := GetChatKey(c.UserID,parseID)

		//first need to update the cache
		c.Cache.AddMessage(context,key,&MessageCache{
			Msg:*msg,
			FromID:c.UserID,	
	})	

		//second publish the job
		c.MsgQ.Publish("addMessagePrivate",&PublishMessageStruct{
			Msg: msg,
			UserID: c.UserID,
		})	
}

