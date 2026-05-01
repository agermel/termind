package render

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderer_StartThenTokensThenDone(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf)
	r.Start()
	if _, err := r.Write("hello "); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Write("world"); err != nil {
		t.Fatal(err)
	}
	r.Done()

	out := buf.String()
	// header 被写过
	if !strings.Contains(out, headerText) {
		t.Fatalf("header not written: %q", out)
	}
	// token 被写过
	if !strings.Contains(out, "hello world") {
		t.Fatalf("tokens missing: %q", out)
	}
	// 必须有 erase line 转换点
	if !strings.Contains(out, ansiEraseLine) {
		t.Fatalf("no erase line transition: %q", out)
	}
	// 最后必须有换行
	if out[len(out)-1] != '\n' {
		t.Fatalf("expected trailing \\n, got %q", out[len(out)-1:])
	}
}

func TestRenderer_EmptyDeltaNoOp(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf)
	r.Start()
	headerLen := buf.Len()
	n, err := r.Write("")
	if err != nil || n != 0 {
		t.Fatalf("empty write: n=%d err=%v", n, err)
	}
	if buf.Len() != headerLen {
		t.Fatalf("empty write produced output: %q", buf.String()[headerLen:])
	}
	r.Done()
}

func TestRenderer_DoneWithoutTokens_ErasesHeader(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf)
	r.Start()
	r.Done()
	out := buf.String()
	// 有 header,但也应该有 erase line
	if !strings.Contains(out, ansiEraseLine) {
		t.Fatalf("erase missing: %q", out)
	}
	// 没有换行(我们是擦了 header,不额外加行)
	if strings.HasSuffix(out, "\n") && !strings.HasSuffix(out, "\r\n") {
		// 允许 \r\x1b[K... 我们不加 \n 是对的
		t.Fatalf("should not add \\n on empty done: %q", out)
	}
}

func TestRenderer_FailBeforeTokens(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf)
	r.Start()
	r.Fail("server 炸了")
	out := buf.String()
	if !strings.Contains(out, "server 炸了") {
		t.Fatalf("fail msg missing: %q", out)
	}
	if !strings.Contains(out, ansiRed) {
		t.Fatalf("red color missing: %q", out)
	}
	if !strings.Contains(out, ansiEraseLine) {
		t.Fatalf("should erase header: %q", out)
	}
}

func TestRenderer_FailAfterTokens(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf)
	r.Start()
	_, _ = r.Write("already said something")
	r.Fail("boom")
	out := buf.String()
	if !strings.Contains(out, "already said something") {
		t.Fatalf("token lost: %q", out)
	}
	if !strings.Contains(out, "boom") {
		t.Fatalf("fail msg missing: %q", out)
	}
}

func TestRenderer_ClosedIsIdempotent(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf)
	r.Start()
	_, _ = r.Write("x")
	r.Done()
	lenAfter := buf.Len()
	// 再调用 Done/Write/Fail 都应 no-op
	r.Done()
	_, _ = r.Write("y")
	r.Fail("ignored")
	if buf.Len() != lenAfter {
		t.Fatalf("closed renderer still wrote: added=%q", buf.String()[lenAfter:])
	}
}

func TestRenderer_WriteWithoutStart(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf)
	// 直接 Write,不 Start
	_, err := r.Write("direct")
	if err != nil {
		t.Fatal(err)
	}
	r.Done()
	if !strings.Contains(buf.String(), "direct") {
		t.Fatalf("direct write lost: %q", buf.String())
	}
}
