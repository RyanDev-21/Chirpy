package setup

import (
	"context"
	"log"
	"os"

	"github.com/joho/godotenv"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func GetClient() *mongo.Client {
	err := godotenv.Load("../../.env")
	if err != nil {
		log.Fatal("failed to load the env")
	}
	uri := os.Getenv("MONGO_URL")
	if uri == "" {
		log.Fatal("failed to get the uri")
	}
	client, err := mongo.Connect(options.Client().
		ApplyURI(uri))
	if err != nil {
		panic(err)
	}
	defer func() {
		if err := client.Disconnect(context.TODO()); err != nil {
			panic(err)
		}
	}()
	return client
}
