package chatmodel

import (
	//	"bytes"
	"log"
	"time"

	mq "RyanDev-21.com/Chirpy/internal/customMq"
	rediscache "RyanDev-21.com/Chirpy/internal/redisCache"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

//this is standard way of doing this i guess
const (
	writeWait = 10 *time.Second
	
	pongWait = 60* time.Second

	pingPeriod = (pongWait*9)/10
	
	maxMessageSize = 512
)



type Client struct{
	Hub *Hub
	Conn *websocket.Conn
	Send chan []byte
	UserID uuid.UUID
	MsgQ *mq.MainMQ
	Cache *rediscache.RedisCacheImpl
}


func NewClient(hub *Hub,conn *websocket.Conn,send chan []byte,userID uuid.UUID,msgQ *mq.MainMQ,redisCache *rediscache.RedisCacheImpl)*Client{
	return &Client{
		Hub: hub,
		Conn: conn,
		Send: send,
		UserID: userID,
		MsgQ: msgQ ,
		Cache: redisCache,
	}
}


var (
	newline = []byte{'\n'}
	space = []byte{' '}
)

//read message from the connection
//and Send the msg to the Broadcast channel
func (c *Client)ReadPump(){
	defer func(){
		c.Hub.Unregister<-c
		c.Conn.Close()
	}()

	c.Conn.SetReadLimit(maxMessageSize)
	c.Conn.SetReadDeadline(time.Now().Add(pongWait))
	//when it recieve pong message from the connection will add more sec to deadline	
	c.Conn.SetPongHandler(func(string)error{c.Conn.SetReadDeadline(time.Now().Add(pongWait));return nil})
	
	var msg Message

	for {

		err:= c.Conn.ReadJSON(&msg)
		if err !=nil{
			if websocket.IsUnexpectedCloseError(err,websocket.CloseGoingAway,websocket.CloseAbnormalClosure){
				log.Printf("error :%v",err)

			}	
			break
		}
		//the toID should be uuid.UUID only 
		_, err=uuid.Parse(msg.ToID)
		if err !=nil{
			log.Println("parsing the uuid failed")
		}
		
		//generate  for the chatID
		//NOTE::need to fix this one
//	    _ = GenerateChatID(c.UserID,parseID)				
		
		//first need to update the cache
		log.Printf("update the cache completed")	
		//second publish the job
		c.MsgQ.Publish("addMessage",&PublishMessageStruct{
			Msg: &msg,
			UserID: c.UserID,
		})	


		//the last parameters takes how many you wanna replace if <0 there is no limit
		//as we don't read the message type anymore 	
		//msg= bytes.TrimSpace(bytes.Replace(message,newline,space,-1))		

		c.Hub.Broadcast <- msg
	
	}
}


//read from the Send chanel and write it to the connection
func (c *Client)WritePump(){
	ticker := time.NewTicker(pingPeriod)
	defer func(){
		ticker.Stop()
		c.Conn.Close()	
	}()
	for {
		select {
		case message,ok := <-c.Send:
		c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
		if !ok{
			//The Hub closed the channel
				c.Conn.WriteMessage(websocket.CloseMessage,[]byte{})
				return
			}
			w, err := c.Conn.NextWriter(websocket.TextMessage)
			if err !=nil{
				return
			}
			w.Write(message)
			n := len(c.Send)
				for i :=0;i<n;i++{
					w.Write(newline)
					w.Write(<-c.Send)
				}
			
			if err := w.Close(); err !=nil{
				return
			}
		case <-ticker.C:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.Conn.WriteMessage(websocket.PingMessage,nil); err !=nil{
				return
			}
		}
	}
			
	
}
