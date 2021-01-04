package goheadless

import (
	"context"
	"encoding/json"
	"testing"
)

type testCase struct {
	name   string
	domain string
	code   string
	files  []string
	args   []string

	messages []Message
}

var runTestCases = []testCase{
	{
		name: "log(log) number and exit 0",
		code: "console.log(1); close()",
		messages: []Message{
			{Method: "log", Args: []interface{}{1}},
			{Method: "clear", Args: []interface{}{0}},
		},
	},

	{
		name: "log(info) string and exit 1",
		code: "console.info('foo'); close(1)",
		messages: []Message{
			{Method: "info", Args: []interface{}{"foo"}},
			{Method: "clear", Args: []interface{}{1}},
		},
	},

	{
		name:  "import module - log(warn) object and exit 0",
		files: []string{"./testdata/index.mjs"},
		messages: []Message{
			{Method: "warning", Args: []interface{}{`{foo: bar}`}},
			{Method: "clear", Args: []interface{}{0}},
		},
	},

	{
		name:  "exit with error on import error",
		files: []string{"./testdata/doesNotExist.mjs"},
		messages: []Message{
			{Method: "exception", Args: []interface{}{"Error: failed to import ./testdata/doesNotExist.mjs\n    at HTMLScriptElement.onerror (http://localhost:9001/_run_4:147:137)\n    at http://localhost:9001/_run_4:146:136"}}},
	},

	{
		name: "log uncaught error",
		code: "invalid code",
		messages: []Message{
			{Method: "exception", Args: []interface{}{"SyntaxError: Unexpected identifier\n    at http://localhost:9001/_run_5:147:8"}},
		},
	},

	{
		name: "receive messages from pages opened in runs",
		code: `
          console.log("code");
          (async () => {
            await headless.ready;
            const page = await headless.open({path: "testdata/index.html"})
            while (true) {
              const {result: {value}} = await page.call("Runtime.evaluate", {expression: "document.readyState"});
              if (value === "complete") return void close(0);
              else await new Promise(r => setTimeout(r, 100))
            }
          })()`,
		messages: []Message{
			{Method: "log", Args: []interface{}{"code"}},
			{Method: "log", Args: []interface{}{"index"}},
			{Method: "clear", Args: []interface{}{0}},
		},
	},
}

func TestRun(t *testing.T) {
	h := &Runner{Port: 9001}
	if err := h.Start(); err != nil {
		t.Error(err)
		return
	}
	defer h.Stop()
	for _, tc := range runTestCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			c := h.Run(ctx, HTML(tc.code, tc.files, tc.args))
			test(t, tc, c, cancel)
		})
	}
}

func test(t *testing.T, tc testCase, c chan Message, cancel func()) {
	messages := []Message{}
	for m := range c {
		messages = append(messages, m)
		if m.Method == "clear" || m.Method == "exception" {
			cancel()
		}
	}
	expectedMessagesJSON, _ := json.Marshal(tc.messages)
	actualMessagesJSON, _ := json.Marshal(messages)
	if string(expectedMessagesJSON) != string(actualMessagesJSON) {
		t.Errorf("messages differ: %s !== %s", string(actualMessagesJSON), string(expectedMessagesJSON))
	}
}
