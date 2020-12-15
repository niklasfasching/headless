package goheadless

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"
)

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

type pausedRequest struct {
	RequestId string
	Request   struct {
	}
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
	if strings.HasPrefix(address, ":") {
		address = "localhost" + address
	}
	s := Serve(address, html)
	defer s.Close()
	return Run("http://" + address)
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

func RunOn(domain, html string) (chan Event, func() (int, error)) {
	url := fmt.Sprintf("http://%s/goheadless.html", domain)
	events, exit := make(chan Event), make(chan interface{})
	if !regexp.MustCompile(`^\w+\.\w+$`).MatchString(domain) {
		close(events)
		return events, func() (int, error) { return -1, fmt.Errorf("invalid domain: %s", domain) }
	}
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
			case error:
				events <- Event{"exception", []interface{}{x.Error()}}
			default:
				close(events)
				exit <- x
			}
		}
	}()
	params := map[string]interface{}{"patterns": []interface{}{map[string]string{"urlPattern": url}}}
	if err := p.Execute("Fetch.enable", params, nil); err != nil {
		close(events)
		return events, func() (int, error) { return -1, err }
	}
	p.Subscribe("Fetch", "requestPaused", func(pr pausedRequest) {
		if err := p.Execute("Fetch.fulfillRequest", map[string]interface{}{
			"requestId":    pr.RequestId,
			"responseCode": 200,
			"body":         base64.StdEncoding.EncodeToString([]byte(html)),
		}, nil); err != nil {
			exit <- err
		}
	})
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
		args := resolveConsoleArgs(call)
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
