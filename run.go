package goheadless

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
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
    window.close = (code = 0) => isHeadless ? console.clear(code) : console.log('exit:', code);
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
    </script>
    <script type=module>
    %s
    %s
    if (isHeadless) console.clear(-1); // notify start. import errors stop script from running at all
    </script>
  </head>
</html>`

type Event struct {
	Method string
	Args   []interface{}
}

type Runner struct {
	Address string
	Code    string
	Files   []string
	Args    []string

	Server *http.Server
}

type exceptionThrown struct {
	ExceptionDetails struct {
		Exception struct {
			Description string
		}
	}
}

type consoleAPICall struct {
	Args []struct {
		Type        string
		Subtype     string
		Description string
		Preview     struct {
			Properties []struct {
				Name  string
				Value string
			}
		}
		Value interface{}
	}
	Type string
}

func (r *Runner) Serve() {
	address, servePath := r.Address, "/"
	if parts := strings.SplitN(r.Address, "/", 2); len(parts) == 2 {
		address, servePath = parts[0], "/"+parts[1]
	}
	html := []byte(formatResponse(r.Code, r.Files, r.Args))
	fs := http.FileServer(http.Dir("./"))
	r.Server = &http.Server{Addr: address, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			w.Header().Set("Content-Type", "application/json")
			is, _ := ioutil.ReadDir(path.Join("./", r.URL.Path))
			files := []string{}
			for _, i := range is {
				files = append(files, i.Name())
			}
			json.NewEncoder(w).Encode(files)
		} else if r.URL.Path == servePath {
			w.Write(html)
		} else {
			fs.ServeHTTP(w, r)
		}
	})}
	go func() {
		if err := r.Server.ListenAndServe(); err != http.ErrServerClosed {
			panic(err)
		}
	}()
}

func (r *Runner) ServeAndRun(ctx context.Context, out chan Event) (int, error) {
	r.Serve()
	defer r.Server.Close()
	return r.Run(ctx, out, "http://"+r.Address)
}

func (r *Runner) Run(ctx context.Context, out chan Event, url string) (int, error) {
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

	c, started := make(chan interface{}), false
	p.Subscribe("Runtime", "consoleAPICalled", func(params consoleAPICall) {
		started = started || handleConsoleAPICall(params, out, c)
	})

	p.Subscribe("Runtime", "exceptionThrown", func(e exceptionThrown) {
		c <- fmt.Errorf("unexpected error: %v", e.ExceptionDetails.Exception.Description)
	})

	go func() {
		if err := p.Open(url); err != nil {
			c <- err
		}
		if !started {
			<-time.After(1 * time.Second)
			c <- fmt.Errorf("timeout: script did not call start() after 1s")
		}
		<-time.After(60 * time.Second)
		c <- fmt.Errorf("timeout: script did not call exit() after 60s")
	}()
	select {
	case err := <-c:
		switch err := err.(type) {
		case int:
			return err, nil
		default:
			return -1, err.(error)
		}
	case <-ctx.Done():
		return -1, nil
	}
}

func handleConsoleAPICall(c consoleAPICall, events chan Event, errors chan interface{}) bool {
	args := resolveArgs(c)
	switch method := c.Type; method {
	case "clear":
		if len(args) != 0 {
			code, ok := args[0].(float64)
			if !ok {
				errors <- fmt.Errorf("bad code: %v", args[0])
			} else if code == -1 {
				return true
			} else {
				time.Sleep(10 * time.Millisecond)
				errors <- int(code)
			}
		}
	default:
		events <- Event{method, args}
	}
	return false
}

func resolveArgs(c consoleAPICall) []interface{} {
	args := make([]interface{}, len(c.Args))
	for i, arg := range c.Args {
		switch t, st := arg.Type, arg.Subtype; {
		case t == "string", t == "number", t == "boolean", st == "null", t == "undefined":
			args[i] = arg.Value
		case t == "function", st == "regexp":
			args[i] = arg.Description
		default:
			properties := arg.Preview.Properties
			kvs := make([]string, len(properties))
			for i, p := range properties {
				if st == "array" {
					kvs[i] = p.Value
				} else {
					kvs[i] = p.Name + ": " + p.Value
				}
			}
			if st == "array" {
				args[i] = "[" + strings.Join(kvs, ",") + "]"
			} else {
				args[i] = "{" + strings.Join(kvs, ",") + "}"
			}
		}
	}
	return args
}

func formatResponse(code string, files, args []string) string {
	argsBytes, _ := json.Marshal(args)
	imports := make([]string, len(files))
	for i, f := range files {
		if !strings.HasPrefix(f, "./") && !strings.HasPrefix(f, "/") {
			f = "./" + f
		}
		imports[i] = fmt.Sprintf(`import "%s";`, f)
	}
	return fmt.Sprintf(htmlTemplate, string(argsBytes), code, strings.Join(imports, "\n"))
}

func GetFreePort() string {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		panic(err)
	}
	defer l.Close()
	return strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
}
