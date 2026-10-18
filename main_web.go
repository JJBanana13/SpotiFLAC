//go:build web

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/spotbye/SpotiFLAC/backend/uiruntime"
)

// Web server mode: the backend runs headless on this machine and the frontend
// is served over HTTPS. Every download is still written to the folders
// configured in the settings, i.e. on the machine running this server.
func main() {
	applyAppVersion()

	cfg := webConfig{}
	flag.StringVar(&cfg.Addr, "addr", envOr("SPOTIFLAC_ADDR", ":8443"), "listen address")
	flag.StringVar(&cfg.CertFile, "cert", os.Getenv("SPOTIFLAC_TLS_CERT"), "TLS certificate file (PEM); a self-signed certificate is generated when empty")
	flag.StringVar(&cfg.KeyFile, "key", os.Getenv("SPOTIFLAC_TLS_KEY"), "TLS private key file (PEM)")
	flag.BoolVar(&cfg.PlainHTTP, "http", envOr("SPOTIFLAC_PLAIN_HTTP", "") == "1", "serve plain HTTP (only behind a reverse proxy that terminates HTTPS)")
	flag.StringVar(&cfg.Username, "user", envOr("SPOTIFLAC_USER", "admin"), "login user name")
	flag.StringVar(&cfg.Password, "password", os.Getenv("SPOTIFLAC_PASSWORD"), "login password (or SPOTIFLAC_PASSWORD)")
	flag.BoolVar(&cfg.NoAuth, "no-auth", false, "disable login (not recommended)")
	flag.Parse()

	if cfg.Password == "" && !cfg.NoAuth {
		fmt.Fprintln(os.Stderr, "A password is required: pass -password or set SPOTIFLAC_PASSWORD (or use -no-auth).")
		os.Exit(2)
	}

	server := newWebServer(cfg)
	uiruntime.SetBridge(server)

	app := NewApp()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	app.startup(ctx)
	defer app.shutdown(ctx)

	if err := server.Run(ctx, app); err != nil {
		log.Fatal("Error:", err.Error())
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
