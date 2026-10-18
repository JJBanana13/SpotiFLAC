//go:build web

package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spotbye/SpotiFLAC/backend"
	"github.com/spotbye/SpotiFLAC/backend/uiruntime"
)

const (
	webDialogEvent  = "spotiflac:web-dialog"
	webOpenURLEvent = "spotiflac:web-open-url"
	webDialogWait   = 15 * time.Minute
	webRequestHdr   = "X-SpotiFLAC-Request"
)

// Methods that only make sense with a local desktop window.
var webDisabledMethods = map[string]string{
	"OpenFolder":       "opening folders is not available in the web interface",
	"OpenConfigFolder": "opening folders is not available in the web interface",
}

type webConfig struct {
	Addr      string
	CertFile  string
	KeyFile   string
	PlainHTTP bool
	Username  string
	Password  string
	NoAuth    bool
}

type webEvent struct {
	Name string        `json:"name"`
	Data []interface{} `json:"data"`
}

type webDialogRequest struct {
	ID               string                 `json:"id"`
	Kind             string                 `json:"kind"`
	Title            string                 `json:"title"`
	DefaultDirectory string                 `json:"defaultDirectory"`
	DefaultFilename  string                 `json:"defaultFilename"`
	Filters          []uiruntime.FileFilter `json:"filters"`
}

type webDialogResponse struct {
	Cancelled bool     `json:"cancelled"`
	Paths     []string `json:"paths"`
}

type webServer struct {
	cfg webConfig

	clientsMu sync.Mutex
	clients   map[chan []byte]struct{}

	dialogsMu sync.Mutex
	dialogs   map[string]chan webDialogResponse
}

func newWebServer(cfg webConfig) *webServer {
	return &webServer{
		cfg:     cfg,
		clients: make(map[chan []byte]struct{}),
		dialogs: make(map[string]chan webDialogResponse),
	}
}

// ---- uiruntime.Bridge ----

func (s *webServer) EventsEmit(eventName string, optionalData ...interface{}) {
	if optionalData == nil {
		optionalData = []interface{}{}
	}
	payload, err := json.Marshal(webEvent{Name: eventName, Data: optionalData})
	if err != nil {
		log.Printf("web: failed to encode event %q: %v", eventName, err)
		return
	}
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	for ch := range s.clients {
		select {
		case ch <- payload:
		default:
			// Slow client; drop the event rather than blocking the backend.
		}
	}
}

func (s *webServer) OpenDirectoryDialog(options uiruntime.OpenDialogOptions) (string, error) {
	paths, err := s.askDialog("directory", options.Title, options.DefaultDirectory, "", options.Filters)
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}

func (s *webServer) OpenFileDialog(options uiruntime.OpenDialogOptions) (string, error) {
	paths, err := s.askDialog("file", options.Title, options.DefaultDirectory, "", options.Filters)
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}

func (s *webServer) OpenMultipleFilesDialog(options uiruntime.OpenDialogOptions) ([]string, error) {
	return s.askDialog("files", options.Title, options.DefaultDirectory, "", options.Filters)
}

func (s *webServer) SaveFileDialog(options uiruntime.SaveDialogOptions) (string, error) {
	paths, err := s.askDialog("save", options.Title, options.DefaultDirectory, options.DefaultFilename, options.Filters)
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}

func (s *webServer) BrowserOpenURL(target string) {
	s.EventsEmit(webOpenURLEvent, target)
}

// askDialog asks the connected browser(s) to show a server-side file browser
// and waits for the first answer.
func (s *webServer) askDialog(kind, title, defaultDir, defaultFilename string, filters []uiruntime.FileFilter) ([]string, error) {
	s.clientsMu.Lock()
	connected := len(s.clients)
	s.clientsMu.Unlock()
	if connected == 0 {
		return nil, errors.New("no browser connected to show the dialog")
	}

	if defaultDir == "" {
		defaultDir = backend.GetDefaultMusicPath()
	}
	id := randomHex(16)
	ch := make(chan webDialogResponse, 1)
	s.dialogsMu.Lock()
	s.dialogs[id] = ch
	s.dialogsMu.Unlock()
	defer func() {
		s.dialogsMu.Lock()
		delete(s.dialogs, id)
		s.dialogsMu.Unlock()
	}()

	s.EventsEmit(webDialogEvent, webDialogRequest{
		ID:               id,
		Kind:             kind,
		Title:            title,
		DefaultDirectory: defaultDir,
		DefaultFilename:  defaultFilename,
		Filters:          filters,
	})

	select {
	case resp := <-ch:
		if resp.Cancelled {
			return nil, nil
		}
		return resp.Paths, nil
	case <-time.After(webDialogWait):
		return nil, errors.New("dialog timed out")
	}
}

