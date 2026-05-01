package osc133

import (
	"bytes"
	"testing"
)

// newTestParser 返回一个 parser + downstream buffer + 收集到的事件切片(通过指针)。
func newTestParser() (*Parser, *bytes.Buffer, *[]Event) {
	var out bytes.Buffer
	var events []Event
	p := NewParser(&out, func(ev Event) { events = append(events, ev) })
	return p, &out, &events
}

func TestParser_PassThroughNormal(t *testing.T) {
	p, out, events := newTestParser()
	if _, err := p.Write([]byte("hello world")); err != nil {
		t.Fatal(err)
	}
	if out.String() != "hello world" {
		t.Errorf("want 'hello world', got %q", out.String())
	}
	if len(*events) != 0 {
		t.Errorf("expected no events, got %d", len(*events))
	}
}

func TestParser_CommandStartEnd(t *testing.T) {
	p, out, events := newTestParser()
	input := []byte("\x1b]133;C\x07hello\n\x1b]133;D;0\x07")
	if _, err := p.Write(input); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "hello\n" {
		t.Errorf("want 'hello\\n', got %q", got)
	}
	if len(*events) != 2 {
		t.Fatalf("want 2 events, got %d: %v", len(*events), *events)
	}
	if (*events)[0].Kind != EventCommandStart {
		t.Errorf("event[0] = %v, want CommandStart", (*events)[0])
	}
	if (*events)[1].Kind != EventCommandEnd || (*events)[1].Exit != 0 {
		t.Errorf("event[1] = %v, want CommandEnd exit=0", (*events)[1])
	}
}

func TestParser_NonZeroExit(t *testing.T) {
	p, _, events := newTestParser()
	if _, err := p.Write([]byte("\x1b]133;D;137\x07")); err != nil {
		t.Fatal(err)
	}
	if len(*events) != 1 {
		t.Fatalf("want 1 event, got %d", len(*events))
	}
	e := (*events)[0]
	if e.Kind != EventCommandEnd || e.Exit != 137 {
		t.Errorf("want CommandEnd exit=137, got %+v", e)
	}
}

func TestParser_PromptAB(t *testing.T) {
	p, _, events := newTestParser()
	if _, err := p.Write([]byte("\x1b]133;A\x07\x1b]133;B\x07")); err != nil {
		t.Fatal(err)
	}
	if len(*events) != 2 {
		t.Fatalf("want 2 events, got %d: %v", len(*events), *events)
	}
	if (*events)[0].Kind != EventPromptStart {
		t.Errorf("event[0] = %v, want PromptStart", (*events)[0])
	}
	if (*events)[1].Kind != EventPromptEnd {
		t.Errorf("event[1] = %v, want PromptEnd", (*events)[1])
	}
}

func TestParser_OtherOscPassThrough(t *testing.T) {
	// OSC 0(设置窗口标题)不是 133,应当原样透传,且不产生事件
	p, out, events := newTestParser()
	input := []byte("\x1b]0;my title\x07after")
	if _, err := p.Write(input); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), input) {
		t.Errorf("OSC 0 should pass through:\n  want %q\n  got  %q", input, out.Bytes())
	}
	if len(*events) != 0 {
		t.Errorf("expected no events, got %d", len(*events))
	}
}

func TestParser_AnsiCsiPassThrough(t *testing.T) {
	// ANSI CSI (ESC [ ... m) 不是 OSC,应当原样透传
	p, out, events := newTestParser()
	input := []byte("\x1b[32mgreen\x1b[0m")
	if _, err := p.Write(input); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), input) {
		t.Errorf("ANSI CSI should pass through:\n  want %q\n  got  %q", input, out.Bytes())
	}
	if len(*events) != 0 {
		t.Errorf("expected no events, got %d", len(*events))
	}
}

func TestParser_SplitAcrossWrites(t *testing.T) {
	// OSC 序列横跨多次 Write 调用,parser 必须保留状态
	p, out, events := newTestParser()
	chunks := [][]byte{
		[]byte("pre"),
		[]byte("\x1b"),
		[]byte("]133"),
		[]byte(";C"),
		[]byte("\x07post"),
	}
	for _, c := range chunks {
		if _, err := p.Write(c); err != nil {
			t.Fatal(err)
		}
	}
	if got := out.String(); got != "prepost" {
		t.Errorf("want 'prepost', got %q", got)
	}
	if len(*events) != 1 || (*events)[0].Kind != EventCommandStart {
		t.Errorf("want 1 CommandStart event, got %v", *events)
	}
}

func TestParser_StTerminator(t *testing.T) {
	// 用 ESC\ 作为终止符替代 BEL,也应能识别
	p, _, events := newTestParser()
	if _, err := p.Write([]byte("\x1b]133;C\x1b\\")); err != nil {
		t.Fatal(err)
	}
	if len(*events) != 1 || (*events)[0].Kind != EventCommandStart {
		t.Errorf("ST terminator: want 1 CommandStart, got %v", *events)
	}
}

func TestParser_FullCommandCycle(t *testing.T) {
	// 一个完整的 "prompt → 命令 → prompt" 循环,验证 tail 的字节空间
	p, out, events := newTestParser()
	input := []byte(
		"\x1b]133;A\x07" + // prompt 开始
			"$ " + // prompt 文字
			"\x1b]133;C\x07" + // 命令开始
			"cmd output\n" + // 命令输出
			"\x1b]133;D;2\x07" + // 命令结束 exit=2
			"\x1b]133;A\x07" + // 下个 prompt 开始
			"$ ") // 下个 prompt 文字
	if _, err := p.Write(input); err != nil {
		t.Fatal(err)
	}
	wantOut := "$ cmd output\n$ "
	if out.String() != wantOut {
		t.Errorf("passthrough:\n  want %q\n  got  %q", wantOut, out.String())
	}
	if len(*events) != 4 {
		t.Fatalf("want 4 events, got %d: %v", len(*events), *events)
	}
	wantKinds := []EventKind{EventPromptStart, EventCommandStart, EventCommandEnd, EventPromptStart}
	for i, want := range wantKinds {
		if (*events)[i].Kind != want {
			t.Errorf("event[%d].Kind = %v, want %v", i, (*events)[i].Kind, want)
		}
	}
	if (*events)[2].Exit != 2 {
		t.Errorf("CommandEnd exit = %d, want 2", (*events)[2].Exit)
	}
}
