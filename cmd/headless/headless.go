package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/niklasfasching/headless"
)

var code = flag.String("c", "", "code to run after files have been imported")
var windowArgs = flag.String("a", "", "window.args - split via strings.Fields")
var browserArgs = flag.String("b", "", "additional browser args")
var fs = flag.Bool("fs", false, "rw access to current directory")
var display = flag.Bool("d", false, "display ui")

func main() {
	log.SetFlags(0)
	flag.Parse()
	args := map[string]bool{}
	for _, a := range strings.Split(*browserArgs, " ") {
		args[a] = true
	}
	if *display {
		args["--headless"] = false
	}
	h, err := headless.Start(args)
	if err != nil {
		log.Fatal(err)
	}
	defer h.Stop()
	s, err := h.Open("about:blank")
	if err != nil {
		log.Fatal(err)
	}
	exitc, errc := make(chan int), make(chan error)
	s.Bind("console.log", func(args ...interface{}) { fmt.Fprintln(os.Stdout, headless.Colorize(args)) })
	s.Bind("console.error", func(args ...interface{}) { fmt.Fprintln(os.Stderr, headless.Colorize(args)) })
	s.Bind("window.close", func(code int) { exitc <- code })

	if *fs {
		s.Bind("writeFile", func(path, body string) error {
			path = filepath.Join(".", path)
			if err := os.MkdirAll(filepath.Dir(path), os.ModePerm); err != nil {
				return err
			}
			return os.WriteFile(path, []byte(body), 0644)
		})
		s.Bind("readFile", func(path string) (string, error) {
			bs, err := os.ReadFile(filepath.Join(".", path))
			return string(bs), err
		})
	}

	s.Handle("Runtime.exceptionThrown", func(m json.RawMessage) { errc <- fmt.Errorf(headless.FormatException(m)) })
	html := headless.TemplateHTML(*code, flag.Args(), strings.Fields(*windowArgs))
	if err := s.Open(html); err != nil {
		log.Fatal(err)
	}
	select {
	case err := <-errc:
		log.Fatal(err)
	case code := <-exitc:
		os.Exit(code)
	}
}
