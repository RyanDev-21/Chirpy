package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"time"

	"RyanDev-21.com/Chirpy/internal/database"
	"RyanDev-21.com/Chirpy/pkg/auth"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

const (
	seedUsersCount = 100
)

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}

	dbURL := os.Getenv("DB_URL")
	if dbURL == "" {
		log.Fatal("DB_URL not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("Unable to connect to database: %v", err)
	}
	defer pool.Close()

	queries := database.New(pool)

	seedUsers(ctx, queries)
}

func seedUsers(ctx context.Context, q *database.Queries) {
	fmt.Println("Seeding users...")

	names := []string{
		"Alice", "Bob", "Charlie", "Diana", "Eve", "Frank", "Grace", "Henry", "Ivy", "Jack",
		"Kate", "Leo", "Mia", "Noah", "Olivia", "Peter", "Quinn", "Rose", "Sam", "Tina",
		"Uma", "Victor", "Wendy", "Xavier", "Yara", "Zack", "Anna", "Brian", "Chloe", "David",
		"Emma", "Felix", "Gina", "Hugo", "Iris", "James", "Kira", "Liam", "Maya", "Nathan",
		"Oliver", "Penny", "Quentin", "Rachel", "Steve", "Tara", "Ulysses", "Violet", "William", "Xena",
		"York", "Zara", "Adam", "Bella", "Chris", "Doris", "Eric", "Fiona", "George", "Hannah",
		"Ian", "Julia", "Kevin", "Luna", "Mike", "Nina", "Oscar", "Paula", "Ryan", "Sara",
		"Tom", "Una", "Vince", "Willa", "Xander", "Yvonne", "Zeke", "Amy", "Ben", "Cindy",
		"Dan", "Ellie", "Fred", "Gwen", "Hal", "Ida", "Jake", "Kelly", "Lance", "Molly",
	}

	passwords := []string{"password123", "test123", "changeme", "securepass", "demo123"}

	for i := 0; i < seedUsersCount; i++ {
		name := names[i%len(names)]
		email := fmt.Sprintf("%s%d@test.com", name, i)
		password := passwords[i%len(passwords)]

		hashedPassword, err := auth.HashPassword(password)
		if err != nil {
			log.Printf("Failed to hash password for %s: %v", email, err)
			continue
		}

		_, err = q.CreateUser(ctx, database.CreateUserParams{
			Name:     fmt.Sprintf("%s%d", name, i),
			Email:    email,
			Password: hashedPassword,
		})

		if err != nil {
			log.Printf("Failed to create user %s: %v", email, err)
		} else {
			fmt.Printf("Created user: %s\n", email)
		}

		time.Sleep(10 * time.Millisecond)
	}

	seedFriendRelationships(q)
}

func seedFriendRelationships(q *database.Queries) {
	fmt.Println("Seeding friend relationships...")

	ctx := context.Background()

	users, err := q.GetAllUser(ctx)
	if err != nil {
		log.Printf("Failed to get users: %v", err)
		return
	}

	if len(users) < 2 {
		fmt.Println("Not enough users to create relationships")
		return
	}

	rand.Seed(time.Now().UnixNano())

	relationshipsCreated := 0
	maxRelationships := 50

	for i := 0; i < len(users) && relationshipsCreated < maxRelationships; i++ {
		for j := i + 1; j < len(users) && relationshipsCreated < maxRelationships; j++ {
			if rand.Float32() < 0.3 {
				userID := users[i].ID
				otherUserID := users[j].ID

				reqID := uuid.New()

				err := q.AddSendReq(ctx, database.AddSendReqParams{
					ID:          reqID,
					UserID:      userID,
					OtheruserID: otherUserID,
				})
				if err != nil {
					continue
				}

				err = q.UpdateSendReq(ctx, reqID)
				if err != nil {
					continue
				}

				relationshipsCreated++
				fmt.Printf("Created friend relationship: %s <-> %s\n", users[i].Email, users[j].Email)
			}
		}
	}

	fmt.Printf("Created %d friend relationships\n", relationshipsCreated)
}
