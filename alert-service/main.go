package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os/signal"
	"syscall"
	"time"

	"go-scada/alert"
	"go-scada/stream"
)

func main() {
	pprofAddr := flag.String(
		"pprof",
		"127.0.0.1:6060",
		"pprof HTTP listen address; empty disables",
	)
	flag.Parse()

	if *pprofAddr != "" {
		server := &http.Server{
			Addr:              *pprofAddr,
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			log.Printf("pprof listening on http://%s/debug/pprof/", *pprofAddr)
			if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
				log.Printf("pprof server: %v", err)
			}
		}()
	}

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

	service, err := alert.NewService(streamClient, log.Default())
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
