package main

import (
	"database/sql"
	"encoding/json"
	htmltmpl "html/template"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"text/template"

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
	LoggedIn bool `json:"loggedIn"`
	IsHouse  bool `json:"isHouse"`
}

type PageData struct {
	AuthJSON htmltmpl.JS
	DataJSON htmltmpl.JS
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

func renderIndex(w http.ResponseWriter, r *http.Request, tmpl *htmltmpl.Template) {
	auth := getAuth(r)
	authBytes, _ := json.Marshal(auth)

	listBytes := []byte("null")
	if auth.LoggedIn {
		if data, err := os.ReadFile("oih.json"); err == nil {
			listBytes = data
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl.Execute(w, PageData{
		AuthJSON: htmltmpl.JS(authBytes),
		DataJSON: htmltmpl.JS(listBytes),
	})
}

func main() {
	if dsn := os.Getenv("MKLIST_DB_DSN"); dsn != "" {
		var err error
		db, err = sql.Open("mysql", dsn)
		if err != nil {
			panic(err)
		}
	}

	indexTmpl, err := htmltmpl.ParseFiles("index.html")
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

	fs := http.FileServer(http.Dir("."))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/", "/index.html":
			renderIndex(w, r, indexTmpl)
		case "/oih.json":
			http.NotFound(w, r)
		default:
			fs.ServeHTTP(w, r)
		}
	})

	if err := http.ListenAndServe(":80", nil); err != nil {
		panic(err)
	}
}

func (test *List) Create(target string) {
	tmpl, err := template.ParseFiles(target + ".tmpl")
	if err != nil {
		panic(err)
	}
	svgfile, err := os.Create(target + ".out.svg")
	if err != nil {
		panic(err)
	}
	if err = tmpl.Execute(svgfile, test); err != nil {
		panic(err)
	}
	if err = exec.Command("rsvg-convert", "-f", "pdf", "-o", target+".out.pdf", target+".out.svg").Run(); err != nil {
		panic(err)
	}
	if err = exec.Command("rsvg-convert", "-f", "png", "-o", target+".out.png", target+".out.svg").Run(); err != nil {
		panic(err)
	}
}
