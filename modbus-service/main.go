package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"go-scada/modbus"
	"go-scada/stream"
)

func main() {
	streamClient, err := stream.New(
		stream.DefaultConfigPath,
		stream.WithErrorHandler(func(err error) {
			log.Printf("Stream error: %v", err)
		}),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer streamClient.Close()

	service, err := modbus.NewService(streamClient, log.Default())
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()
	if err := service.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
