// SPDX-License-Identifier: MIT

package i18n

// en 字典 tabular 域：终端表格表头/合计/Percentage/ULOC/DRYness 行文案（T-022 白名单域）。
// 键=中文原文（zh 模式恒等显示），值=scc 4.1.0 原英文逐字迁入（en 为包级默认语言，
// 本字典保证既有测试零改动与 en 输出对 baseline 字节级还原）。
//
// 键空间与 dict_en_misc.go 不重叠：原英文相同的列概念复用同键同值（语言/文件数/行数/
// 代码/复杂度，first-wins 下无论注册顺序结果一致）；原英文不同者（tabular 的 Blanks/
// Comments 对 html th 的 Blank/Comment）改用独立中文键（空行数/注释数），避免抢注。
//
// 宽度约束：各键的 zh 显示宽度（runewidth 单元格）不得超出其 en 值在行内保留的
// 位宽（调用点按 en 原文宽度填充，见 processor/formatters_tabular.go），
// 否则 zh 行内数值列右移。
func init() {
	RegisterDict("en", map[string]string{
		"语言":           "Language",
		"文件数":          "Files",
		"行数":           "Lines",
		"空行数":          "Blanks",
		"注释数":          "Comments",
		"代码":           "Code",
		"复杂度":          "Complexity",
		"认知复杂度":        "Cognitive",
		"复杂度/行数":       "Complexity/Lines",
		"合计":           "Total",
		"百分比":          "Percentage",
		"(唯一代码行)":      "(ULOC)",
		"唯一代码行 (ULOC)": "Unique Lines of Code (ULOC)",
		"去重率":          "DRYness",
	})
}
