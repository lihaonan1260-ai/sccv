// SPDX-License-Identifier: MIT

package i18n

// en dictionary, CLI domain: the 90 flag help descriptions of config.go
// registerFlags (87) + registerConfigControlFlags (3), owned by T-021, plus
// the main.go help text keys owned by T-005 (Long head/tail and the Example
// block; the en values are the exact scc 4.1.0 originals migrated verbatim).
// Key = the Chinese help text (shown verbatim under zh), value = the exact
// scc 4.1.0 English text migrated verbatim (restored under en so that
// `--lang en --help` matches the original output line by line).

func init() {

	RegisterDict("en", map[string]string{
		"计算每行最大与平均字符数":                      "calculate max and mean characters per line",
		"在输出中包含百分比数值":                       "include percentage values in output",
		"计算项目的唯一代码行数（ULOC）":                 "calculate the number of unique lines of code (ULOC) for the project",
		"计算项目的 DRYness（同时开启 --uloc）":        "calculate the DRYness of the project (implies --uloc)",
		"计算认知复杂度（按嵌套深度加权）":                  "calculate cognitive (nesting-weighted) complexity",
		"关闭二进制文件检测":                         "disable binary file detection",
		"逐文件显示输出":                           "display output for every file",
		"启用 CI 输出模式（stdout 仅用 ASCII）":       "enable CI output settings where stdout is ASCII",
		"禁用 .ignore 文件逻辑":                   "disables .ignore file logic",
		"禁用 .sccignore 文件逻辑":                "disables .sccignore file logic",
		"禁用 .gitignore 文件逻辑":                "disables .gitignore file logic",
		"禁用 .gitmodules 文件逻辑":               "disables .gitmodules file logic",
		"设为允许将 .gitignore 与 .ignore 文件纳入统计": "set to allow .gitignore and .ignore files to be counted",
		"将无法识别语言的文件以「Unknown」类别按普通文本统计":     "count files with an unrecognised language under an \"Unknown\" category as plain text",
		"指定额外的 gitignore 格式忽略文件路径，自扫描根目录起生效；可重复添加，后出现的文件与树内 ignore 文件优先级更高": "path to an additional gitignore-format ignore file, applied from the scan root; repeat to add more, later files and any in-tree ignore files take precedence",
		"开启调试输出":                           "enable debug output",
		"要排除的目录":                           "directories to exclude",
		"解析多少个文件后开启 GC":                    "number of files to parse before turning the GC on",
		"已发现且待读入内存的文件队列大小":                 "the size of the queue of files found and ready to be read into memory",
		"处理文件并收集统计信息的 goroutine worker 数量": "number of goroutine workers that process files collecting stats",
		"实验性功能，可能变更或移除：对存在专用扫描器的语言（当前为 C、C Header 与 Java）使用该扫描器统计":                                                        "experimental, may change or be removed: count with the scanner written for the file's language where there is one, currently C, C Header and Java",
		"将遍历到的路径转为计数作业的 goroutine worker 数量":                                                                              "number of goroutine workers that turn a walked path into a job to be counted",
		"格式化前暂存已处理文件统计信息的队列大小":                                                                                            "the size of the queue used to hold processed file statistics before formatting",
		"控制遍历目录树的最大 worker 数量":                                                                                            "controls the maximum number of workers which will walk the directory tree",
		"设置输出格式 [tabular, wide, json, json2, csv, csv-stream, cloc-yaml, html, html-table, sql, sql-insert, openmetrics]": "set output format [tabular, wide, json, json2, csv, csv-stream, cloc-yaml, html, html-table, sql, sql-insert, openmetrics]",
		"写出自包含的 HTML 报告；裸旗标写入 scc-report.html 并在覆盖前询问，--report=path/out.html 静默覆盖":                                        "write a self-contained HTML report; bare flag writes scc-report.html and prompts before overwriting, --report=path/out.html overwrites silently",
		"要省略的小节（逗号分隔）(cocomo,locomo,hotspots,coupling,authors,timeline,files,uloc,linelength,card)":                       "comma-separated sections to omit (cocomo,locomo,hotspots,coupling,authors,timeline,files,uloc,linelength,card)",
		"覆盖报告横幅中显示的仓库名称":                                                                                                  "override the repo name shown in the report banner",
		"限定文件扩展名 [逗号分隔列表，例如 go,java,js]":                                                                                  "limit to file extensions [comma separated list: e.g. go,java,js]",
		"忽略文件扩展名（覆盖 include-ext）[逗号分隔列表，例如 go,java,js]":                                                                   "ignore file extensions (overrides include-ext) [comma separated list: e.g. go,java,js]",
		"忽略名称匹配的文件":                                                                                            "ignore files with matching names",
		"打印支持的语言及扩展名":                                                                                          "print supported languages and extensions",
		"用于基础 COCOMO 计算的平均工资":                                                                                  "average wage value used for basic COCOMO calculation",
		"设置企业开销（场地、设备、财务等）的 overhead 乘数":                                                                       "set the overhead multiplier for corporate overhead (facilities, equipment, accounting, etc.)",
		"由成本驱动因子导出的工作量调整系数（评级为 nominal 时为 1.0）":                                                                "the effort adjustment factor derived from the cost drivers (1.0 if rated nominal)",
		"输出更接近 SLOCCount 风格的 COCOMO 计算":                                                                        "print a more SLOCCount like COCOMO calculation",
		"不输出 COCOMO 计算结果":                                                                                      "remove COCOMO calculation output",
		"更改 COCOMO 模型类型 [organic, semi-detached, embedded, 「custom,1,1,1,1」]":                                  "change COCOMO model type [organic, semi-detached, embedded, \"custom,1,1,1,1\"]",
		"不输出规模（size）计算结果":                                                                                      "remove size calculation output",
		"去除小节之间的水平边框":                                                                                          "remove horizontal borders between sections",
		"设置规模单位 [si, binary, mixed, xkcd-kb, xkcd-kelly, xkcd-imaginary, xkcd-intel, xkcd-drive, xkcd-bakers]": "set size unit [si, binary, mixed, xkcd-kb, xkcd-kelly, xkcd-imaginary, xkcd-intel, xkcd-drive, xkcd-bakers]",
		"跳过代码复杂度计算":                                                                                            "skip calculation of code complexity",
		"从统计与输出中去除重复文件":                                                                                        "remove duplicate files from stats and output",
		"识别压缩（minified）或生成（generated）文件":                                                                       "identify minified or generated files",
		"识别压缩（minified）文件":                                                                                     "identify minified files",
		"识别生成（generated）文件":                                                                                    "identify generated files",
		"生成文件头部的标记字符串":                                                                                         "string markers in head of generated files",
		"在输出中忽略压缩或生成文件（同时开启 --min-gen）":                                                                        "ignore minified or generated files in output (implies --min-gen)",
		"在输出中忽略压缩文件（同时开启 --min）":                                                                               "ignore minified files in output (implies --min)",
		"在输出中忽略生成文件（同时开启 --gen）":                                                                               "ignore generated files in output (implies --gen)",
		"判定为压缩或生成文件的平均每行字节数阈值":                                                                                 "number of bytes per average line for file to be considered minified or generated",
		"忽略匹配正则表达式的文件与目录":                                                                                      "ignore files and directories matching regular expression",
		"输出文件名（默认 stdout）":                                                                                     "output filename (default stdout)",
		"排序依据的列 [files, name, lines, blanks, code, comments, complexity]":                                      "column to sort by [files, name, lines, blanks, code, comments, complexity]",
		"开启跟踪输出（处理多个文件时不建议使用）":                                                                                 "enable trace output (not recommended when processing multiple files)",
		"输出详细日志":                                                                                               "verbose output",
		"更宽的输出，包含额外统计信息（同时开启 --complexity）":                                                                    "wider output with additional statistics (implies --complexity)",
		"忽略超过 large-line-count 与 large-byte-count 所设行数/字节上限的文件":                                                "ignore files over certain byte and line size set by large-line-count and large-byte-count",
		"设置后将统计符号链接文件":                                                                                         "if set will count symlink files",
		"文件可包含的最大行数，超过后从输出中移除":                                                                                 "number of lines a file can contain before being removed from output",
		"文件可包含的最大字节数，超过后从输出中移除":                                                                                "number of bytes a file can contain before being removed from output",
		"将扩展名按指定语言统计 [例如 jsp:htm,chead:「C Header」将扩展名 jsp 映射为 html，chead 映射为 C Header]":                                                         "count extension as language [e.g. jsp:htm,chead:\"C Header\" maps extension jsp to html and chead to C Header]",
		"将匹配路径模式的文件按基础语言统计为新的命名类别 [可重复；模式默认为 glob，加 re: 前缀则为正则；例如 *_spec.rb:「Ruby Spec」:Ruby 或 re:\\.test\\.js$:「JavaScript Tests」:JavaScript]": "count files matching a path pattern as a new named category backed by a base language [repeatable; pattern is glob by default, prefix with re: for regex; e.g. *_spec.rb:\"Ruby Spec\":Ruby or re:\\.test\\.js$:\"JavaScript Tests\":JavaScript]",
		"以多种格式输出，覆盖 --format [例如 tabular:stdout,csv:file.csv,json:file.json]":                                                                   "have multiple format output overriding --format [e.g. tabular:stdout,csv:file.csv,json:file.json]",
		"使用给定名称作为本次运行的项目标识。仅在 --format sql 或 sql-insert 选项下有效":                                                                                  "use supplied name as the project identifier for the current run. Only valid with the --format sql or sql-insert option",
		"检查类型未知的文件，按字符串匹配重新映射语言 [例如 「-*- C++ -*-」:「C Header」]":                                                                                  "inspect files of unknown type and remap by checking for a string and remapping the language [e.g. \"-*- C++ -*-\":\"C Header\"]",
		"检查所有文件，按字符串匹配重新映射语言 [例如 「-*- C++ -*-」:「C Header」]":                                                                                     "inspect every file and remap by checking for a string and remapping the language [e.g. \"-*- C++ -*-\":\"C Header\"]",
		"设置货币符号": "set currency symbol",
		"启用 LOCOMO（LLM Output COst MOdel）成本估算":                                                 "enable LOCOMO (LLM Output COst MOdel) cost estimation",
		"并排显示 COCOMO 与 LOCOMO 估算结果":                                                            "show both COCOMO and LOCOMO estimates side by side",
		"LOCOMO 模型预设 [large, medium, small, local]":                                            "LOCOMO model preset [large, medium, small, local]",
		"LOCOMO 估算中每行代码的人工评审分钟数":                                                               "human review minutes per line of code for LOCOMO estimate",
		"LOCOMO 高级配置 「tokensPerLine,inputPerLine,complexityWeight,iterations,iterationWeight」": "LOCOMO power-user config \"tokensPerLine,inputPerLine,complexityWeight,iterations,iterationWeight\"",
		"LOCOMO 每百万输入 token 的美元成本（覆盖预设）":                                                       "LOCOMO cost per 1M input tokens in dollars (overrides preset)",
		"LOCOMO 每百万输出 token 的美元成本（覆盖预设）":                                                       "LOCOMO cost per 1M output tokens in dollars (overrides preset)",
		"LOCOMO 每秒输出 token 数（覆盖预设）":                                                            "LOCOMO output tokens per second (overrides preset)",
		"覆盖估算的 LLM 迭代轮数（默认：由复杂度计算得出）":                                                          "override estimated LLM iteration cycles (default: calculated from complexity)",
		"渲染热点报告（按复杂度 × 近期 git 历史变更频率对文件排名）":                                                    "render the hotspots report (files ranked by complexity × change frequency over recent git history)",
		"渲染变更耦合报告（近期 git 历史中倾向于一起变更的文件对）":                                                      "render the change-coupling report (file pairs that change together over recent git history)",
		"影响范围视图：给定文件路径，显示近期 git 历史中倾向于随之变更的文件":                                                 "blast-radius view: given a file path, show what tends to change with it over recent git history",
		"按文件复杂度为耦合加权，使复杂文件对排在生成/数据文件 churn 之上（同时开启 --coupling）":                                "weight coupling by file complexity so pairs of complex files rank above generated/data-file churn (implies --coupling)",
		"渲染作者汇总报告（近期 git 历史中的 bus factor 与最后修改者归属）":                                            "render the author rollup report (bus factor and last-toucher attribution over recent git history)",
		"git 历史报告的提交窗口大小；0 表示全部历史（大仓库可能较慢）":                                                    "commit window size for git history reports; 0 means entire history (large repos may be slow)",
		"渲染近期 git 历史的时间轴视图；配合 --by-author 运行作者时间轴，单独使用运行语言时间轴":                                 "render an over-time view of recent git history; with --by-author runs the author timeline, alone runs the languages timeline",
		"git 时间轴报告的时间分桶粒度":                                                                     "time-bucket resolution for the git timeline reports",
		"关闭 git 作者报告的「姓名+邮箱域」身份折叠回退（mailmap 仍然生效）":                                             "disable the name+email-domain identity folding fallback for git author reports (mailmap still applied)",
		"以 MCP（Model Context Protocol）服务器模式经 stdio 启动":                                         "start as an MCP (Model Context Protocol) server over stdio",
		"将该文件作为全局配置源加载；覆盖 SCC_CONFIG_PATH，即使使用 --no-config 也生效":                                "load this file as the global config source; overrides SCC_CONFIG_PATH, honored even with --no-config",
		"关闭 SCC_CONFIG_PATH 全局配置与项目 ./.sccconfig 配置的自动发现":                                      "disable auto-discovery of the SCC_CONFIG_PATH global and the project ./.sccconfig config",
		"通过向上遍历至仓库根目录来发现项目 .sccconfig，而非使用 ./.sccconfig":                                       "discover the project .sccconfig by walking up to the repository root instead of using ./.sccconfig",

		// main.go help text (T-005): Long is split around the dynamic version
		// number, so it travels as a head and a tail key; the Example block is
		// one key whose English value is the baseline raw string verbatim.
		"Sloc, Cloc and Code. 统计目录中的代码行数并估算复杂度。\n版本 ":                       "Sloc, Cloc and Code. Count lines of code in a directory with complexity estimation.\nVersion ",
		"\nBen Boyter <ben@boyter.org> 及贡献者\nhttps://github.com/boyter/scc": "\nBen Boyter <ben@boyter.org> + Contributors\nhttps://github.com/boyter/scc",
		`  统计当前目录：
    scc

  统计指定文件夹或文件：
    scc myproject/
    scc main.go

  一次统计多个路径：
    scc src/ docs/ README.md

  逐文件明细输出（默认按语言汇总）：
    scc --by-file

  以 CSV 或 JSON 输出（例如供进一步处理使用）：
    scc --format csv
    scc --format json -o counts.json

  把无法识别的扩展名按已知语言统计：
    scc --count-as jsp:html

  把匹配路径模式的文件按基础语言统计为新类别（默认 glob，可用 re: 前缀改为正则）：
    scc --count-as-pattern '*_spec.rb:Ruby Spec:Ruby'

  生成自包含的 HTML 信息图报告：
    scc --report
    scc --report=out.html --report-title "myrepo" --report-skip cocomo

  使用项目配置文件（./.sccconfig）或全局配置文件（优先级：全局 < 项目 < 命令行）：
    export SCC_CONFIG_PATH=~/.sccconfig
    scc --config team.sccconfig

  调整 COCOMO 成本估算参数，或关闭估算（见 https://en.wikipedia.org/wiki/COCOMO）：
    scc --avg-wage 75000 --cocomo-project-type semi-detached
    scc --no-cocomo`: `  Count the current directory:
    scc

  Count a specific folder or file:
    scc myproject/
    scc main.go

  Count several paths at once:
    scc src/ docs/ README.md

  Show a per-file breakdown instead of the per-language summary:
    scc --by-file

  Output as CSV or JSON (e.g. for further processing):
    scc --format csv
    scc --format json -o counts.json

  Count an unrecognised extension as a known language:
    scc --count-as jsp:html

  Count files matching a path pattern as a new category (glob by default):
    scc --count-as-pattern '*_spec.rb:Ruby Spec:Ruby'

  Generate a self-contained HTML infographic report:
    scc --report
    scc --report=out.html --report-title "myrepo" --report-skip cocomo

  Use a project config file (./.sccconfig) or a global one (precedence: global < project < CLI):
    export SCC_CONFIG_PATH=~/.sccconfig
    scc --config team.sccconfig

  Tune the COCOMO cost estimate, or turn it off (see https://en.wikipedia.org/wiki/COCOMO):
    scc --avg-wage 75000 --cocomo-project-type semi-detached
    scc --no-cocomo`,
	})
}
