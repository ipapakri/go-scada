package main

import (
	"log"

	"go-scada/stream"
)

func main() {
	streamClient, err := stream.New(stream.DefaultConfigPath)
	if err != nil {
		log.Fatalf("create stream client: %v", err)
	}
	defer streamClient.Close()

	if err := streamClient.CreateStream(); err != nil {
		log.Fatalf("create stream: %v", err)
	}

	log.Println("Stream created successfully")
}
