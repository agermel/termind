// Package render 把诊断结果渲染成轻量的终端面板。
package render

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/term"
)

const (
	ansiEraseLine = "\r\x1b[K"
	ansiDim       = "\x1b[2m"
	ansiCyan      = "\x1b[36m"
	ansiRed       = "\x1b[31m"
	ansiBold      = "\x1b[1m"
	ansiReset     = "\x1b[0m"
	headerText    = "termind is thinking..."
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// Renderer 是一次诊断的终端渲染器。
type Renderer struct {
	w io.Writer

	mu       sync.Mutex
	started  bool
	gotToken bool
	closed   bool
	buf      strings.Builder
}

// New 构造一个 Renderer,w 通常是 os.Stdout。
func New(w io.Writer) *Renderer {
	return &Renderer{w: w}
}

// Start 打 header 占位行。多次调用只生效一次。
func (r *Renderer) Start() {
	r.StartAtLineStart(false)
}

// StartAtLineStart 打 header 占位行。atLineStart=true 表示调用方已经确认
// 光标在新行开头,不再额外插入换行。
func (r *Renderer) StartAtLineStart(atLineStart bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started || r.closed {
		return
	}
	r.started = true
	prefix := "\r\n"
	if atLineStart {
		prefix = ""
	}
	_, _ = fmt.Fprintf(r.w, "%s%s%s%s", prefix, ansiDim, headerText, ansiReset)
}

// Write 累积诊断文本。真正的面板在 Done 时一次性绘制,避免流式输出打乱 prompt。
func (r *Renderer) Write(delta string) (int, error) {
	if delta == "" {
		return 0, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return 0, nil
	}
	r.gotToken = true
	r.buf.WriteString(delta)
	return len(delta), nil
}

// Fail 终止当前渲染,打一个红色错误面板。
func (r *Renderer) Fail(msg string) {
	if strings.TrimSpace(msg) == "" {
		msg = "unknown error"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	r.clearHeader()
	_, _ = fmt.Fprint(r.w, renderPanel("termind diagnose", msg, panelError))
}

// Done 正常结束渲染。
func (r *Renderer) Done() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	if !r.gotToken {
		r.clearHeader()
		return
	}
	text := sanitizeForPanel(r.buf.String())
	if text == "" {
		r.clearHeader()
		return
	}
	r.clearHeader()
	_, _ = fmt.Fprint(r.w, renderPanel("termind insight", text, panelInfo))
}

func (r *Renderer) clearHeader() {
	if r.started {
		_, _ = fmt.Fprint(r.w, ansiEraseLine)
	}
}

type panelKind int

const (
	panelInfo panelKind = iota
	panelError
)

func renderPanel(title, body string, kind panelKind) string {
	width := panelWidth()
	contentWidth := width - 4
	lines := wrapLines(body, contentWidth)
	if len(lines) == 0 {
		lines = []string{"unknown error"}
	}

	color := ansiCyan
	mark := "i"
	if kind == panelError {
		color = ansiRed
		mark = "!"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s╭─ %s%s %s%s", color, ansiBold, mark, title, ansiReset)
	fill := width - 5 - displayWidth(mark) - displayWidth(title)
	if fill < 1 {
		fill = 1
	}
	b.WriteString(color)
	b.WriteString(strings.Repeat("─", fill))
	b.WriteString("╮")
	b.WriteString(ansiReset)
	b.WriteString("\r\n")

	for _, line := range lines {
		pad := contentWidth - displayWidth(line)
		if pad < 0 {
			pad = 0
		}
		fmt.Fprintf(&b, "%s│%s %s%s │%s\r\n", color, ansiReset, line, strings.Repeat(" ", pad), color)
		b.WriteString(ansiReset)
	}

	b.WriteString(color)
	b.WriteString("╰")
	b.WriteString(strings.Repeat("─", width-2))
	b.WriteString("╯")
	b.WriteString(ansiReset)
	b.WriteString("\r\n")
	return b.String()
}

func panelWidth() int {
	width := 72
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		width = w - 2
	}
	if width > 72 {
		width = 72
	}
	if width < 36 {
		width = 36
	}
	return width
}

func sanitizeForPanel(s string) string {
	s = ansiRE.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	inFence := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
			continue
		}
		line = strings.TrimPrefix(line, "- ")
		line = strings.TrimPrefix(line, "* ")
		line = strings.ReplaceAll(line, "**", "")
		line = strings.ReplaceAll(line, "__", "")
		line = strings.ReplaceAll(line, "`", "")
		line = strings.Join(strings.Fields(line), " ")
		if inFence && line != "" {
			line = "$ " + line
		}
		if line != "" {
			out = append(out, line)
		}
		if len(out) >= 4 {
			break
		}
	}
	return strings.Join(out, "\n")
}

func wrapLines(s string, width int) []string {
	var out []string
	for _, raw := range strings.Split(s, "\n") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		for displayWidth(raw) > width {
			head, tail := splitByDisplayWidth(raw, width)
			out = append(out, strings.TrimSpace(head))
			raw = strings.TrimSpace(tail)
		}
		if raw != "" {
			out = append(out, raw)
		}
	}
	return out
}

func splitByDisplayWidth(s string, width int) (string, string) {
	if width <= 0 {
		return "", s
	}
	lastSpaceByte := -1
	used := 0
	for i, r := range s {
		if r == ' ' || r == '\t' {
			lastSpaceByte = i
		}
		next := used + runeWidth(r)
		if next > width {
			if lastSpaceByte > 0 {
				return s[:lastSpaceByte], s[lastSpaceByte+1:]
			}
			return s[:i], s[i:]
		}
		used = next
	}
	return s, ""
}

func displayWidth(s string) int {
	width := 0
	for _, r := range s {
		width += runeWidth(r)
	}
	return width
}

func runeWidth(r rune) int {
	if r == '\t' {
		return 4
	}
	if r < 32 {
		return 0
	}
	if r >= 0x1100 &&
		(r <= 0x115F ||
			r == 0x2329 || r == 0x232A ||
			(r >= 0x2E80 && r <= 0xA4CF) ||
			(r >= 0xAC00 && r <= 0xD7A3) ||
			(r >= 0xF900 && r <= 0xFAFF) ||
			(r >= 0xFE10 && r <= 0xFE19) ||
			(r >= 0xFE30 && r <= 0xFE6F) ||
			(r >= 0xFF00 && r <= 0xFF60) ||
			(r >= 0xFFE0 && r <= 0xFFE6)) {
		return 2
	}
	if !utf8.ValidRune(r) {
		return 1
	}
	return 1
}
