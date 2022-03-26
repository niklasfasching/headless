package headless

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

type ExitErr int

var Colors = map[string]int{
	"none":   0,
	"red":    31,
	"green":  32,
	"yellow": 33,
	"blue":   34,
	"purple": 35,
	"cyan":   36,
	"grey":   37,
}

var colorRegexp = regexp.MustCompile(`\bcolor\s*:\s*(\w+)\b`)
var modulePathRegexp = regexp.MustCompile("^(./|/|https?://)")

func (e ExitErr) Error() string { return fmt.Sprintf("exit code: %v", int(e)) }

func Colorize(args []interface{}) string {
	if len(args) == 0 {
		return ""
	}
	raw, ok := args[0].(string)
	if !ok {
		return fmt.Sprintf("%v", args)
	}
	parts := strings.Split(raw, "%c")
	out := parts[0]
	for i, part := range parts[1:] {
		if len(args) > i+1 {
			colorString, _ := args[i+1].(string)
			if m := colorRegexp.FindStringSubmatch(colorString); m != nil {
				out += fmt.Sprintf("\033[%dm", Colors[m[1]])
			}
		} else {
			out += fmt.Sprintf("\033[%dm", Colors["none"])
		}
		out += part
	}
	if len(parts) > 1 {
		out += fmt.Sprintf("\033[%dm", Colors["none"])
	}
	for _, a := range args[1:] {
		out += fmt.Sprintf(" %v", a)
	}
	return out
}

func TemplateHTML(code string, modules, args []string) string {
	argsBytes, err := json.Marshal(args)
	if err != nil {
		panic(err)
	}
	html := "<html>\n<head>\n"
	html += fmt.Sprintf("<script>window.args = %s;</script>\n", string(argsBytes))
	html += `<script type="module" onerror="throw new Error('failed to import files')">` + "\n"
	for _, m := range modules {
		if !modulePathRegexp.MatchString(m) {
			m = "./" + m
		}
		html += fmt.Sprintf(`import "%s";`, m) + "\n"
	}
	html += "</script>\n"
	if code != "" {
		html += fmt.Sprintf(`<script type="module">%s</script>`, "\n"+code+"\n")
	}
	return html + "</head>\n</html>"
}

func FormatException(m json.RawMessage) string {
	r := struct {
		ExceptionDetails struct {
			LineNumber, ColumnNumber int
			Url                      string
			Exception                struct {
				Description string
				Value       interface{}
			}
		}
	}{}
	if err := json.Unmarshal(m, &r); err != nil {
		panic(err)
	}
	if e := r.ExceptionDetails.Exception; e.Description != "" {
		return e.Description
	} else if e.Value != "" {
		return fmt.Sprintf("Unhandled: %v", e.Value)
	}
	return fmt.Sprintf("Unhandled %v", r.ExceptionDetails)
}
