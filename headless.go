package headless

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "embed"

	"golang.org/x/net/websocket"
)

type H struct {
	Port    int
	Browser Browser
	POSTMux *http.ServeMux

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
	id     int
}

type Run struct {
	URL      string
	HTML     string
	Messages chan Message
	WS       *websocket.Conn
}

var DefaultBrowserArgs = []string{
	"--headless",
	"--temp-profile",
	"--hide-scrollbars",
	"--autoplay-policy=no-user-gesture-required",
}

func (b *Browser) Start(url string) error {
	if b.Port == 0 {
		b.Port = GetFreePort()
	}
	if b.Args == nil {
		b.Args = DefaultBrowserArgs
	}
	if b.Executable == "" {
		if executable := os.Getenv("HEADLESS_EXECUTABLE"); executable != "" {
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

	s := make(chan os.Signal)
	signal.Notify(s, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-s
		b.Stop()
		os.Exit(1)
	}()

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

func (h *H) Start() error {
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
	if err := h.Browser.Start("http://" + address + "/_headless"); err != nil {
		return err
	}
	<-h.connected
	return nil
}

func (h *H) Stop() error {
	if err := h.Browser.Stop(); err != nil {
		log.Fatal(err)
		return err
	}
	return h.server.Close()
}

func (h *H) Run(ctx context.Context, html string) *Run {
	h.id++
	r := &Run{
		URL:      fmt.Sprintf("http://localhost:%d/_headless_run_%d", h.Port, h.id),
		HTML:     html,
		Messages: make(chan Message),
	}
	h.runs.Store(r.URL, r)
	websocket.JSON.Send(h.ws, map[string]interface{}{
		"method": "open",
		"params": map[string]interface{}{"url": r.URL},
	})
	go func() {
		<-ctx.Done()
		websocket.JSON.Send(r.WS, map[string]interface{}{"method": "close"})
	}()
	return r
}

func (h *H) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Upgrade") == "websocket" {
		websocket.Handler(h.handleWebsocket).ServeHTTP(w, r)
	} else if r.Method == "POST" {
		if origin := r.Header.Get("Origin"); origin != "" && !strings.HasPrefix(origin, "http://localhost:") {
			w.WriteHeader(401)
			log.Printf("cross origin POST from '%s' rejected. Only localhost is allowed to call post handlers", origin)
			return
		}
		h.POSTMux.ServeHTTP(w, r)
	} else if r.URL.Path == "/_headless" {
		w.Write([]byte(HTML("", "")))
	} else if strings.HasPrefix(r.URL.Path, "/_headless_run_") {
		r, _ := h.runs.Load(fmt.Sprintf("http://localhost:%d%s", h.Port, r.URL.Path))
		w.Write([]byte(r.(*Run).HTML))
	} else if strings.HasPrefix(r.URL.Path, "/_headless/") {
		http.StripPrefix("/_headless/", http.FileServer(http.FS(Etc))).ServeHTTP(w, r)
	} else {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Expires", "0")
		http.FileServer(http.Dir("./")).ServeHTTP(w, r)
	}
}

func (h *H) handleWebsocket(ws *websocket.Conn) {
	if !strings.HasPrefix(ws.Config().Origin.Host, "localhost:") {
		return
	}
	websocket.JSON.Send(ws, map[string]interface{}{
		"method": "connect",
		"params": map[string]interface{}{"browserWebsocketUrl": h.Browser.websocketURL},
	})
	path := ws.Config().Location.Path
	url, messages := fmt.Sprintf("http://localhost:%d%s", h.Port, path), chan Message(nil)
	if path == "/_headless" {
		h.ws = ws
	} else {
		v, _ := h.runs.Load(url)
		v.(*Run).WS = ws
		messages = v.(*Run).Messages
	}
	for {
		m := struct {
			Method string
			Id     int
			Params struct {
				Url  string
				Args []interface{}
			}
		}{}
		if err := websocket.JSON.Receive(ws, &m); err != nil {
			if _, ok := h.runs.Load(url); h.Browser.cmd == nil || (path != "/_headless" && !ok) {
				return
			}
			panic(fmt.Sprintf("%s: %s", url, err))
		}
		switch m.Method {
		case "connect":
			if path == "/_headless" {
				close(h.connected)
			}
		case "close":
			h.runs.Delete(m.Params.Url)
			close(messages)
			ws.Close()
		default:
			if path == "/_headless" {
				log.Printf("unexpected message %#v", m)
			} else {
				messages <- Message{m.Method, m.Params.Args, m.Id}
			}
		}
	}
}

func (r *Run) Respond(m Message, v interface{}) {
	websocket.JSON.Send(r.WS, map[string]interface{}{
		"data": map[string]interface{}{"id": m.id, "result": v},
	})
}
