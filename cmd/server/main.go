package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/afkarxyz/SpotiFLAC/backend"
	"github.com/afkarxyz/SpotiFLAC/web"
)

//go:embed all:frontend_dist
var frontendFS embed.FS

const frontendRoot = "frontend_dist"

func main() {
	backend.AppVersion = envOr("SPOTIFLAC_VERSION", "dev")

	if err := backend.InitHistoryDB("SpotiFLAC"); err != nil {
		log.Printf("init history db: %v", err)
	}
	if err := backend.InitISRCCacheDB(); err != nil {
		log.Printf("init isrc cache db: %v", err)
	}
	if err := backend.InitProviderPriorityDB(); err != nil {
		log.Printf("init provider priority db: %v", err)
	}
	if err := backend.SanitizePersistedConfigSettings(); err != nil {
		log.Printf("sanitize config: %v", err)
	}

	if err := web.InitAuth(); err != nil {
		log.Fatalf("init auth: %v", err)
	}

	startTempCleanup()

	addr := envOr("SPOTIFLAC_LISTEN", ":8080")

	e := web.NewRouter(frontendFS, frontendRoot)
	go func() {
		if err := e.Start(addr); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	}()
	log.Printf("SpotiFLAC web listening on %s", addr)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = e.Shutdown(ctx)

	web.CloseAuth()
	backend.CloseHistoryDB()
	backend.CloseISRCCacheDB()
	backend.CloseProviderPriorityDB()
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// startTempCleanup periodically wipes stale per-user temp dirs in case downloads were aborted.
func startTempCleanup() {
	go func() {
		ttl := time.Hour
		if v := os.Getenv("SPOTIFLAC_TMP_TTL_MIN"); v != "" {
			var min int
			if _, err := fmt.Sscanf(v, "%d", &min); err == nil && min > 0 {
				ttl = time.Duration(min) * time.Minute
			}
		}
		base := os.Getenv("SPOTIFLAC_TMP_DIR")
		if base == "" {
			base = os.TempDir() + string(os.PathSeparator) + "spotiflac"
		}
		_ = os.MkdirAll(base, 0o755)

		ticker := time.NewTicker(15 * time.Minute)
		defer ticker.Stop()
		sweep := func() {
			cutoff := time.Now().Add(-ttl)
			entries, err := os.ReadDir(base)
			if err != nil {
				return
			}
			for _, userDir := range entries {
				if !userDir.IsDir() {
					continue
				}
				userPath := base + string(os.PathSeparator) + userDir.Name()
				jobs, err := os.ReadDir(userPath)
				if err != nil {
					continue
				}
				for _, j := range jobs {
					if !j.IsDir() {
						continue
					}
					info, err := j.Info()
					if err != nil {
						continue
					}
					if info.ModTime().Before(cutoff) {
						_ = os.RemoveAll(userPath + string(os.PathSeparator) + j.Name())
					}
				}
			}
		}
		sweep()
		for range ticker.C {
			sweep()
		}
	}()
}
