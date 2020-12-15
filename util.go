package goheadless

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
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

func resolveConsoleArgs(c consoleAPICall) []interface{} {
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
