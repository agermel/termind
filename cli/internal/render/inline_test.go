package render

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderer_StartThenTokensThenDoneRendersPanel(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf)
	r.StartAtLineStart(true)
	if _, err := r.Write("hello world"); err != nil {
		t.Fatal(err)
	}
	r.Done()

	out := stripANSI(buf.String())
	if !strings.Contains(out, "termind is thinking") {
		t.Fatalf("header not written: %q", out)
	}
	if !strings.Contains(out, "termind insight") {
		t.Fatalf("panel title missing: %q", out)
	}
	if !strings.Contains(out, "hello world") {
		t.Fatalf("tokens missing: %q", out)
	}
	if !strings.Contains(out, "╭") || !strings.Contains(out, "╰") {
		t.Fatalf("panel border missing: %q", out)
	}
}

func TestRenderer_StartAtLineStartAvoidsLeadingBlankLine(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf)
	r.StartAtLineStart(true)
	out := buf.String()
	if strings.HasPrefix(out, "\n") || strings.HasPrefix(out, "\r\n") {
		t.Fatalf("unexpected leading newline: %q", out)
	}
	if !strings.Contains(out, headerText) {
		t.Fatalf("header not written: %q", out)
	}
}

func TestRenderer_DoneWithoutTokensErasesHeader(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf)
	r.Start()
	r.Done()
	out := buf.String()
	if !strings.Contains(out, ansiEraseLine) {
		t.Fatalf("erase missing: %q", out)
	}
	if strings.Contains(stripANSI(out), "termind insight") {
		t.Fatalf("empty done should not render panel: %q", out)
	}
}

func TestRenderer_FailBeforeTokens(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf)
	r.Start()
	r.Fail("server 炸了")
	out := stripANSI(buf.String())
	if !strings.Contains(out, "server 炸了") {
		t.Fatalf("fail msg missing: %q", out)
	}
	if !strings.Contains(out, "termind diagnose") {
		t.Fatalf("error panel title missing: %q", out)
	}
}

func TestRenderer_ClosedIsIdempotent(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf)
	r.Start()
	_, _ = r.Write("x")
	r.Done()
	lenAfter := buf.Len()
	r.Done()
	_, _ = r.Write("y")
	r.Fail("ignored")
	if buf.Len() != lenAfter {
		t.Fatalf("closed renderer still wrote: added=%q", buf.String()[lenAfter:])
	}
}

func TestRenderer_SanitizesMarkdownAndWraps(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf)
	r.StartAtLineStart(true)
	_, _ = r.Write("**最可能原因**：命令 `cnmb` 不存在。\n```bash\nwhich cnmb 2>/dev/null || brew search cnmb\n```\n- 检查 alias。")
	r.Done()
	out := stripANSI(buf.String())
	if strings.Contains(out, "**") || strings.Contains(out, "```") || strings.Contains(out, "`") {
		t.Fatalf("markdown leaked: %q", out)
	}
	if !strings.Contains(out, "最可能原因：命令 cnmb 不存在。") {
		t.Fatalf("clean text missing: %q", out)
	}
}

func TestWrapLinesRespectsWidth(t *testing.T) {
	lines := wrapLines("abcdefghijklmnopqrstuvwxyz", 8)
	if len(lines) < 2 {
		t.Fatalf("expected wrapping: %v", lines)
	}
	for _, line := range lines {
		if displayWidth(line) > 8 {
			t.Fatalf("line too wide: %q", line)
		}
	}
}

func stripANSI(s string) string {
	return ansiRE.ReplaceAllString(s, "")
}
