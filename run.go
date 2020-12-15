package goheadless

import (
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
    window.hasImported = true;
    </script>
    <script type=module>
    if (!window.hasImported) throw new Error('bad imports');
    %s
    </script>
  </head>
</html>`

type Event struct {
	Method string
	Args   []interface{}
}

type exceptionThrown struct {
	ExceptionDetails struct {
		LineNumber   int
		ColumnNumber int
		Url          string
		Exception    struct {
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

func Serve(address, html string) *http.Server {
	servePath := "/"
	if parts := strings.SplitN(address, "/", 2); len(parts) == 2 {
		address, servePath = parts[0], "/"+parts[1]
	}
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
			w.Write([]byte(html))
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

func ServeAndRun(address, html string) (chan Event, func() (int, error)) {
	s := Serve(address, html)
	defer s.Close()
	return Run("http://" + address)
}

func OpenPage() (*Page, chan interface{}, func(), error) {
	b := &Browser{Port: GetFreePort()}
	if err := b.Start(); err != nil {
		return nil, nil, nil, err
	}
	p, err := b.OpenPage()
	if err != nil {
		return nil, nil, nil, err
	}
	c := make(chan interface{})
	p.Subscribe("Runtime", "consoleAPICalled", func(call consoleAPICall) {
		args := resolveArgs(call)
		switch method := call.Type; method {
		case "clear":
			if len(args) == 1 {
				if code, ok := args[0].(float64); !ok {
					c <- fmt.Errorf("bad code: %v", args[0])
				} else {
					time.Sleep(10 * time.Millisecond)
					c <- int(code)
				}
			}
		default:
			c <- Event{method, args}
		}
	})
	p.Subscribe("Runtime", "exceptionThrown", func(e exceptionThrown) {
		c <- fmt.Errorf("%s\n    at %s:%d:%d", e.ExceptionDetails.Exception.Description,
			e.ExceptionDetails.Url, e.ExceptionDetails.LineNumber, e.ExceptionDetails.ColumnNumber)
	})
	return p, c, func() {
		b.Stop()
		close(c)
	}, nil
}

func Run(url string) (chan Event, func() (int, error)) {
	events, exit := make(chan Event), make(chan interface{})
	p, c, stop, err := OpenPage()
	if err != nil {
		close(events)
		return events, func() (int, error) { return -1, err }
	}
	go func() {
		for x := range c {
			switch x := x.(type) {
			case Event:
				events <- x
			default:
				close(events)
				exit <- x
			}
		}
	}()
	if err := p.Open(url); err != nil {
		close(events)
		return events, func() (int, error) { return -1, err }
	}
	return events, func() (int, error) {
		stop()
		switch e := (<-exit).(type) {
		case int:
			return e, nil
		default:
			return -1, e.(error)
		}
	}
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

func HTML(code string, files, args []string) string {
	argsBytes, _ := json.Marshal(args)
	imports := make([]string, len(files))
	for i, f := range files {
		if !strings.HasPrefix(f, "./") && !strings.HasPrefix(f, "/") {
			f = "./" + f
		}
		imports[i] = fmt.Sprintf(`import "%s";`, f)
	}
	return fmt.Sprintf(htmlTemplate, string(argsBytes), strings.Join(imports, "\n"), code)
}

func GetFreePort() string {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		panic(err)
	}
	defer l.Close()
	return strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
}
