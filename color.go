package goheadless

import (
	"fmt"
	"regexp"
	"strings"
)

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

func Colorize(e Event) string {
	if len(e.Args) == 0 {
		return ""
	}
	raw, _ := e.Args[0].(string)
	parts := strings.Split(raw, "%c")
	out := parts[0]
	for i, part := range parts[1:] {
		if len(e.Args) > i+1 {
			colorString, _ := e.Args[i+1].(string)
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
