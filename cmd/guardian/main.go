// Command guardian runs Guardian on its own: one process, one SQLite file.
//
//	PORT=8080 DATA_DIR=./data GUARDIAN_CONNECT=1 GUARDIAN_LANG=cs guardian
//
// Put it behind a reverse proxy with a login: the pages have no login of
// their own.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/miarmi/guardian"
)

func main() {
	log.SetFlags(0)
	port := envOr("PORT", "8080")
	dataDir := envOr("DATA_DIR", "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		log.Fatal(err)
	}
	app, err := guardian.New(context.Background(), guardian.Config{
		DataDir:  dataDir,
		Connect:  os.Getenv("GUARDIAN_CONNECT") == "1",
		Env:      os.Getenv("APP_ENV"),
		Version:  os.Getenv("APP_VERSION"),
		TimeZone: envOr("TZ", "UTC"),
		Language: envOr("GUARDIAN_LANG", "en"),
	})
	if err != nil {
		log.Fatal(err)
	}
	defer app.Close()

	srv := &http.Server{
		Addr:              envOr("LISTEN", "127.0.0.1") + ":" + port,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Printf("guardian listening on %s", srv.Addr)
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 9*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdown); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
