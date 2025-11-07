package main

import (
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
)

//go:embed web/*
var content embed.FS

var (
	indexOnce sync.Once
	indexTmpl *template.Template
	tmplErr   error
)

type server struct {
	settingsMu sync.RWMutex
	settings   Settings

	tokenMu    sync.RWMutex
	adminToken string
}

type Settings struct {
	AppName         string `json:"appName"`
	RefreshInterval int    `json:"refreshInterval"`
}

const (
	settingsFile = "settings.json"
	tokenFile    = "admin.token"
)

func main() {
	srv := &server{}
	if err := srv.init(); err != nil {
		log.Fatalf("failed to start: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.handleIndex)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(getStaticFS())))
	mux.HandleFunc("/api/settings", srv.handleSettings)

	addr, ln, err := listenLoopback()
	if err != nil {
		log.Fatalf("listen error: %v", err)
	}
	defer ln.Close()

	log.Printf("admin token: %s", srv.currentAdminToken())
	log.Printf("serving UI at http://%s", addr)

	if err := http.Serve(ln, csrfMiddleware(srv.requireAdminToken(mux))); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server error: %v", err)
	}
}

func (s *server) init() error {
	if err := s.loadSettings(); err != nil {
		return err
	}
	if err := s.ensureAdminToken(); err != nil {
		return err
	}
	return nil
}

func (s *server) loadSettings() error {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()

	data, err := os.ReadFile(settingsFile)
	if errors.Is(err, os.ErrNotExist) {
		s.settings = Settings{AppName: "Research App", RefreshInterval: 30}
		return s.saveSettingsLocked()
	}
	if err != nil {
		return fmt.Errorf("read settings: %w", err)
	}
	if err := json.Unmarshal(data, &s.settings); err != nil {
		return fmt.Errorf("parse settings: %w", err)
	}
	return nil
}

func (s *server) saveSettingsLocked() error {
	data, err := json.MarshalIndent(s.settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}
	tmp := settingsFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write temp: %w", err)
	}
	if err := os.Rename(tmp, settingsFile); err != nil {
		return fmt.Errorf("replace settings: %w", err)
	}
	return nil
}

func (s *server) ensureAdminToken() error {
	s.tokenMu.Lock()
	defer s.tokenMu.Unlock()

	data, err := os.ReadFile(tokenFile)
	if errors.Is(err, os.ErrNotExist) {
		token, genErr := generateToken()
		if genErr != nil {
			return genErr
		}
		if writeErr := os.WriteFile(tokenFile, []byte(token), 0o600); writeErr != nil {
			return fmt.Errorf("write token: %w", writeErr)
		}
		s.adminToken = token
		return nil
	}
	if err != nil {
		return fmt.Errorf("read token: %w", err)
	}
	s.adminToken = string(data)
	return nil
}

func (s *server) currentAdminToken() string {
	s.tokenMu.RLock()
	defer s.tokenMu.RUnlock()
	return s.adminToken
}

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	csrf := issueCSRFCookie(w, r)

	tmpl, err := parseIndexTemplate()
	if err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}

	data := struct {
		CSRFToken string
	}{CSRFToken: csrf}

	if err := tmpl.Execute(w, data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}
}

func parseIndexTemplate() (*template.Template, error) {
	indexOnce.Do(func() {
		var err error
		indexTmpl, err = template.ParseFS(content, "web/index.html")
		tmplErr = err
	})
	if tmplErr != nil {
		return nil, tmplErr
	}
	return indexTmpl, nil
}

func getStaticFS() http.FileSystem {
	f, err := fs.Sub(content, "web")
	if err != nil {
		panic(err)
	}
	return http.FS(f)
}

func (s *server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.settingsMu.RLock()
		defer s.settingsMu.RUnlock()
		respondJSON(w, s.settings)
	case http.MethodPost:
		var payload Settings
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if err := validateSettings(payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.settingsMu.Lock()
		s.settings = payload
		if err := s.saveSettingsLocked(); err != nil {
			s.settingsMu.Unlock()
			http.Error(w, "save error", http.StatusInternalServerError)
			return
		}
		s.settingsMu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func validateSettings(s Settings) error {
	if s.AppName == "" {
		return errors.New("appName is required")
	}
	if s.RefreshInterval <= 0 {
		return errors.New("refreshInterval must be positive")
	}
	return nil
}

func respondJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("json encode error: %v", err)
	}
}

func (s *server) requireAdminToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		token := r.Header.Get("X-Admin-Token")
		if token == "" {
			http.Error(w, "missing admin token", http.StatusUnauthorized)
			return
		}
		if token != s.currentAdminToken() {
			http.Error(w, "invalid admin token", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func csrfMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		cookie, err := r.Cookie("csrf_token")
		if err != nil || cookie.Value == "" {
			http.Error(w, "missing csrf token", http.StatusUnauthorized)
			return
		}
		header := r.Header.Get("X-CSRF-Token")
		if header == "" || header != cookie.Value {
			http.Error(w, "csrf token mismatch", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func issueCSRFCookie(w http.ResponseWriter, r *http.Request) string {
	if cookie, err := r.Cookie("csrf_token"); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	token, err := generateToken()
	if err != nil {
		log.Printf("csrf token error: %v", err)
		return ""
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "csrf_token",
		Value:    token,
		Path:     "/",
		SameSite: http.SameSiteStrictMode,
	})
	return token
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("token generation: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func listenLoopback() (string, net.Listener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	return ln.Addr().String(), ln, nil
}
