// SPDX-License-Identifier: MIT

package i18n

// en 字典 misc 域：html/html-table 输出的 <th> 表头（T-023 白名单域）。
// 键=中文原文（zh 模式恒等显示），值=scc 4.1.0 原英文文案逐字迁入
// （en 模式还原，保证既有测试零改动与 --lang en 字节级回退）。

func init() {
	RegisterDict("en", map[string]string{
		"语言":    "Language",
		"文件数":   "Files",
		"行数":    "Lines",
		"空行":    "Blank",
		"注释":    "Comment",
		"代码":    "Code",
		"复杂度":   "Complexity",
		"字节":    "Bytes",
		"唯一代码行": "Uloc",
	})
}
