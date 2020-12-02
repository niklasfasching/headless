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
    <style>
      html, body, iframe { height: 100%%; width: 100%%; border: none; margin: 0; display: block; }
    </style>
    <script type=module>
    window.isHeadless = navigator.webdriver;
    window.args = %s;
    window.close = (code = 0) => isHeadless ? console.clear(code) : console.log('exit: ', code);
    window.onerror = (msg, src, line, col, err) => {
      console.log(err.stack);
      console.log("    at " + src + ":" + line + ":" + col);
      window.close();
      return true;
    };

    window.openIframe = (src) => {
      return new Promise((resolve, reject) => {
        const iframe = document.createElement("iframe");
        const onerror = reject;
        const onload = () => resolve(iframe);
        document.body.appendChild(Object.assign(iframe, {onload, onerror, src}));
      });
    };

    const f = console.log;
    console.log = (...args) => f.call(console, args.map(arg => Object(arg) === arg ? JSON.stringify(arg) : arg?.toString()).join(' '));
    </script>
    <script type=module>
    console.clear(-1); // notify start. import errors stop script from running at all
    import './%s';
    </script>
  </head>
</html>
`

type Event struct {
	Method string
	Args   []interface{}
}

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

func ServeAndRun(ctx context.Context, out chan Event, address, servePath, fileName string, args []string) (int, error) {
	s := Serve(address, servePath, fileName, args)
	defer s.Close()
	return Run(ctx, out, "http://"+address+servePath)
}

func Run(ctx context.Context, out chan Event, url string) (int, error) {
	b := &Browser{Port: GetFreePort()}
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
		args := m["args"].([]interface{})
		for i, arg := range args {
			args[i] = arg.(map[string]interface{})["value"]
		}
		switch method := m["type"].(string); method {
		case "clear":
			if len(args) != 0 {
				code, ok := args[0].(float64)
				if !ok {
					c <- fmt.Errorf("bad code: %v %v", args[0], err)
				} else if code == -1 {
					started = true
				} else {
					time.Sleep(10 * time.Millisecond)
					c <- exitCode(code)
				}
			}
		default:
			out <- Event{method, args}
		}
	})

	p.Subscribe("Runtime", "exceptionThrown", func(v interface{}) {
		e := v.(map[string]interface{})["exceptionDetails"].(map[string]interface{})["exception"].(map[string]interface{})
		c <- fmt.Errorf("unexpected error: %v", e["description"])
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
