package main

import (
	"log"
	"os"
	"path"

	"github.com/niklasfasching/goheadless"
)

func main() {
	log.SetFlags(0)
	if len(os.Args) < 3 {
		log.Fatal("not enough arguments: headless [command] [scriptFile] [...args]")
	}
	address := "localhost:" + goheadless.GetFreePort()
	servePath, fileName := "/"+path.Dir(path.Clean(os.Args[2]))+"/index.html", path.Base(path.Clean(os.Args[2]))
	switch cmd := os.Args[1]; cmd {
	case "run":
		goheadless.ServeAndRun(address, servePath, fileName, os.Args[3:])
	case "serve":
		log.Println("http://" + address + servePath)
		goheadless.Serve(address, servePath, fileName, os.Args[3:])

	default:
		log.Fatalf("unknown command: %s (run|serve)", cmd)
	}
}
