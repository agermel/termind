package enrich

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCollectFillsBasics(t *testing.T) {
	// cwd 给本仓库根 (通过回溯 go.mod 找到 cli 目录的父级).
	cwd := repoRoot(t)

	ctx := Collect(context.Background(), cwd)

	if ctx.User == "" {
		t.Errorf("User should not be empty (USER env or home dir should provide one)")
	}
	if ctx.Project == "" {
		t.Errorf("Project should not be empty for a real cwd")
	}
	if ctx.OS == "" {
		t.Errorf("OS should not be empty; got %q", ctx.OS)
	}
	// 在本仓库里跑测试时 git 应该总是可用
	if ctx.Branch == "" && ctx.GitCommit == "" {
		t.Errorf("Branch/GitCommit both empty — expected git metadata in a git repo, got %+v", ctx)
	}
}

func TestCollectEmptyCwdOnlyFillsProcessFields(t *testing.T) {
	ctx := Collect(context.Background(), "")

	if ctx.Project != "" {
		t.Errorf("Project should be empty when cwd is empty; got %q", ctx.Project)
	}
	if ctx.Branch != "" || ctx.GitCommit != "" {
		t.Errorf("Git fields should be empty when cwd is empty; got Branch=%q Commit=%q", ctx.Branch, ctx.GitCommit)
	}
	// 进程字段仍应尝试填充
	if ctx.OS == "" {
		t.Errorf("OS should still be populated from runtime/uname; got empty")
	}
}

func TestCollectNonGitDirUsesCwdBasename(t *testing.T) {
	tmp := t.TempDir()
	// 明确不是 git 仓库 (TempDir 不含 .git)
	ctx := Collect(context.Background(), tmp)

	want := filepath.Base(tmp)
	if ctx.Project != want {
		t.Errorf("Project fallback should be cwd basename; got %q want %q", ctx.Project, want)
	}
	if ctx.Branch != "" || ctx.GitCommit != "" {
		t.Errorf("Git fields should be empty outside git repo; got Branch=%q Commit=%q", ctx.Branch, ctx.GitCommit)
	}
}

func TestCurrentUserFallsBackToHomeDir(t *testing.T) {
	// 不破坏全局 env (t.Setenv 在 t.Cleanup 里还原)
	t.Setenv("USER", "")
	t.Setenv("LOGNAME", "")

	got := currentUser()
	if got == "" {
		t.Errorf("currentUser should fallback to home dir basename; got empty")
	}
}

func TestParseGoModVersion(t *testing.T) {
	dir := t.TempDir()
	content := "module example.com/foo\n\ngo 1.22.3 // toolchain hint\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(content), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	got := goModVersion(dir)
	if got != "go1.22.3" {
		t.Errorf("goModVersion=%q, want go1.22.3", got)
	}
}

func TestGoModVersionWalksUpward(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n\ngo 1.20\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	got := goModVersion(nested)
	if got != "go1.20" {
		t.Errorf("goModVersion=%q, want go1.20 from parent dir", got)
	}
}

func TestOSDescriptionStartsWithGOOS(t *testing.T) {
	ctx := context.Background()
	got := osDescription(ctx)
	if got == "" {
		t.Fatal("osDescription returned empty")
	}
	if !startsWithGOOS(got) {
		t.Errorf("osDescription=%q should start with runtime.GOOS=%q", got, runtime.GOOS)
	}
}

func startsWithGOOS(s string) bool {
	g := runtime.GOOS
	if len(s) < len(g) {
		return false
	}
	return s[:len(g)] == g
}

// repoRoot 用 runtime.Caller 推断 cli 目录, 再回溯到仓库根.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// file = .../cli/internal/enrich/enrich_test.go → 仓库根 = file/../../../..
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}
