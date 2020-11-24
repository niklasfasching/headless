package main

import (
	"log"
	"os"

	"github.com/niklasfasching/goheadless"
)

func main() {
	log.SetFlags(0)
	if len(os.Args) < 3 {
		log.Fatal("not enough arguments: headless [command] [scriptFile] [...args]")
	}
	address := "localhost:" + goheadless.GetFreePort()
	servePath, fileName := goheadless.SplitPath(os.Args[2])
	switch cmd := os.Args[1]; cmd {
	case "run":
		out, f := make(chan string), func() {}
		go func() {
			for msg := range out {
				log.Println(msg)
			}
			f()
		}()
		exitCode, err := goheadless.ServeAndRun(out, address, servePath, fileName, os.Args[3:])
		f = func() {
			if err != nil {
				log.Fatal(err)
			} else {
				os.Exit(exitCode)
			}
		}
	case "serve":
		log.Println("http://" + address + servePath)
		goheadless.Serve(address, servePath, fileName, os.Args[3:])

	default:
		log.Fatalf("unknown command: %s (run|serve)", cmd)
	}
}
