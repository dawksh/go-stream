package main

import (
	"context"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	port := flag.Int("port", 8080, "HTTP server port")
	dataDir := flag.String("data", "/tmp/go-stream", "directory for torrent data")
	flag.Parse()

	manager, err := NewTorrentManager(*dataDir)
	if err != nil {
		log.Fatalf("Failed to create torrent manager: %v", err)
	}

	tmpl, err := template.ParseGlob("templates/*.html")
	if err != nil {
		log.Fatalf("Failed to parse templates: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", handleIndex(tmpl))
	mux.HandleFunc("POST /api/magnet", handleAddMagnet(manager))
	mux.HandleFunc("POST /api/select/{torrentId}", handleSelectFile(manager))
	mux.HandleFunc("GET /stream/{torrentId}", handleStream(manager))
	mux.HandleFunc("GET /subs/{torrentId}/{fileIndex}", handleSubtitle(manager))
	mux.HandleFunc("POST /api/subtitle/{torrentId}", handleUploadSubtitle(manager))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go manager.CleanupLoop(ctx, 24*time.Hour)

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", *port),
		Handler: mux,
	}

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Println("Shutting down...")
		cancel()

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTP shutdown error: %v", err)
		}
	}()

	log.Printf("Starting server on http://localhost:%d", *port)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}

	manager.Close()
	log.Println("Shutdown complete.")
}
