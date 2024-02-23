package headless

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"reflect"
	"sync"
	"time"
)

type H struct {
	cmd      *exec.Cmd
	pipe     pipe
	nextID   int
	pending  map[int]chan message
	sessions map[string]*Session
	sync.Mutex
}

type Params map[string]interface{}

type pipe struct {
	r *os.File
	w *os.File
	*bufio.Reader
}

type message struct {
	ID        int
	SessionID string
	Method    string
	Params    json.RawMessage
	Result    json.RawMessage
	Error     json.RawMessage
}

var Executable = "chromium-browser"

var debug = os.Getenv("DEBUG") == "true"
var defaultBrowserArgs = map[string]bool{
	"--remote-debugging-pipe": true,
	"--temp-profile":          true,
	"--headless":              true,
}

func init() {
	if e := os.Getenv("HEADLESS_EXECUTABLE"); e != "" {
		Executable = e
	}
}

func Start(args map[string]bool) (*H, error) {
	m, mergedArgs := map[string]bool{}, []string{}
	for k, v := range defaultBrowserArgs {
		m[k] = v
	}
	for k, v := range args {
		m[k] = v
	}
	for a, v := range m {
		if v {
			mergedArgs = append(mergedArgs, a)
		}
	}
	ir, iw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	or, ow, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(Executable, mergedArgs...)
	cmd.ExtraFiles = append(cmd.ExtraFiles, ir, ow)
	h := &H{
		pipe:     pipe{or, iw, bufio.NewReader(or)},
		cmd:      cmd,
		pending:  map[int]chan message{},
		sessions: map[string]*Session{},
	}
	if err := h.cmd.Start(); err != nil {
		return nil, err
	}
	go func() {
		if err := h.loop(); err != nil {
			panic(err)
		}
	}()
	return h, nil
}

func (h *H) Stop() error {
	h.Lock()
	cmd, pipe := h.cmd, h.pipe
	h.cmd = nil
	h.Unlock()
	if cmd == nil {
		return nil
	}
	if err := pipe.r.Close(); err != nil {
		return err
	} else if err := pipe.w.Close(); err != nil {
		return err
	} else if err := cmd.Process.Kill(); err != nil {
		return err
	}
	_, err := cmd.Process.Wait()
	return err
}

func (h *H) Open(url string) (*Session, error) {
	cr := struct{ TargetId string }{}
	if err := h.Exec("Target.createTarget", Params{"url": url}, &cr); err != nil {
		return nil, err
	}
	ar := struct{ SessionId string }{}
	if err := h.Exec("Target.attachToTarget", Params{"targetId": cr.TargetId, "flatten": true}, &ar); err != nil {
		return nil, err
	}
	s := &Session{
		id:       ar.SessionId,
		targetID: cr.TargetId,
		h:        h,
		handlers: map[string][]reflect.Value{},
		bindings: map[string]reflect.Value{},
		Err:      make(chan error),
	}
	h.Lock()
	h.sessions[ar.SessionId] = s
	h.Unlock()
	for _, domain := range []string{"Page", "Runtime"} {
		if err := s.Exec(domain+".enable", nil, nil); err != nil {
			return nil, err
		}
	}
	s.Handle("Runtime.bindingCalled", s.onBindingCalled)
	s.Handle("Runtime.exceptionThrown", func(m json.RawMessage) { s.Err <- fmt.Errorf(FormatException(m)) })
	return s, nil
}

func (h *H) Close(s *Session) error {
	h.Lock()
	delete(h.sessions, s.id)
	h.Unlock()
	r := struct{ Success bool }{}
	err := h.Exec("Target.closeTarget", Params{"targetId": s.targetID}, &r)
	if err != nil {
		return err
	} else if !r.Success {
		return fmt.Errorf("error closing target: browser says no success")
	}
	return nil
}

func (h *H) Exec(method string, params, v interface{}) error {
	return h.exec("", method, params, v)
}

func (h *H) exec(sessionID, method string, params, v interface{}) error {
	h.Lock()
	id, c := h.nextID, make(chan message, 1)
	h.nextID++
	h.pending[id] = c
	h.Unlock()
	m := map[string]interface{}{
		"method":    method,
		"params":    params,
		"id":        id,
		"sessionId": sessionID,
	}
	bs, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if debug {
		log.Println("->", string(bs))
	}
	if err := h.send(bs); err != nil {
		return err
	}
	r := <-c
	if r.Error != nil {
		e := map[string]interface{}{}
		if err := json.Unmarshal(r.Error, &e); err != nil {
			return fmt.Errorf("%s", string(r.Error))
		}
		return fmt.Errorf("%v: %v (%v)", e["code"], e["message"], e["data"])
	}
	if v == nil {
		return nil
	} else if err := json.Unmarshal(r.Result, v); err != nil {
		return fmt.Errorf("could not unmarshal '%s' into %T", string(r.Result), v)
	}
	return nil
}

func (h *H) send(bs []byte) error {
	h.Lock()
	defer h.Unlock()
	_, err := h.pipe.w.Write(append(bs, 0))
	return err
}

func (h *H) loop() error {
	c := make(chan func(), 100)
	go func() {
		for f := range c {
			f()
		}
	}()
	for {
		bs, err := h.pipe.ReadBytes(0)
		if err != nil {
			h.Lock()
			cmd := h.cmd
			h.Unlock()
			if cmd == nil {
				return nil
			}
			return fmt.Errorf("could not read from pipe: %s", err)
		}
		if len(bs) == 0 {
			continue
		}
		m, bs := message{}, bs[:len(bs)-1]
		if err := json.Unmarshal(bs, &m); err != nil {
			return fmt.Errorf("bad message: %s: '%s'", err, string(bs))
		}
		if debug {
			log.Println("<-", string(bs))
		}
		if m.Method != "" {
			h.Lock()
			s := h.sessions[m.SessionID]
			h.Unlock()
			if s == nil {
				continue
			}
			s.Lock()
			hvs := s.handlers[m.Method]
			s.Unlock()
			for _, hv := range hvs {
				av := reflect.New(hv.Type().In(0))
				if err := json.Unmarshal(m.Params, av.Interface()); err != nil {
					return fmt.Errorf("could not marshal %s into %T", string(m.Params), av.Interface())
				}
				select {
				case c <- func() { hv.Call([]reflect.Value{av.Elem()}) }:
				case <-time.After(10 * time.Second):
					panic(fmt.Sprintf("cannot enqueue %s", string(m.Params)))
				}
			}
		} else {
			h.Lock()
			c, ok := h.pending[m.ID]
			delete(h.pending, m.ID)
			h.Unlock()
			if ok {
				c <- m
			}
		}
	}
}
