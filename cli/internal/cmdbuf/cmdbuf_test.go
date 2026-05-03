package cmdbuf

import (
	"testing"

	"termind/internal/osc133"
)

func TestBufferCapturesCommandText(t *testing.T) {
	var got Command
	buf := NewBuffer(1024, func(c Command) {
		got = c
	})

	buf.OnEvent(osc133.Event{Kind: osc133.EventCommandStart, Command: "go test ./..."})
	_, _ = buf.Write([]byte("boom\n"))
	buf.OnEvent(osc133.Event{Kind: osc133.EventCommandEnd, Exit: 1})

	if got.Text != "go test ./..." {
		t.Fatalf("Text=%q, want command", got.Text)
	}
	if got.Exit != 1 {
		t.Fatalf("Exit=%d, want 1", got.Exit)
	}
	if string(got.Tail) != "boom\n" {
		t.Fatalf("Tail=%q, want boom", got.Tail)
	}
}
