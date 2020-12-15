package goheadless

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

type testCase struct {
	name  string
	code  string
	files []string
	args  []string

	events   []Event
	error    error
	exitCode int
}

var testCases = []testCase{
	{
		name: "log(log) number and exit 0",
		code: "console.log(1); close()",
		events: []Event{
			Event{Method: "log", Args: []interface{}{1}},
		},
	},

	{
		name:     "log(info) string and exit 1",
		code:     "console.info('foo'); close(1)",
		events:   []Event{{Method: "info", Args: []interface{}{"foo"}}},
		exitCode: 1,
	},

	{
		name:     "import module - log(warn) object and exit 0",
		files:    []string{"./testdata/index.mjs"},
		events:   []Event{{Method: "warning", Args: []interface{}{`{foo: bar}`}}},
		exitCode: 0,
	},

	{
		name:     "exit with error on import error",
		files:    []string{"./testdata/doesNotExist.mjs"},
		exitCode: -1,
		events:   []Event{},
		error:    fmt.Errorf("Error: bad imports\n    at http://localhost:9001/:27:36\n    at http://localhost:9001/:26:35"),
	},

	{
		name:     "log uncaught error",
		code:     "invalid code",
		events:   []Event{},
		error:    fmt.Errorf("SyntaxError: Unexpected identifier\n    at http://localhost:9001/:27:12"),
		exitCode: -1,
	},
}

func TestRun(t *testing.T) {
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r := &Runner{
				Address: "localhost:9001",
				Code:    tc.code,
				Files:   tc.files,
				Args:    tc.args,
			}
			out, events := make(chan Event, 1000), []Event{}
			exitCode, err := r.ServeAndRun(context.Background(), out)
			if exitCode != tc.exitCode {
				t.Errorf("exitCode differs: %d !== %d", exitCode, tc.exitCode)
			}
			if fmt.Sprintf("%v", err) != fmt.Sprintf("%v", tc.error) {
				t.Errorf("error  differs: %#v !== %#v", err, tc.error)
			}
			for event := range out {
				events = append(events, event)
			}
			expectedEventsJSON, _ := json.Marshal(tc.events)
			actualEventsJSON, _ := json.Marshal(events)
			if string(expectedEventsJSON) != string(actualEventsJSON) {
				t.Errorf("events differ: %s !== %s", string(actualEventsJSON), string(expectedEventsJSON))
			}
		})
	}

}
