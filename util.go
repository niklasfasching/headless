package headless

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

//go:embed etc/*
var etc embed.FS
var Etc fs.FS

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

func init() {
	if strings.HasPrefix(os.Args[0], "/tmp/go-build") {
		_, filename, _, _ := runtime.Caller(0)
		Etc = os.DirFS(filepath.Join(filename, "../etc"))
	} else {
		Etc, _ = fs.Sub(etc, "etc")
	}
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

func HTML(headHTML, templateHTML string) string {
	bs, _ := fs.ReadFile(Etc, "headless.html")
	html := strings.ReplaceAll(string(bs), "</template>", templateHTML+"</template>")
	return strings.ReplaceAll(html, "<!-- head -->", headHTML)
}

func TemplateHTML(code string, files, args []string) string {
	argsBytes, _ := json.Marshal(args)
	html := fmt.Sprintf("<script>window.args = %s;</script>\n", string(argsBytes))
	html += `<script type="module" onerror="throw new Error('failed to import files')">` + "\n"
	for _, f := range files {
		html += fmt.Sprintf(`import "./%s";`, f) + "\n"
	}
	html += "</script>\n"
	if code != "" {
		html += fmt.Sprintf(`<script type="module">%s</script>`, "\n"+code+"\n")
	}
	return html
}

func CreateHandler(w http.ResponseWriter, r *http.Request) {
	f, err := os.Create(filepath.Join(".", r.URL.Query().Get("path")))
	if err != nil {
		w.WriteHeader(504)
		fmt.Fprintf(w, "%s", err)
		return
	}
	defer f.Close()
	io.Copy(f, r.Body)
}
