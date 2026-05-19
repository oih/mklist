package main

import (
	"database/sql"
	"encoding/json"
	"html/template"
	"io"
	"net/http"
	"os"
	"sync"

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

var m map[string]*sync.Mutex = make(map[string]*sync.Mutex)
var db *sql.DB

func getAuth(r *http.Request) AuthInfo {
	cookie, err := r.Cookie("session")
	if err != nil || db == nil {
		return AuthInfo{}
	}
	var level int
	err = db.QueryRow(
		"SELECT u.level FROM sessions s JOIN users u ON s.uid = u.id WHERE s.id = ? AND u.lastseen + INTERVAL 90 DAY > NOW()",
		cookie.Value,
	).Scan(&level)
	if err != nil {
		return AuthInfo{}
	}
	return AuthInfo{LoggedIn: true, IsHouse: level <= 2}
}

func renderIndex(w http.ResponseWriter, r *http.Request, tmpl *template.Template) {
	auth := getAuth(r)
	var list *List
	if auth.LoggedIn {
		if data, err := os.ReadFile("oih.json"); err == nil {
			list = new(List)
			if err := json.Unmarshal(data, list); err != nil {
				list = nil
			}
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl.Execute(w, PageData{Auth: auth, Data: list})
}

func main() {
	if dsn := os.Getenv("MKLIST_DB_DSN"); dsn != "" {
		var err error
		db, err = sql.Open("mysql", dsn)
		if err != nil {
			panic(err)
		}
	}

	indexTmpl, err := template.ParseFiles("index.html")
	if err != nil {
		panic(err)
	}

	target := "oih"
	if m[target] == nil {
		m[target] = new(sync.Mutex)
	}

	http.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			return
		}
		if !getAuth(r).IsHouse {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			io.WriteString(w, err.Error())
			return
		}
		list := new(List)
		if err = json.Unmarshal(body, list); err != nil {
			io.WriteString(w, err.Error())
			return
		}
		m[target].Lock()
		defer m[target].Unlock()
		if err = os.WriteFile(target+".json", body, 0644); err != nil {
			io.WriteString(w, err.Error())
			panic(err)
		}
		io.WriteString(w, "Liste kann jetzt heruntergeladen werden!")
	})

	fs := http.FileServer(http.Dir("public"))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
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
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		panic(err)
	}
}
