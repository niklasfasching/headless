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
	switch cmd := os.Args[1]; cmd {
	case "run":
		goheadless.ServeAndRun(address, os.Args[2], os.Args[3:])
	case "serve":
		log.Println("http://" + address + "/run")
		goheadless.Serve(address, os.Args[2], os.Args[3:])

	default:
		log.Fatalf("unknown command: %s (run|serve)", cmd)
	}
}
