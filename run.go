package goheadless

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"
)

var htmlTemplate = `
<!doctype html>
<html lang=en>
  <head>
    <meta charset=utf-8>
    <script type=module>
    window.isHeadless = navigator.webdriver;
    window.baseUrl = '%s';
    window.args = %s;
    window.close = (code = 0) => isHeadless ? console.clear(code) : console.log('exit: ', code);

    for (const name of ['debug', 'info', 'error', 'warn', 'log']) {
      const f = console[name];
      console[name] = (...args) => f.call(console, args.map(arg => Object(arg) === arg ? JSON.stringify(arg) : arg?.toString()).join(' '));
    }
    </script>
    <script type=module>
    if (isHeadless) console.clear(-1); // notify start - import errors can't be caught - script isn't run at all
    import './%s';
    </script>;
  </head>
</html>
`

type exitCode int

func (e exitCode) Error() string { return strconv.Itoa(int(e)) }

func Serve(address, servePath, fileName string, args []string) error {
	fs := http.FileServer(http.Dir("./"))
	http.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == servePath {
			argsBytes, _ := json.Marshal(args)
			fmt.Fprintf(w, htmlTemplate, "http://"+address, string(argsBytes), fileName)
		} else {
			fs.ServeHTTP(w, r)
		}
	}))
	return http.ListenAndServe(address, nil)
}

func ServeAndRun(address, servePath, fileName string, args []string) {
	go func() {
		log.Fatal(Serve(address, servePath, fileName, args))
	}()

	b := &Browser{Executable: "chromium-browser", Port: GetFreePort()}
	if err := Run(b, "http://"+address+servePath); err != nil {
		switch x := err.(type) {
		case exitCode:
			os.Exit(int(x))
		default:
			log.Fatal(err)
		}
	}
}

func Run(b *Browser, url string) error {
	c := make(chan error)

	if err := b.Start(); err != nil {
		return err
	}
	defer b.Stop()

	p, err := b.OpenPage()
	if err != nil {
		return err
	}

	started := false
	p.Subscribe("Runtime", "consoleAPICalled", func(params interface{}) {
		m := params.(map[string]interface{})
		switch m["type"] {
		case "clear":
			if args := m["args"].([]interface{}); len(args) != 0 {
				code, ok := args[0].(map[string]interface{})["value"].(float64)
				if !ok {
					c <- fmt.Errorf("bad code: %v %v", args[0].(map[string]interface{})["value"], err)
				}
				if code == -1 {
					started = true
				} else {
					c <- exitCode(code)
				}
			}
		case "debug", "info", "error", "warn", "log":
			args := m["args"].([]interface{})
			msg := args[0].(map[string]interface{})["value"].(string)
			log.Println(msg)
		}
	})

	p.Subscribe("Runtime", "exceptionThrown", func(v interface{}) {
		bs, _ := json.MarshalIndent(v, "", "  ")
		c <- fmt.Errorf("unexpected error: %s", string(bs))
	})
	go func() {
		loaded, err := p.Await("Page", "frameStoppedLoading")
		if err != nil {
			c <- err
		}
		if err := p.Execute("Page.navigate", map[string]string{"url": url}, nil); err != nil {
			c <- err
		}
		<-loaded
		if !started {
			log.Printf("script did not call start(). check the console at %s", url)
			<-time.After(30 * time.Second)
			c <- fmt.Errorf("timeout: script did not call start() after 30s")
		}
		<-time.After(60 * time.Second)
		c <- fmt.Errorf("timeout: script did not call exit() after 60s")
	}()
	return <-c
}

func GetFreePort() string {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		panic(err)
	}
	defer l.Close()
	return strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
}
