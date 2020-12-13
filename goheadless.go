package goheadless

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

type Browser struct {
	Executable string
	Port       string
	cmd        *exec.Cmd
	stopped    bool
}

type Page struct {
	Browser   *Browser
	ID        string
	socket    *websocket.Conn
	err       error
	commandID int
	commands  map[int]chan *response
	events    map[string]chan *response
	domains   map[string]bool
	sync.RWMutex
}

type response struct {
	json json.RawMessage
	err  error
}

func (b *Browser) Stop() (*os.ProcessState, error) {
	if b.stopped {
		return nil, nil
	}
	b.stopped = true
	if err := b.cmd.Process.Kill(); err != nil {
		return nil, err
	}
	ps, err := b.cmd.Process.Wait()
	return ps, err
}

func (b *Browser) Start() error {
	args := []string{"--headless", "--disable-gpu", "--hide-scrollbars"}
	if b.Executable == "" {
		if executable := os.Getenv("GOHEADLESS_EXECUTABLE"); executable != "" {
			b.Executable = executable
		} else {
			b.Executable = "chromium-browser"
		}
	}
	if b.Port == "" {
		b.Port = "9000"
	}
	b.cmd = exec.Command(b.Executable, append(args, "--remote-debugging-port="+b.Port)...)
	if err := b.cmd.Start(); err != nil {
		return err
	}
	for i := 0; i < 1000; i++ {
		_, err := http.Get(fmt.Sprintf("http://localhost:%s/json", b.Port))
		if err == nil {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return errors.New("timeout (10s) waiting for browser to start")
}

func (b *Browser) OpenPage() (*Page, error) {
	res, err := http.Get(fmt.Sprintf("http://localhost:%s/json/new", b.Port))
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	bs, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	v := &struct{ Id string }{}
	if err := json.Unmarshal(bs, v); err != nil {
		return nil, fmt.Errorf("%s: %s", err, string(bs))
	}
	page := &Page{Browser: b, ID: v.Id}
	return page, page.Connect()
}

func (p *Page) Connect() (err error) {
	hostport := "localhost:" + p.Browser.Port
	p.socket, err = websocket.Dial(fmt.Sprintf("ws://%s/devtools/page/%s", hostport, p.ID), "", hostport)
	p.commands = map[int]chan *response{}
	p.events = map[string]chan *response{}
	p.domains = map[string]bool{}
	go p.receiveLoop()
	return err
}

func (p *Page) Close() error {
	return p.Execute("Page.close", nil, nil)
}

func (p *Page) receiveLoop() {
	for {
		r := &struct {
			Id     int
			Method string
			Params json.RawMessage
			Result json.RawMessage
			Error  *struct {
				Code    int
				Message string
				Data    string
			}
		}{}
		bs := []byte{}
		if err := websocket.Message.Receive(p.socket, &bs); err != nil {
			p.err = fmt.Errorf("websocket: %w", err)
			break
		}
		if err := json.Unmarshal(bs, r); err != nil {
			p.err = fmt.Errorf("json: (%s) %w", string(bs), err)
			break
		}
		if r.Method != "" {
			p.RLock()
			c, ok := p.events[r.Method]
			p.RUnlock()
			if !ok {
				// ignore unexpected events
			} else if r.Error != nil {
				err := fmt.Errorf("%d: %s - %s", r.Error.Code, r.Error.Message, r.Error.Data)
				c <- &response{json: r.Params, err: err}
			} else {
				c <- &response{json: r.Params}
			}
		} else {
			p.RLock()
			c, ok := p.commands[r.Id]
			p.RUnlock()
			if !ok {
				panic(fmt.Errorf("unexpected result: %s", string(bs)))
			} else if r.Error != nil {
				err := fmt.Errorf("%d: %s - %s", r.Error.Code, r.Error.Message, r.Error.Data)
				c <- &response{json: r.Result, err: err}
			} else {
				c <- &response{json: r.Result}
			}
		}
	}
	if !p.Browser.stopped {
		panic(fmt.Errorf("end of receive loop: %s", p.err))
	}
}

func (p *Page) Disconnect() error {
	return p.socket.Close()
}

func (p *Page) Subscribe(domain, event string, f func(interface{})) error {
	if !p.domains[domain] {
		p.domains[domain] = true
		if err := p.Execute(domain+".enable", nil, nil); err != nil {
			return err
		}
	}
	c := make(chan *response)
	p.Lock()
	p.events[domain+"."+event] = c
	p.Unlock()
	go func() {
		for r := range c {
			var x interface{}
			if r.err != nil {
				panic(fmt.Errorf("%s.%s: %w", domain, event, r.err))
			} else if err := json.Unmarshal(r.json, &x); err != nil {
				panic(fmt.Errorf("%s.%s: %w", domain, event, err))
			} else {
				f(x)
			}
		}
	}()
	return nil
}

func (p *Page) Unsubscribe(domain, event string) error {
	p.Lock()
	delete(p.events, domain+"."+event)
	hasSubscriptions := false
	for k := range p.events {
		if strings.HasPrefix(k, domain) {
			hasSubscriptions = true
		}
	}
	p.Unlock()
	if !hasSubscriptions {
		delete(p.domains, domain)
		if err := p.Execute(domain+".disable", nil, nil); err != nil {
			return err
		}
	}
	return nil
}

func (p *Page) Await(domain, event string) (chan interface{}, error) {
	c := make(chan interface{})
	err := p.Subscribe(domain, event, func(v interface{}) {
		p.Unsubscribe(domain, event)
		c <- v
	})
	return c, err
}

func (p *Page) Open(url string) error {
	loaded, err := p.Await("Page", "frameStoppedLoading")
	if err != nil {
		return err
	}
	if err := p.Execute("Page.navigate", map[string]string{"url": url}, nil); err != nil {
		return err
	}
	<-loaded
	return nil
}

func (p *Page) Execute(method string, params, result interface{}) error {
	if p.err != nil {
		return p.err
	}
	if strings.HasSuffix(method, ".enable") {
		p.domains[strings.TrimSuffix(method, ".enable")] = true
	}
	p.commandID += 1
	c, id := make(chan *response), p.commandID
	p.Lock()
	p.commands[id] = c
	p.Unlock()
	msg := map[string]interface{}{"id": id, "method": method, "params": params}
	if err := websocket.JSON.Send(p.socket, msg); err != nil {
		return err
	}
	r := <-c
	p.Lock()
	delete(p.commands, id)
	p.Unlock()
	if r.err != nil {
		return r.err
	}
	if result != nil {
		return json.Unmarshal(r.json, result)
	}
	return nil
}
