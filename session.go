package headless

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
)

type Session struct {
	id       string
	targetID string
	h        *H
	handlers map[string][]reflect.Value
	bindings map[string]reflect.Value
	sync.Mutex
}

func (s *Session) Handle(method string, f interface{}) {
	fv := reflect.ValueOf(f)
	if t := fv.Type(); t.NumIn() != 1 || t.NumOut() != 0 {
		panic(fmt.Sprintf("handler func must be of type func(T)"))
	}
	s.Lock()
	s.handlers[method] = append(s.handlers[method], fv)
	s.Unlock()
}

func (s *Session) Exec(method string, params, v interface{}) error {
	return s.h.exec(s.id, method, params, v)
}

func (s *Session) Open(url string) error {
	return s.Exec("Page.navigate", Params{"url": url}, nil)
}

func (s *Session) Eval(js string, v interface{}) error {
	r, params := struct {
		Result struct{ Value json.RawMessage }
	}{}, Params{"expression": js, "returnByValue": true, "replMode": true, "awaitPromise": true}
	if err := s.Exec("Runtime.evaluate", params, &r); err != nil {
		return err
	}
	if v == nil {
		return nil
	}
	return json.Unmarshal(r.Result.Value, v)
}

func (s *Session) Close() error {
	return s.h.Close(s)
}

func (s *Session) Bind(name string, f interface{}) {
	if err := s.bind(name, f); err != nil {
		panic(err)
	}
}

func (s *Session) bind(name string, f interface{}) error {
	fv := reflect.ValueOf(f)
	ft := fv.Type()
	s.Lock()
	s.bindings[name] = fv
	s.Unlock()
	if err := s.Exec("Runtime.addBinding", Params{"name": name}, nil); err != nil {
		return err
	}
	js := fmt.Sprintf(`(() => {
      const binding = window["%[1]s"];
      window.%[1]s = (...args) => new Promise((resolve, reject) => {
        const id = String(window.%[1]s.nextID++);
        window.%[1]s.pending[id] = {resolve, reject};
        binding(JSON.stringify({id, args}));
      });
      Object.assign(window.%[1]s, {pending: {}, nextID: 0});
    })()`, name)
	if isVoid := ft.NumOut() == 0; isVoid {
		js = fmt.Sprintf(`(() => {
          const binding = window["%[1]s"];
          window.%[1]s = (...args) => binding(JSON.stringify({args}));
        })()`, name)
	}
	if err := s.Exec("Page.addScriptToEvaluateOnNewDocument", Params{"source": js}, nil); err != nil {
		return err
	}
	return s.Eval(js, nil)
}

func (s *Session) onBindingCalled(m struct{ Name, Payload string }) {
	s.Lock()
	fv, ok := s.bindings[m.Name]
	s.Unlock()
	if !ok {
		return
	}
	p := struct {
		ID   string
		Args []json.RawMessage
	}{}
	if err := json.Unmarshal([]byte(m.Payload), &p); err != nil {
		panic(err)
	}
	isVoid, isErr, arg := fv.Type().NumOut() == 0, "false", "null"
	if v, err := callBoundFunc(fv, p.Args); isVoid {
		return
	} else if err != nil {
		isErr, arg = "true", fmt.Sprintf(`new Error("%s")`, err.Error())
	} else if vbs, err := json.Marshal(v); err != nil {
		isErr, arg = "true", fmt.Sprintf(`new Error("marshal: %s")`, err.Error())
	} else {
		isErr, arg = "false", string(vbs)
	}
	js := fmt.Sprintf(`(() => {
      const id = "%[2]s", isErr = %[3]s, arg = %[4]s;
      window.%[1]s.pending[id][isErr ? "reject" : "resolve"](arg);
      delete window.%[1]s.pending[id];
    })()`, m.Name, p.ID, isErr, arg)
	if err := s.Eval(js, nil); err != nil {
		panic(err)
	}
}

func callBoundFunc(fv reflect.Value, args []json.RawMessage) (interface{}, error) {
	ft, avs := fv.Type(), []reflect.Value{}
	numIn, isVariadic := ft.NumIn(), fv.Type().IsVariadic()
	if !isVariadic && len(args) != ft.NumIn() {
		return nil, fmt.Errorf("wrong number of arguments: %d but expected %d", len(args), ft.NumIn())
	}
	for i := 0; i < len(args); i++ {
		var av reflect.Value
		if isVarArg := i >= numIn-1 && isVariadic; isVarArg {
			av = reflect.New(ft.In(ft.NumIn() - 1).Elem())
		} else {
			av = reflect.New(ft.In(i))
		}
		if err := json.Unmarshal(args[i], av.Interface()); err != nil {
			return nil, err
		}
		avs = append(avs, av.Elem())
	}

	rvs := fv.Call(avs)
	if len(rvs) == 0 {
		return nil, nil
	} else if len(rvs) == 1 {
		if err, ok := rvs[0].Interface().(error); ok {
			return nil, err
		}
		return rvs[0].Interface(), nil
	} else if err := rvs[1].Interface(); err != nil {
		return nil, err.(error)
	}
	return rvs[0].Interface(), nil
}
