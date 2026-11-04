// Command admin grants or revokes Chirpy admin rights. It is the only way
// to become an admin (there is no API for it), and it talks to the database
// directly:
//
//	DB_URL=postgres://... go run ./cmd/admin grant you@example.com
//	DB_URL=postgres://... go run ./cmd/admin revoke you@example.com
//	DB_URL=postgres://... go run ./cmd/admin list
//
// Revoking takes effect immediately: the admin API checks the flag on
// every request. Every change is written to the admin audit log.
package main

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"RyanDev-21.com/Chirpy/internal/admin"
	"RyanDev-21.com/Chirpy/internal/database"
	"RyanDev-21.com/Chirpy/internal/users"
	"github.com/jackc/pgx/v5/pgxpool"
)

func usage() {
	fmt.Fprintln(os.Stderr, "usage: admin grant <email> | revoke <email> | list   (needs DB_URL)")
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	dbURL := os.Getenv("DB_URL")
	if dbURL == "" {
		fmt.Fprint(os.Stderr, "Database URL (DB_URL): ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		dbURL = strings.TrimSpace(line)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		fail(err)
	}
	defer pool.Close()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	svc := admin.NewService(pool, users.NewUserRepo(database.New(pool)), nil, logger)

	switch cmd := os.Args[1]; cmd {
	case "grant", "revoke":
		if len(os.Args) != 3 {
			usage()
		}
		name, err := svc.SetAdmin(ctx, os.Args[2], cmd == "grant")
		if err != nil {
			fail(err)
		}
		if cmd == "grant" {
			fmt.Printf("%s (%s) is now an admin. Sign in at https://api.ponypigeon.com/admin\n", name, os.Args[2])
		} else {
			fmt.Printf("%s (%s) is no longer an admin.\n", name, os.Args[2])
		}
	case "list":
		list, err := svc.Admins(ctx)
		if err != nil {
			fail(err)
		}
		if len(list) == 0 {
			fmt.Println("no admins")
		}
		for _, a := range list {
			fmt.Printf("%s\t%s\n", a.Name, a.Email)
		}
	default:
		usage()
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
