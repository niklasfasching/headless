package goheadless

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
)

var htmlTemplate = `
<!doctype html>
<html lang=en>
  <head>
    <meta charset=utf-8>
    <script type=module>
    window.isHeadless = navigator.webdriver;
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

func Serve(address, servePath, fileName string, args []string) *http.Server {
	fs := http.FileServer(http.Dir("./"))
	s := &http.Server{Addr: address, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			w.Header().Set("Content-Type", "application/json")
			is, _ := ioutil.ReadDir(path.Join("./", r.URL.Path))
			files := []string{}
			for _, i := range is {
				files = append(files, i.Name())
			}
			json.NewEncoder(w).Encode(files)
		} else if r.URL.Path == servePath {
			argsBytes, _ := json.Marshal(args)
			fmt.Fprintf(w, htmlTemplate, string(argsBytes), fileName)
		} else {
			fs.ServeHTTP(w, r)
		}
	})}
	go func() {
		if err := s.ListenAndServe(); err != http.ErrServerClosed {
			panic(err)
		}
	}()
	return s
}

func ServeAndRun(ctx context.Context, out chan string, address, servePath, fileName string, args []string) (int, error) {
	s := Serve(address, servePath, fileName, args)
	defer s.Close()
	return Run(ctx, out, "http://"+address+servePath)
}

func Run(ctx context.Context, out chan string, url string) (int, error) {
	b := &Browser{Executable: "chromium-browser", Port: GetFreePort()}
	defer func() { close(out) }()
	if err := b.Start(); err != nil {
		return -1, err
	}
	defer b.Stop()
	p, err := b.OpenPage()
	if err != nil {
		return -1, err
	}

	c, started := make(chan error), false
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
			out <- args[0].(map[string]interface{})["value"].(string)
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
	select {
	case err := <-c:
		switch err := err.(type) {
		case exitCode:
			return int(err), nil
		default:
			return -1, err
		}
	case <-ctx.Done():
		return -1, nil
	}
}

func GetFreePort() string {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		panic(err)
	}
	defer l.Close()
	return strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
}

func SplitPath(p string) (servePath string, fileName string) {
	p = "/" + p
	directory := path.Clean(path.Dir(p))
	if !strings.HasSuffix(directory, "/") {
		directory += "/"
	}
	return directory + "index.html", path.Base(path.Clean(p))
}
