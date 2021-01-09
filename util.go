package goheadless

import (
	"embed"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"
)

//go:embed etc/*
var Etc embed.FS

var colorRegexp = regexp.MustCompile(`\bcolor\s*:\s*(\w+)\b`)

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

func Colorize(m Message) string {
	if len(m.Args) == 0 {
		return ""
	}
	raw, _ := m.Args[0].(string)
	if fi, _ := os.Stdout.Stat(); (fi.Mode() & os.ModeCharDevice) == 0 {
		return strings.ReplaceAll(raw, "%c", "")
	}
	parts := strings.Split(raw, "%c")
	out := parts[0]
	for i, part := range parts[1:] {
		if len(m.Args) > i+1 {
			colorString, _ := m.Args[i+1].(string)
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
	return out
}

func GetFreePort() int {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		panic(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func HTML(code string, files, args []string) string {
	argsBytes, _ := json.Marshal(args)
	html := fmt.Sprintf("<script>window.args = %s;</script>\n", string(argsBytes))
	for _, f := range files {
		html += fmt.Sprintf(`<script type="module" src="%s" onerror="throw new Error('failed to import %s')"></script>`, f, f) + "\n"
	}
	if code != "" {
		html += fmt.Sprintf(`<script type="module">%s</script>`, "\n"+code+"\n")
	}

	runHTML, err := Etc.ReadFile("etc/run.html")
	if err != nil {
		panic(err)
	}
	return strings.ReplaceAll(string(runHTML), "</template>", html+"</template>")
}
