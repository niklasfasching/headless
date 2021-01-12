package main

import (
	"context"
	"flag"
	"log"
	"os"
	"strings"

	"github.com/niklasfasching/headless"
)

var code = flag.String("c", "", "code to run after files have been imported")
var args = flag.String("a", "", "window.args - split via strings.Fields")

func main() {
	log.SetFlags(0)
	flag.Parse()
	h := &headless.Runner{}
	if err := h.Start(); err != nil {
		log.Fatal(err)
	}
	defer h.Stop()
	html := headless.HTML(*code, flag.Args(), strings.Fields(*args))
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
