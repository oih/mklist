package main

import (
	"database/sql"
	"encoding/json"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

type Group struct {
	Name    string
	Mail    string
	Members []string
}

type List struct {
	Date string
	Cols [][]Group
}

type AuthInfo struct {
	LoggedIn bool
	IsHouse  bool
}

type PageData struct {
	Auth AuthInfo
	Data *List
}

var (
	m  map[string]*sync.Mutex = make(map[string]*sync.Mutex)
	db *sql.DB
)

// loggingMiddleware logs details about every incoming request.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"remote", r.RemoteAddr,
			"duration", time.Since(start),
		)
	})
}

func getAuth(r *http.Request) AuthInfo {
	cookie, err := r.Cookie("session")
	if err != nil {
		return AuthInfo{}
	}

	if db == nil {
		slog.Warn("auth attempt but database is not connected")
		return AuthInfo{}
	}

	var level int
	err = db.QueryRow(
		"SELECT u.level FROM sessions s JOIN users u ON s.uid = u.id WHERE s.id = ? AND u.lastseen + INTERVAL 90 DAY > NOW()",
		cookie.Value,
	).Scan(&level)

	if err != nil {
		if err != sql.ErrNoRows {
			slog.Error("database error during auth", "error", err)
		}
		return AuthInfo{}
	}

	return AuthInfo{LoggedIn: true, IsHouse: level <= 2}
}

func renderIndex(w http.ResponseWriter, r *http.Request, tmpl *template.Template) {
	auth := getAuth(r)
	var list *List

	if auth.LoggedIn {
		data, err := os.ReadFile("oih.json")
		if err == nil {
			list = new(List)
			if err := json.Unmarshal(data, list); err != nil {
				slog.Error("failed to unmarshal oih.json", "error", err)
				list = nil
			}
		} else if !os.IsNotExist(err) {
			slog.Error("failed to read oih.json", "error", err)
		}
	}

	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, PageData{Auth: auth, Data: list}); err != nil {
		slog.Error("template execution failed", "error", err)
	}
}

func main() {
	// Setup structured logging
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if dsn := os.Getenv("MKLIST_DB_DSN"); dsn != "" {
		var err error
		db, err = sql.Open("mysql", dsn)
		if err != nil {
			slog.Error("failed to open database", "error", err)
			os.Exit(1)
		}
		// Check connection
		if err := db.Ping(); err != nil {
			slog.Warn("database ping failed, auth will not work", "error", err)
		} else {
			slog.Info("database connected successfully")
		}
	} else {
		slog.Warn("MKLIST_DB_DSN not set, running in local/no-auth mode")
	}

	indexTmpl, err := template.ParseFiles("index.html")
	if err != nil {
		slog.Error("failed to parse index.html template", "error", err)
		os.Exit(1)
	}

	target := "oih"
	if m[target] == nil {
		m[target] = new(sync.Mutex)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		auth := getAuth(r)
		if !auth.IsHouse {
			slog.Warn("unauthorized API access attempt", "remote", r.RemoteAddr)
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			slog.Error("failed to read request body", "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		list := new(List)
		if err = json.Unmarshal(body, list); err != nil {
			slog.Error("invalid JSON received", "error", err)
			http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}

		m[target].Lock()
		defer m[target].Unlock()

		if err = os.WriteFile(target+".json", body, 0644); err != nil {
			slog.Error("failed to save JSON file", "file", target+".json", "error", err)
			http.Error(w, "Internal server error saving data", http.StatusInternalServerError)
			return
		}

		slog.Info("list updated successfully", "user_agent", r.UserAgent())
		io.WriteString(w, "Liste wurde erfolgreich gespeichert!")
	})

	fs := http.FileServer(http.Dir("public"))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			renderIndex(w, r, indexTmpl)
			return
		}
		fs.ServeHTTP(w, r)
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	slog.Info("starting server", "port", port)
	server := &http.Server{
		Addr:         ":" + port,
		Handler:      loggingMiddleware(mux),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}
