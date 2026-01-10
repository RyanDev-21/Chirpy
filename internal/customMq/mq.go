package mq

import (
	"log"
	"sync"
)
//the most basic form of the mq first

//stores tag and the channel whcih can receive anything

type PubStruct struct{
	topic string 
	info any
}


type WorkerFunction func(chan *Channel)

type DoneStruct struct{
	topic string
	localTag int
}

type ResultStruct struct{
	tag int
	topic string
	msg any
	retries int
}

type MainMQ struct{
	mainMq map[string]chan *Channel
	mu sync.RWMutex
	//pub should stores the task name and then channel
	pub chan PubStruct
	FailedResult chan ResultStruct
	retriesLimit int
}


type Channel struct{
	topic string 
	LocalTag int
	Msg any
}



func NewMainMQ(mq *map[string]chan *Channel,retiresLimit int)*MainMQ{
	return &MainMQ{
		mainMq: *mq,
		pub: make(chan PubStruct),
		FailedResult: make(chan ResultStruct,100),
		retriesLimit: retiresLimit,
	}
}

func (mq *MainMQ)AddTopic(topic string){
	mq.mainMq = make(map[string]chan *Channel)
	mq.mainMq[topic]=make(chan *Channel,1000) 
}


func (mq *MainMQ)Run(){
	jobCount := make(map[string]int)
	 for {
		select{
		case task := <- mq.pub:
			log.Printf("inside the pub struct now")
			mq.mu.RLock();
			//if the topic doesn't exist create one
			//and set its job count to 0
				if _,ok :=mq.mainMq[task.topic];!ok{
						mq.mainMq[task.topic] = make(chan *Channel,1000);
						jobCount[task.topic] = 0	
			}
			//before firiing ,increment jobCount to make the lcoalTag start at 1
			jobCount[task.topic] +=1

			log.Printf("firing of for the topic %v workerNubmer %v",task.topic,jobCount[task.topic])
			//send the Channel type struct into the channel 
			mq.mainMq[task.topic]<-&Channel{
				topic: task.topic,
				LocalTag: jobCount[task.topic],
				Msg: task.info,
			}
			mq.mu.RUnlock();
			//finally unlock the read lock
		case result := <-mq.FailedResult:
			//pull from the fail channel and if their retires count is lower than the limit then 
			//resend it through the pub channel again with the same tag and info
			log.Printf("got result from %v ,retries :%v\n",result.topic,result.retries)
				log.Printf("restrying the msg id :%v\n",result.tag)
			if result.retries<mq.retriesLimit{

				mq.mainMq[result.topic]<- &Channel{
					topic: result.topic,
					LocalTag: result.tag,
					Msg: result.msg,
				}	
			}else{
				log.Printf("retries count exceed the limit\n saving it to log\n")
			}	
		}						
	}
}


//use this one to publish the topic along with the info
func (mq *MainMQ)Publish(topic string,info any){
	mq.pub<-PubStruct{
		topic: topic,
		info: info,
	}		
}

func (mq *MainMQ)Republish(channel *Channel,retries int){
	mq.FailedResult<-ResultStruct{
		topic: channel.topic,
		tag: channel.LocalTag,
		msg: channel.Msg,
		retries: retries,
	}	
}

// the worker basically recieve all the msg from the chan 
// if the operation failed then you retry it with the pub one again
// this fucntion is just for testing purpose
// func (mq *MainMQ)workerForTheSmth(channel chan*Channel,workfucntion func()){
// 	retiresChanList := make(map[int]int)
// 	   for msg := range channel{
// 			workfucntion()	
// 		if msg.LocalTag == 3{
// 			retiresChanList[msg.LocalTag] +=1
// 			mq.FailedResult<-ResultStruct{
// 				topic: msg.topic,
// 				tag: msg.LocalTag,
// 				msg: msg.Msg,
// 				retries: retiresChanList[msg.LocalTag],
// 			}
// 		}
// 	}
// }


//we don't need this one at all 
func (mq *MainMQ)GetChannelForTopic(topic string)chan *Channel{
	if v, ok := mq.mainMq[topic];ok{
		return v
	}
	return nil
}

//wait the topic to be created and then make those worker do the job
func (mq *MainMQ)ListeningForTheChannels(topic string,numWorkers int,workFunction WorkerFunction){
	var channel chan *Channel	
	for {
		mq.mu.RLock();
		if ch,ok := mq.mainMq[topic]; ok{
		    channel = ch	
         mq.mu.RUnlock()
         break
		}
		mq.mu.RUnlock();
     
		}
  for i := 0; i<numWorkers;i++{
		go workFunction(channel)
}
	} 


// func main(){
// 	messageQueue := make(map[string]chan*Channel)		
// 	mq := NewMainMQ(&messageQueue,10);
//  	var saveIntoDb = "save into db"
// 	var notifyEvent = "pls notify this"
// 	go mq.Run()
// 	messages := []PubStruct{
// 		{saveIntoDb, "Hello from 1 of db"},
// 		{saveIntoDb, "Hello from 2 of db"},
// 		{saveIntoDb, "Hello from 3 of db"},
// 		{saveIntoDb, "Hello from 4 of db"},
// 		{notifyEvent, "Hello from 1 of noti"},
// 		{notifyEvent, "Hello from 2 of noti"},
// 		{notifyEvent, "Hello from 3 of noti"},
// 		{notifyEvent, "Hello from 4 of noti"},
// }	
// 	for i := range messages{
// 		mq.publish(messages[i].topic,messages[i].info)
// 	}
//
// 	go mq.listeningForTheChannels(saveIntoDb,3,mq.workerForTheSmth)
// 	go mq.listeningForTheChannels(notifyEvent,3,mq.workerForTheSmth)
//
//
//
// 	sigs := make(chan os.Signal, 1)
// 	done := make(chan bool, 1)
//
// 	// 2. Register the channel to receive notifications for specific signals.
// 	// In this case, SIGINT (Ctrl+C) and SIGTERM (terminate).
// 	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
//
// 	// 3. Start a goroutine to handle received signals.
// 	go func() {
// 		// This blocks until a signal is received on the 'sigs' channel.
// 		sig := <-sigs
// 		fmt.Printf("\nReceived signal: %s\n", sig)
//
// 		// Perform cleanup or graceful shutdown logic here
// 		fmt.Println("Performing graceful shutdown... cleaning up resources.")
//
// 		// Signal the main goroutine that processing is done.
// 		done <- true
// 	}()
//
// 	// 4. The main goroutine waits for the signal handler to finish.
// 	fmt.Println("Program is running. Press Ctrl+C or send a SIGTERM to exit.")
// 	<-done
// 	fmt.Println("Program exited gracefully.")
// }
