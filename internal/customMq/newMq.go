package mq

// import (
// 	"fmt"
// 	"log"
// 	"os"
// 	"os/signal"
// 	"sync"
// 	"syscall"
// 	"time"
// )
//
// type PubStruct struct {
// 	generalTag string
// 	info       string
// }
//
// type ResultStruct struct {
// 	tag     int
// 	topic   string
// 	msg     string
// 	retries int
// }
//
// type mainMQ struct {
// 	mainMq       map[string]chan *Channel
// 	mu           sync.Mutex
// 	pub          chan PubStruct
// 	FailedResult chan ResultStruct
// }
//
// type Channel struct {
// 	generalTag string
// 	localTag   int
// 	msg        string
// }
//
// func NewMainMQ() *mainMQ {
// 	return &mainMQ{
// 		mainMq:       make(map[string]chan *Channel),
// 		pub:          make(chan PubStruct),
// 		FailedResult: make(chan ResultStruct, 100),
// 	}
// }
//
// func (m *mainMQ) Run() {
// 	taskCounter := make(map[string]int)
//
// 	for {
// 		select {
// 		case task := <-m.pub:
// 			m.mu.Lock()
// 			// Only create channel if it doesn't exist
// 			if _, ok := m.mainMq[task.generalTag]; !ok {
// 				m.mainMq[task.generalTag] = make(chan *Channel, 100)
// 				taskCounter[task.generalTag] = 0
// 			}
//
// 			localTag := taskCounter[task.generalTag]
// 			taskCounter[task.generalTag]++
//
// 			log.Printf("Publishing to topic %v, task #%v", task.generalTag, localTag)
//
// 			m.mainMq[task.generalTag] <- &Channel{
// 				generalTag: task.generalTag,
// 				localTag:   localTag,
// 				msg:        task.info,
// 			}
// 			m.mu.Unlock()
//
// 		case result := <-m.FailedResult:
// 			log.Printf("Got failed result from topic %v, tag %v (retry %d)", 
// 				result.topic, result.tag, result.retries)
//
// 			if result.retries < 3 {
// 				log.Printf("Retrying message tag %v", result.tag)
// 				m.mu.Lock()
// 				m.mainMq[result.topic] <- &Channel{
// 					generalTag: result.topic,
// 					localTag:   result.tag,
// 					msg:        result.msg,
// 				}
// 				m.mu.Unlock()
// 			} else {
// 				log.Printf("Message tag %v failed after 3 retries, moving to DLQ", result.tag)
// 			}
// 		}
// 	}
// }
//
// func (mq *mainMQ) publish(topic string, info string) {
// 	mq.pub <- PubStruct{
// 		generalTag: topic,
// 		info:       info,
// 	}
// }
//
// func (mq *mainMQ) workerForTheSmth(workerID int, channel chan *Channel) {
// 	retriesChanList := make(map[int]int)
//
// 	for msg := range channel {  // Keep processing messages forever
// 		log.Printf("Worker %d processing task %v: %s", workerID, msg.localTag, msg.msg)
// 		time.Sleep(500 * time.Millisecond) // Simulate work
//
// 		// Simulate failure for task 3
// 		if msg.localTag == 3 {
// 			retriesChanList[msg.localTag]++
// 			log.Printf("Task %v failed (attempt %d)", msg.localTag, retriesChanList[msg.localTag])
//
// 			mq.FailedResult <- ResultStruct{
// 				topic:   msg.generalTag,
// 				tag:     msg.localTag,
// 				msg:     msg.msg,
// 				retries: retriesChanList[msg.localTag],
// 			}
// 		} else {
// 			log.Printf("Task %v completed successfully", msg.localTag)
// 		}
// 	}
// }
//
// func (mq *mainMQ) listeningForTheChannels(topic string, numWorkers int) {
// 	// Wait for channel to be created
// 	var channel chan *Channel
// 	for {
// 		mq.mu.Lock()
// 		if ch, ok := mq.mainMq[topic]; ok {
// 			channel = ch
// 			mq.mu.Unlock()
// 			break
// 		}
// 		mq.mu.Unlock()
// 		time.Sleep(100 * time.Millisecond)
// 	}
//
// 	log.Printf("Starting %d workers for topic: %s", numWorkers, topic)
//
// 	// Start multiple workers for this topic
// 	for i := 0; i < numWorkers; i++ {
// 		go mq.workerForTheSmth(i, channel)
// 	}
// }
//
// func main() {
// 	mq := NewMainMQ()
// 	var saveIntoDb = "save into db"
// 	var notifyEvent = "pls notify this"
//
// 	go mq.Run()
//
// 	// Start workers (they'll wait for the channels to be created)
// 	go mq.listeningForTheChannels(saveIntoDb, 3)
// 	go mq.listeningForTheChannels(notifyEvent, 2)
//
// 	time.Sleep(100 * time.Millisecond) // Give workers time to start
//
// 	// Publish messages - use a slice instead of map
// 	messages := []PubStruct{
// 		{saveIntoDb, "Hello from 1 of db"},
// 		{saveIntoDb, "Hello from 2 of db"},
// 		{saveIntoDb, "Hello from 3 of db"},
// 		{saveIntoDb, "Hello from 4 of db"},
// 		{notifyEvent, "Hello from 1 of noti"},
// 		{notifyEvent, "Hello from 2 of noti"},
// 		{notifyEvent, "Hello from 3 of noti"},
// 		{notifyEvent, "Hello from 4 of noti"},
// 	}
//
// 	for _, msg := range messages {
// 		mq.publish(msg.generalTag, msg.info)
// 	}
//
// 	sigs := make(chan os.Signal, 1)
// 	done := make(chan bool, 1)
// 	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
//
// 	go func() {
// 		sig := <-sigs
// 		fmt.Printf("\nReceived signal: %s\n", sig)
// 		fmt.Println("Performing graceful shutdown...")
// 		done <- true
// 	}()
//
// 	fmt.Println("Program is running. Press Ctrl+C to exit.")
// 	<-done
// 	fmt.Println("Program exited gracefully.")
// }