// ---- HTTP ----

func (s *webServer) Run(ctx context.Context, app *App) error {
	dist, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/call/{method}", func(w http.ResponseWriter, r *http.Request) {
		s.handleCall(w, r, app)
	})
	mux.HandleFunc("GET /api/events", s.handleEvents)
	mux.HandleFunc("POST /api/dialog/{id}", s.handleDialogResponse)
	mux.HandleFunc("GET /api/fs", s.handleListDir)
	mux.HandleFunc("GET /api/community-callback/{port}/session-grant", handleCommunityCallback)
	mux.Handle("/", http.FileServerFS(dist))

	srv := &http.Server{
		Addr:              s.cfg.Addr,
		Handler:           s.withSecurity(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	if s.cfg.PlainHTTP {
		log.Printf("SpotiFLAC web interface on http://%s (plain HTTP, terminate TLS in a reverse proxy)", displayAddr(s.cfg.Addr))
		err = srv.ListenAndServe()
	} else {
		certFile, keyFile := s.cfg.CertFile, s.cfg.KeyFile
		if certFile == "" || keyFile == "" {
			certFile, keyFile, err = ensureSelfSignedCert()
			if err != nil {
				return fmt.Errorf("failed to create self-signed certificate: %w", err)
			}
			log.Printf("Using self-signed certificate %s", certFile)
		}
		srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		log.Printf("SpotiFLAC web interface on https://%s", displayAddr(s.cfg.Addr))
		err = srv.ListenAndServeTLS(certFile, keyFile)
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *webServer) withSecurity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.cfg.NoAuth {
			user, pass, ok := r.BasicAuth()
			userOK := subtle.ConstantTimeCompare([]byte(user), []byte(s.cfg.Username)) == 1
			passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(s.cfg.Password)) == 1
			if !ok || !userOK || !passOK {
				w.Header().Set("WWW-Authenticate", `Basic realm="SpotiFLAC", charset="UTF-8"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}
		// State-changing API requests must carry a custom header. Browsers
		// cannot send it cross-origin without a CORS preflight, which this
		// server never approves, so other sites cannot drive the backend.
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get(webRequestHdr) != "1" {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		if !s.cfg.PlainHTTP {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func (s *webServer) handleCall(w http.ResponseWriter, r *http.Request, app *App) {
	name := r.PathValue("method")
	if name == "Quit" {
		writeJSON(w, http.StatusOK, nil)
		return
	}
	if reason, disabled := webDisabledMethods[name]; disabled {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": reason})
		return
	}
	method := reflect.ValueOf(app).MethodByName(name)
	if !method.IsValid() {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown method " + name})
		return
	}

	var rawArgs []json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&rawArgs); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid arguments: " + err.Error()})
		return
	}
	methodType := method.Type()
	if len(rawArgs) > methodType.NumIn() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "too many arguments for " + name})
		return
	}
	args := make([]reflect.Value, methodType.NumIn())
	for i := range args {
		arg := reflect.New(methodType.In(i))
		if i < len(rawArgs) && len(rawArgs[i]) > 0 && string(rawArgs[i]) != "null" {
			if err := json.Unmarshal(rawArgs[i], arg.Interface()); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("argument %d of %s: %v", i+1, name, err)})
				return
			}
		}
		args[i] = arg.Elem()
	}

	results, panicErr := callSafely(method, args)
	if panicErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": panicErr.Error()})
		return
	}

	errorType := reflect.TypeOf((*error)(nil)).Elem()
	var value interface{}
	for _, result := range results {
		if result.Type() == errorType {
			if !result.IsNil() {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": result.Interface().(error).Error()})
				return
			}
			continue
		}
		value = result.Interface()
	}
	writeJSON(w, http.StatusOK, value)
}

func callSafely(method reflect.Value, args []reflect.Value) (results []reflect.Value, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("%v", p)
		}
	}()
	return method.Call(args), nil
}

func (s *webServer) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := make(chan []byte, 1024)
	s.clientsMu.Lock()
	s.clients[ch] = struct{}{}
	s.clientsMu.Unlock()
	defer func() {
		s.clientsMu.Lock()
		delete(s.clients, ch)
		s.clientsMu.Unlock()
	}()

	_, _ = io.WriteString(w, ": connected\n\n")
	flusher.Flush()

	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case payload := <-ch:
			if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *webServer) handleDialogResponse(w http.ResponseWriter, r *http.Request) {
	var resp webDialogResponse
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&resp); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.dialogsMu.Lock()
	ch, ok := s.dialogs[r.PathValue("id")]
	if ok {
		delete(s.dialogs, r.PathValue("id"))
	}
	s.dialogsMu.Unlock()
	if ok {
		ch <- resp
	}
	writeJSON(w, http.StatusOK, map[string]bool{"accepted": ok})
}

type webDirEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"isDir"`
}

type webDirListing struct {
	Path    string        `json:"path"`
	Parent  string        `json:"parent"`
	Sep     string        `json:"sep"`
	Roots   []string      `json:"roots"`
	Entries []webDirEntry `json:"entries"`
}

func (s *webServer) handleListDir(w http.ResponseWriter, r *http.Request) {
	dir := strings.TrimSpace(r.URL.Query().Get("path"))
	if dir == "" {
		dir = backend.GetDefaultMusicPath()
	}
	dir = filepath.Clean(dir)
	// Fall back to the closest existing parent, e.g. when the configured
	// download folder has not been created yet.
	for {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	items, err := os.ReadDir(dir)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	listing := webDirListing{Path: dir, Sep: string(filepath.Separator), Roots: fsRoots(), Entries: []webDirEntry{}}
	if parent := filepath.Dir(dir); parent != dir {
		listing.Parent = parent
	}
	for _, item := range items {
		isDir := item.IsDir()
		if item.Type()&os.ModeSymlink != 0 {
			if info, err := os.Stat(filepath.Join(dir, item.Name())); err == nil {
				isDir = info.IsDir()
			}
		}
		listing.Entries = append(listing.Entries, webDirEntry{Name: item.Name(), IsDir: isDir})
	}
	sort.SliceStable(listing.Entries, func(i, j int) bool {
		a, b := listing.Entries[i], listing.Entries[j]
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	writeJSON(w, http.StatusOK, listing)
}

func fsRoots() []string {
	if runtime.GOOS != "windows" {
		return []string{"/"}
	}
	var roots []string
	for letter := 'A'; letter <= 'Z'; letter++ {
		root := string(letter) + `:\`
		if _, err := os.Stat(root); err == nil {
			roots = append(roots, root)
		}
	}
	return roots
}

// handleCommunityCallback forwards the community verification redirect, which
// the backend expects on a loopback listener, from the remote browser to that
// listener on this machine.
func handleCommunityCallback(w http.ResponseWriter, r *http.Request) {
	port, err := strconv.Atoi(r.PathValue("port"))
	if err != nil || port <= 0 || port > 65535 {
		http.Error(w, "invalid port", http.StatusBadRequest)
		return
	}
	target := url.URL{
		Scheme:   "http",
		Host:     net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		Path:     "/session-grant",
		RawQuery: r.URL.RawQuery,
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Get(target.String())
	if err != nil {
		http.Error(w, "verification callback is not active", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 1<<20))
}

func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("web: failed to encode response: %v", err)
	}
}

func randomHex(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func displayAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "0.0.0.0" + addr
	}
	return addr
}

func ensureSelfSignedCert() (string, string, error) {
	appDir, err := backend.EnsureAppDir()
	if err != nil {
		return "", "", err
	}
	dir := filepath.Join(appDir, "web-tls")
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")
	if _, err := tls.LoadX509KeyPair(certFile, keyFile); err == nil {
		return certFile, keyFile, nil
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", "", err
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", err
	}
	template := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "SpotiFLAC"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	if hostname, err := os.Hostname(); err == nil && hostname != "" {
		template.DNSNames = append(template.DNSNames, hostname)
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
				template.IPAddresses = append(template.IPAddresses, ipNet.IP)
			}
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return "", "", err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", "", err
	}
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		return "", "", err
	}
	return certFile, keyFile, nil
}
