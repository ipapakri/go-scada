package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"go-scada/designer"
	"go-scada/stream"
)

func main() {
	configPath := flag.String("config", stream.DefaultConfigPath, "stream configuration file")
	listenAddress := flag.String("listen", "127.0.0.1:8080", "HTTP listen address")
	staticDirectory := flag.String("static", "designer-ui/dist", "compiled SPA directory")
	flag.Parse()

	streamClient, err := stream.New(
		*configPath,
		stream.WithErrorHandler(func(err error) { log.Printf("Stream error: %v", err) }),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer streamClient.Close()

	api, err := designer.NewServer(designer.NewStreamStore(streamClient), log.Default())
	if err != nil {
		log.Fatal(err)
	}
	handler := withStaticFiles(api.Handler(), *staticDirectory)
	httpServer := &http.Server{
		Addr:              *listenAddress,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownContext); err != nil {
			log.Printf("Shutdown designer service: %v", err)
		}
	}()

	log.Printf("Designer listening on http://%s", *listenAddress)
	if err := httpServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func withStaticFiles(api http.Handler, directory string) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/api/") {
			api.ServeHTTP(writer, request)
			return
		}
		cleanPath := strings.TrimPrefix(path.Clean("/"+request.URL.Path), "/")
		if cleanPath == "" {
			cleanPath = "index.html"
		}
		target := filepath.Join(directory, filepath.FromSlash(cleanPath))
		info, err := os.Stat(target)
		if err == nil && !info.IsDir() {
			http.ServeFile(writer, request, target)
			return
		}
		index := filepath.Join(directory, "index.html")
		if _, err := os.Stat(index); err != nil {
			http.Error(writer, "designer UI has not been built", http.StatusNotFound)
			return
		}
		http.ServeFile(writer, request, index)
	})
}
