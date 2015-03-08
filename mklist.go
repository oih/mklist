package main

import (
    "encoding/json"
    "io"
    "io/ioutil"
    "net/http"
    "os"
    "os/exec"
    "sync"
    "text/template"
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

var m map[string]*sync.Mutex = make(map[string]*sync.Mutex)

func main() {
    target := "oih"
    // important:
    if m[target] == nil {
        m[target] = new(sync.Mutex)
    }
    http.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
        if r.Method == "POST" {
            body, err := ioutil.ReadAll(r.Body)
            if err != nil {
                io.WriteString(w, err.Error())
                return
            }

            // JSON parsen
            list := new(List)
            err = json.Unmarshal(body, &list)
            if err != nil {
                io.WriteString(w, err.Error())
                return
            }

            m[target].Lock()
            defer m[target].Unlock()
            err = ioutil.WriteFile(target + ".json", body, 0644)
            if err != nil {
                io.WriteString(w, err.Error())
                panic(err)
            }
            //list.Create(target)
            io.WriteString(w, "Liste kann jetzt heruntergeladen werden!")
        }
    })
    http.Handle("/", http.FileServer(http.Dir(".")))
    err := http.ListenAndServe(":80", nil)
    if err != nil {
        panic(err)
    }
}

func (test *List) Create(target string) {
    // Template parsen
    tmpl, err := template.ParseFiles(target + ".tmpl")
    if err != nil {
        panic(err)
    }

    // Output-Datei (svg) anlegen
    svgfile, err := os.Create(target + ".out.svg")
    if err != nil {
        panic(err)
    }

    // svg schreiben
    err = tmpl.Execute(svgfile, test)
    if err != nil {
        panic(err)
    }

    // pdf erzeugen
    err = exec.Command("rsvg-convert", "-f", "pdf", "-o", target+".out.pdf", target+".out.svg").Run()
    if err != nil {
        panic(err)
    }

    // png erzeugen
    err = exec.Command("rsvg-convert", "-f", "png", "-o", target+".out.png", target+".out.svg").Run()
    if err != nil {
        panic(err)
    }
}
