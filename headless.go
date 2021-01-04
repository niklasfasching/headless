package goheadless

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "embed"

	"golang.org/x/net/websocket"
)

type Runner struct {
	Port    int
	Browser Browser

	server    http.Server
	runs      sync.Map
	ws        *websocket.Conn
	connected chan struct{}
	id        int
}

type Browser struct {
	Executable string
	Port       int
	Args       []string

	websocketURL string
	cmd          *exec.Cmd
}

type Message struct {
	Method string
	Args   []interface{}
}

type run struct {
	html     string
	messages chan Message
	ws       *websocket.Conn
}

func (b *Browser) Start(url string) error {
	if b.Port == 0 {
		b.Port = GetFreePort()
	}
	if b.Args == nil {
		b.Args = []string{
			"--headless",
			"--temp-profile",
			"--hide-scrollbars",
			"--autoplay-policy=no-user-gesture-required",
		}
	}
	if b.Executable == "" {
		if executable := os.Getenv("GOHEADLESS_EXECUTABLE"); executable != "" {
			b.Executable = executable
		} else {
			b.Executable = "chromium-browser"
		}
	}
	b.cmd = exec.Command(b.Executable, append(b.Args, fmt.Sprintf("--remote-debugging-port=%d", b.Port), url)...)
	b.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := b.cmd.Start(); err != nil {
		return err
	}
	for i := 0; i < 1000; i++ {
		if r, err := http.Get(fmt.Sprintf("http://localhost:%d/json/version", b.Port)); err == nil {
			defer r.Body.Close()
			m := map[string]string{}
			if err := json.NewDecoder(r.Body).Decode(&m); err == nil {
				b.websocketURL = m["webSocketDebuggerUrl"]
				return nil
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return errors.New("timeout (10s) waiting for browser to start")
}

func (b *Browser) Stop() error {
	cmd := b.cmd
	b.cmd = nil
	if err := cmd.Process.Kill(); err != nil {
		return err
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		return err
	}
	_, err := cmd.Process.Wait()
	return err
}

func (h *Runner) Start() error {
	if h.Port == 0 {
		h.Port = GetFreePort()
	}
	address := "localhost:" + strconv.Itoa(h.Port)
	h.connected = make(chan struct{})
	h.server = http.Server{Handler: http.HandlerFunc(h.serveHTTP)}
	l, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	go h.server.Serve(l)
	if err := h.Browser.Start("http://" + address + "/_main"); err != nil {
		return err
	}
	<-h.connected
	return nil
}

func (h *Runner) Stop() error {
	if err := h.Browser.Stop(); err != nil {
		log.Fatal(err)
		return err
	}
	return h.server.Close()
}

func (h *Runner) Run(ctx context.Context, html string) chan Message {
	h.id++
	path, r := fmt.Sprintf("/_run_%d", h.id), &run{html: html, messages: make(chan Message)}
	h.runs.Store(path, r)
	websocket.JSON.Send(h.ws, map[string]interface{}{"method": "open", "path": path, "isRun": true})
	go func() {
		<-ctx.Done()
		websocket.JSON.Send(h.ws, map[string]interface{}{"method": "close", "path": path, "isRun": true})
	}()
	return r.messages
}

func (h *Runner) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Upgrade") == "websocket" {
		websocket.Handler(h.handleWebsocket).ServeHTTP(w, r)
	} else if r.Method == "POST" {
		w.Header().Set("Content-Type", "application/json")
		is, _ := ioutil.ReadDir(path.Join("./", r.URL.Path))
		files := []string{}
		for _, i := range is {
			files = append(files, i.Name())
		}
		json.NewEncoder(w).Encode(files)
	} else if r.URL.Path == "/_main" {
		w.Write([]byte(runHTML))
	} else if strings.HasPrefix(r.URL.Path, "/_run_") {
		r, _ := h.runs.Load(r.URL.Path)
		w.Write([]byte(r.(*run).html))
	} else {
		http.FileServer(http.Dir("./")).ServeHTTP(w, r)
	}
}

func (h *Runner) handleWebsocket(ws *websocket.Conn) {
	if !strings.HasPrefix(ws.Config().Origin.Host, "localhost:") {
		return
	}
	websocket.JSON.Send(ws, map[string]interface{}{"method": "connect", "url": h.Browser.websocketURL})
	key := ws.Config().Location.Path
	if key == "/_main" {
		h.ws = ws
	} else {
		v, _ := h.runs.Load(key)
		v.(*run).ws = ws
	}
	for {
		m := struct {
			Key    string
			Method string
			Args   []interface{}
		}{}
		if err := websocket.JSON.Receive(ws, &m); err != nil {
			if _, ok := h.runs.Load(key); h.Browser.cmd == nil || (key != "/_main" && !ok) {
				return
			}
			panic(fmt.Sprintf("%s: %s", key, err))
		}
		switch m.Method {
		case "connect":
			select {
			case <-h.connected:
			default:
				close(h.connected)
			}
		case "close":
			r, _ := h.runs.LoadAndDelete(m.Key)
			close(r.(*run).messages)
			r.(*run).ws.Close()
		default:
			r, _ := h.runs.Load(m.Key)
			r.(*run).messages <- Message{m.Method, m.Args}
		}
	}
}
