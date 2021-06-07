package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/niklasfasching/headless"
)

var code = flag.String("c", "", "code to run after files have been imported")
var args = flag.String("a", "", "window.args - split via strings.Fields")
var browserArgs = flag.String("b", "", "additional browser args")
var display = flag.Bool("d", false, "display ui")
var fs = flag.Bool("fs", false, "allow http POST fs (create) access to working directory")

func main() {
	log.SetFlags(0)
	flag.Parse()
	h := &headless.H{}
	h.Browser.Args = append(headless.DefaultBrowserArgs, strings.Split(*browserArgs, " ")...)
	h.Browser.DisplayUI = *display
	h.POSTMux = http.DefaultServeMux
	if *fs {
		h.POSTMux.HandleFunc("/create", headless.CreateHandler)
	}
	if err := h.Start(); err != nil {
		log.Fatal(err)
	}
	defer h.Stop()
	html := headless.HTML("", headless.TemplateHTML(*code, flag.Args(), strings.Fields(*args)))
	r := h.Run(context.Background(), html)
	log.Println("Running on", r.URL)
	for m := range r.Messages {
		if m.Method == "clear" && len(m.Args) == 1 {
			exitCode, ok := m.Args[0].(float64)
			if !ok {
				os.Exit(-1)
			}
			os.Exit(int(exitCode))
		} else if m.Method == "info" {
			log.Println(headless.Colorize(m))
		} else {
			log.Println(append([]interface{}{m.Method}, m.Args...)...)
		}
	}
}
