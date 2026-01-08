package rabbitmq

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"

	amqp "github.com/rabbitmq/amqp091-go"
)

var RabbitMQClient *RabbitMQ
var ErrInvalidPayload = errors.New("invalid payload")

type RabbitMQ struct{
	Conn *amqp.Connection
	Channel *amqp.Channel
}

//WARNING:right now using one cnetralized method to send all the payload to queue
func (r *RabbitMQ)PublishToQueue(payload any,rabbitMQ_queue string)error{
	q, err := r.Channel.QueueDeclare(
		rabbitMQ_queue,
		true,           // durable
	  false,          // delete when unused
	  false,          // exclusive
	  false,          // no-wait
  		nil, 
	)
	if err !=nil{
		log.Fatalf("failed to declare a queue %s",err);
	}
	body, err := json.Marshal(payload)
	if err !=nil{
		return ErrInvalidPayload
	}
	err = r.Channel.Publish(
  "",     // exchange
  q.Name, // routing key (queue name)
  false,  // mandatory
  false,  // immediate
  amqp.Publishing{
   ContentType: "application/json",
   Body:        body,
  })
 if err != nil {
  return fmt.Errorf("failed to publish the payload: %v", err)
 }
	log.Printf("payload has been published to rabbitMq queue :%s",payload)

	return nil

}

func NewRabbitMQ()*RabbitMQ {
	conn,err := amqp.Dial( os.Getenv("RABBITMQ_URL"));
	if err!=nil{
		log.Fatalf("failed to get the rabbitmq instance url");	
	}
	ch,err := conn.Channel()	
	if err !=nil{
		log.Fatalf("Failed to open a RabbitMQ channel")
	}

	RabbitMQClient= &RabbitMQ{
		Conn: conn, Channel: ch, }
	return RabbitMQClient
}


