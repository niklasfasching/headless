package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/niklasfasching/goheadless"
)

var htmlTemplate = `
<!doctype html>
<html lang=en>
  <head>
    <meta charset=utf-8>
    <script type=module>
    console.clear(-1); // notify start - import errors can't be caught - script isn't run at all

    window.baseUrl = '%s';
    window.args = %s;
    window.close = (code = 0) => console.clear(code);

    for (const name of ['debug', 'info', 'error', 'warn', 'log']) {
      const f = console[name];
      console[name] = (...args) => f.call(console, args.map(arg => Object(arg) === arg ? JSON.stringify(arg) : arg?.toString()).join(' '));
    }

    import '%s';
    </script>
  </head>
</html>
`

func main() {
	if len(os.Args) == 1 {
		log.Fatal("not enough arguments: headless [scriptFile] [...args]")
	}
	argsBytes, _ := json.Marshal(os.Args[2:])
	scriptFile := os.Args[1]
	if !strings.HasPrefix(scriptFile, "./") {
		scriptFile = "./" + scriptFile
	}

	b := &goheadless.Browser{
		Executable: "chromium-browser",
		Port:       getFreePort(),
	}

	if err := b.Start(); err != nil {
		log.Fatal(err)
	}
	exit := func(code int, args ...interface{}) {
		b.Stop()
		if len(args) != 0 {
			if fmt, ok := args[1].(string); ok {
				log.Printf(fmt, args[1:]...)
			} else {
				log.Print(args...)
			}

		}
		os.Exit(code)
	}
	signals := make(chan os.Signal)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signals
		exit(1)
	}()

	p, err := b.OpenPage()
	if err != nil {
		exit(1, err)
	}

	started := false
	p.Subscribe("Runtime", "consoleAPICalled", func(params interface{}) {
		m := params.(map[string]interface{})
		switch m["type"] {
		case "clear":
			if args := m["args"].([]interface{}); len(args) != 0 {
				code, ok := args[0].(map[string]interface{})["value"].(float64)
				if !ok {
					exit(1, "error: bad code", args[0].(map[string]interface{})["value"], err)
				}
				if code == -1 {
					started = true
				} else {
					exit(int(code))
				}
			}
		case "debug", "info", "error", "warn", "log":
			args := m["args"].([]interface{})
			msg, ok := args[0].(map[string]interface{})["value"].(string)
			if !ok {
				log.Println("error: bad log", m)
			}
			log.Println(msg)
		}
	})

	p.Subscribe("Runtime", "exceptionThrown", func(v interface{}) {
		bs, _ := json.MarshalIndent(v, "", "  ")
		log.Print("Unexpected error: ", string(bs))
	})

	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		exit(1, err)
	}
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	baseURL := "http://localhost:" + port

	go func() {
		url := baseURL + "/run"
		log.Println(url)
		if err := p.Execute("Page.navigate", map[string]string{"url": url}, nil); err != nil {
			exit(1, err)
		}
		<-time.After(1 * time.Second)
		if !started {
			log.Printf("timeout - script did not call start() after 1s - check the console at %s", url)
			<-time.After(30 * time.Second)
			exit(1)
		}
		<-time.After(60 * time.Second)
		exit(1, "timeout - script did not call exit() after 60s")
	}()

	http.Handle("/run", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, htmlTemplate, baseURL, string(argsBytes), scriptFile)
	}))
	http.Handle("/", http.FileServer(http.Dir("./")))
	http.Serve(listener, nil)
}

func getFreePort() string {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		panic(err)
	}
	defer l.Close()
	return strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
}
