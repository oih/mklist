package main

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"os/exec"
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

func main() {
	target := "oih"

	// JSON-Datei einlesen
	jsonfile, err := ioutil.ReadFile(target + ".json")
	if err != nil {
		panic(err)
	}

	// JSON parsen
	var test List
	err = json.Unmarshal(jsonfile, &test)
	if err != nil {
		panic(err)
	}

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
