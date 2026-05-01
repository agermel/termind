// termind 命令行入口。
//
// 实际逻辑在 cmd 包,本文件只负责把控制权交给 Cobra。
package main

import "termind/cmd"

func main() {
	cmd.Execute()
}
