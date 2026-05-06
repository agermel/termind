package enrich

import (
	"context"
	"os/exec"
	"strings"
)

// kernelRelease 返回 `uname -r` 的输出, 例如:
//
//	macOS:  "24.0.0"
//	Linux:  "5.15.0-105-generic"
//
// darwin 和 linux 都有 uname, Windows 则没有 — 我们只保底返回 ("", false).
func kernelRelease(parent context.Context) (string, bool) {
	ctx, cancel := context.WithTimeout(parent, stepTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "uname", "-r").Output()
	if err != nil {
		return "", false
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return "", false
	}
	return s, true
}
