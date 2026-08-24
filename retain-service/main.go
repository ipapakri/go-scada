package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"

	"github.com/nats-io/nats.go"

	"go-scada/retain"
	"go-scada/stream"
)

func main() {
	configPath := flag.String("config", stream.DefaultConfigPath, "stream configuration file")
	dbPath := flag.String("db", "retain.db", "SQLite last-value database")
	flag.Parse()

	config, err := stream.LoadConfig(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	store, err := retain.Open(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	connection, err := nats.Connect(config.NATSURL)
	if err != nil {
		log.Fatalf("connect to NATS at %q: %v", config.NATSURL, err)
	}
	defer connection.Close()

	server, err := retain.Listen(connection, store, config.SystemName)
	if err != nil {
		log.Fatal(err)
	}
	defer server.Close()

	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()
	log.Printf(
		"Retain service listening on %s for system %s",
		config.NATSURL,
		config.SystemName,
	)
	<-ctx.Done()
}
