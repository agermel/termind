package enrich

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// goModVersion 向上寻找 cwd 所在目录链上的 go.mod, 解析其中的 `go <version>` 行,
// 返回 "go1.22.3" 形式. 没找到或解析失败时返回 "".
//
// 只读文件且最多向上 8 层, 不跨网络, 不触发 go 工具链.
func goModVersion(cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		return ""
	}
	dir, err := filepath.Abs(cwd)
	if err != nil {
		return ""
	}
	// 最多向上 8 层目录防御性地避免无限循环 (根目录 "/" 的 parent 还是 "/").
	for i := 0; i < 8; i++ {
		candidate := filepath.Join(dir, "go.mod")
		if v := parseGoModVersion(candidate); v != "" {
			return v
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
	return ""
}

// parseGoModVersion 打开 go.mod, 抓第一条 `go <version>` 行, 返回 "go<version>".
func parseGoModVersion(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	// go.mod 单行不会很长, 16KB 够用; 防止被异常文件撑爆内存.
	scanner.Buffer(make([]byte, 0, 1024), 16*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "go ") {
			continue
		}
		ver := strings.TrimSpace(strings.TrimPrefix(line, "go "))
		if ver == "" {
			continue
		}
		// 去掉可能的 "// 注释" 后缀
		if i := strings.Index(ver, "//"); i >= 0 {
			ver = strings.TrimSpace(ver[:i])
		}
		if ver == "" {
			continue
		}
		return "go" + ver
	}
	return ""
}
