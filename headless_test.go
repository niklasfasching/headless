package goheadless

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"testing"
)

var updateTestData = flag.Bool("update-test-data", false, "update test data rather than actually running tests")

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
	},

	{
		name: "log(info) string and exit 1",
		code: "console.info('foo'); close(1)",
	},

	{
		name:  "import module - log(warn) object and exit 0",
		files: []string{"/testdata/index.mjs"},
	},

	{
		name:  "exit with error on import error",
		files: []string{"./testdata/doesNotExist.mjs"},
	},

	{
		name: "log uncaught error",
		code: "invalid code",
	},
	{
		name: "closes opened child targets with the respective runs",
		code: `
          (async () => {
            const {targetInfos} = await headless.browser.call("Target.getTargets");
            console.log(targetInfos.length)
            close(0)
           })()
        `,
	},
}

func TestRun(t *testing.T) {
	flag.Parse()

	r := &Runner{Port: 9001}
	if err := r.Start(); err != nil {
		t.Error(err)
		return
	}
	defer r.Stop()

	bs, err := ioutil.ReadFile("testdata/results.json")
	if err != nil {
		t.Error(err)
		return
	}
	expected := map[string]json.RawMessage{}
	if !*updateTestData {
		json.Unmarshal(bs, &expected)
	}
	for i, tc := range runTestCases {
		t.Run(tc.name, func(t *testing.T) {
			key := fmt.Sprintf("%d: %s", i, tc.name)
			ctx, cancel := context.WithCancel(context.Background())
			run := r.Run(ctx, HTML(tc.code, tc.files, tc.args))
			messages := []Message{}
			for m := range run.Messages {
				messages = append(messages, m)
				if m.Method == "clear" || m.Method == "exception" {
					cancel()
				}
			}
			actual, _ := json.MarshalIndent(messages, "  ", "  ")
			if *updateTestData {
				expected[key] = actual
			} else if string(actual) != string(expected[key]) {
				t.Errorf("messages differ: %s !== %s", string(actual), string(expected[key]))
			}
		})
	}

	if *updateTestData {
		bs, _ := json.MarshalIndent(expected, "", "  ")
		if err := ioutil.WriteFile("testdata/results.json", bs, 0666); err != nil {
			t.Error(err)
		}
	}
}
