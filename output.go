package main

import (
	"fmt"
	"hash/fnv"
	"os"
	"strings"

	"golang.org/x/term"
)

// outMsg is a fully formatted line destined for either stdout (log output) or stderr (status/errors).
type outMsg struct {
	text  string
	isErr bool
}

// palette of distinguishable ANSI foreground colors, assigned stably per host.
var palette = []string{
	"\033[31m", "\033[32m", "\033[33m", "\033[34m", "\033[35m", "\033[36m",
	"\033[91m", "\033[92m", "\033[93m", "\033[94m", "\033[95m", "\033[96m",
}

const colorReset = "\033[0m"

// host carries the precomputed presentation for one instance.
type host struct {
	name  string
	color string // ANSI code, or "" when color is disabled
	width int    // padding width for aligned prefixes
}

// colorsEnabled reports whether we should emit ANSI color: stdout is a TTY and NO_COLOR is unset.
func colorsEnabled() bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// buildHosts assigns stable colors and a shared alignment width across all instances.
func buildHosts(instances []instance) []*host {
	useColor := colorsEnabled()
	width := 0
	for _, inst := range instances {
		if len(inst.name) > width {
			width = len(inst.name)
		}
	}

	hosts := make([]*host, len(instances))
	for i, inst := range instances {
		color := ""
		if useColor {
			color = palette[hashIndex(inst.name, len(palette))]
		}
		hosts[i] = &host{name: inst.name, color: color, width: width}
	}
	return hosts
}

func hashIndex(s string, n int) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return int(h.Sum32()) % n
}

// logLine formats a tail output line: "name │ <text>", color-coded and aligned.
func (h *host) logLine(text string) string {
	name := fmt.Sprintf("%-*s", h.width, h.name)
	if h.color != "" {
		name = h.color + name + colorReset
	}
	return name + " │ " + text
}

// statusLine formats a status/error line: "name <symbol> <text>".
func (h *host) statusLine(symbol, text string) string {
	name := h.name
	if h.color != "" {
		name = h.color + name + colorReset
	}
	return strings.Join([]string{name, symbol, text}, " ")
}
