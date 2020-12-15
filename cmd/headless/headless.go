package main

import (
	"flag"
	"log"
	"os"
	"strings"

	"github.com/niklasfasching/goheadless"
)

var address = flag.String("l", "localhost:"+goheadless.GetFreePort(), "listen address")
var windowArgs = flag.String("a", "", "window.args - split via strings.Fields")
var run = flag.Bool("r", false, "run served script")
var code = flag.String("c", "", "code snippet to run")

func main() {
	log.SetFlags(0)
	flag.Parse()
	if strings.HasPrefix(*address, ":") {
		*address = "0.0.0.0" + *address
	}
	html := goheadless.HTML(*code, flag.Args(), strings.Fields(*windowArgs))
	if *run {
		events, f := goheadless.ServeAndRun(*address, html)
		for event := range events {
			if event.Method == "info" {
				log.Println(goheadless.Colorize(event))
			} else if l := len(event.Args); l == 0 {
				continue
			} else if arg1, ok := event.Args[0].(string); ok && l == 1 {
				log.Println(arg1)
			} else {
				log.Printf("%s %v", event.Method, event.Args)
			}
		}
		if exitCode, err := f(); err != nil {
			log.Fatal(err)
		} else {
			os.Exit(exitCode)
		}
	} else {
		log.Println("http://" + *address)
		goheadless.Serve(*address, html)
		select {}
	}
}
