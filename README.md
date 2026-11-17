# sccv —— 代码行数统计工具

基于开源工具 [scc 4.1.0](https://github.com/boyter/scc)（Ben Boyter，MIT）二次开发的**图形化中文版代码行数统计工具**：单文件便携 exe，双击即用，自动打开浏览器完成分析，零安装、零外部依赖、离线可用。

## 功能特性

- **两步分析向导**
  - 第一步：目录树层级展示（不限深度、全量入树），三态勾选——勾选纳入分析、取消即排除整棵子树，支持常用排除快捷项（node_modules/.git/target/dist）
  - 第二步：文件类型筛选——盘点所选范围内全部扩展名（含文件数），双模式（仅分析选中 / 排除选中），与 languages 通道互斥保护
- **全语言统计**：语言 / 文件数 / 行数 / 代码 / 注释 / 空白 / 圈复杂度 / 字节，统计口径与 scc 4.1.0 完全一致；未识别类型可选纳入
- **可视化呈现**：概览卡片 + 语言明细表（排序/搜索/分页）+ 语言分布图（饼图↔环形切换，随筛选联动），列头问号悬浮展示各字段计算逻辑
- **结果导出**：CSV（UTF-8 BOM，Excel 直开不乱码）/ JSON / HTML 中文报告（自包含单文件，离线可打开）
- **统计口径说明**：按语言展示注释符号与复杂度规则，"未计入统计"（二进制/未识别/被排除）如实说明
- **运行日志**：exe 同目录 `logs/` 下按日滚动 JSONL，记录每次分析的过程与结果摘要（不含 token 与文件内容，写失败静默降级）
- **全面汉化**：GUI / CLI 帮助 / 终端表头 / HTML 报告默认简体中文，`--lang en` 一键回退英文
- **便携安全**：单 exe 双击启动（无控制台黑窗）、仅监听 127.0.0.1 随机端口、token 门禁、关闭网页后约 15~45 秒自动退出（分析进行中会等分析完成后退出）

## 快速开始

### 运行

1. 从 [Releases](https://github.com/lihaonan1260-ai/sccv/releases) 下载（或自行构建）`代码行数统计.exe`
2. 双击运行——默认浏览器自动打开本地页面（仅监听 127.0.0.1，随机端口 + token 防护）
3. 选择文件夹 → （可选）勾选目录与文件类型 → 开始分析 → 查看/导出
4. 关闭网页后程序将在约 15~45 秒内自动退出；也可随时点击页面右上角「退出」立即结束

### 命令行形态

无参数 = 图形界面；**其余参数保持 scc 4.1.0 原语义**（分析当前目录/指定目录、`--by-file`、`--format csv/json` 等全部可用），并额外支持：

| 参数/环境变量 | 说明 |
|---|---|
| `--lang en` / `SCCV_LANG` | 界面与输出语言回退英文（默认中文） |
| `--port N` / `SCCV_PORT` | 指定图形界面端口（默认随机） |
| `SCCV_NO_BROWSER=1` | 启动时不自动打开浏览器 |

## 从源码构建

要求：**Go 1.26 及以上**（Windows）。

```powershell
# 1.（仅首次/图标或版本信息变更后）生成 Windows 资源对象（图标+版本信息）
#    工具：go install github.com/josephspurrier/goversioninfo/cmd/goversioninfo@v1.7.0
goversioninfo -o rsrc_windows_amd64.syso versioninfo.json

# 2. 构建（rsrc_windows_amd64.syso 会被自动链接，图标随 exe 携带）
go build -trimpath -ldflags "-s -w -H=windowsgui" -o dist/代码行数统计.exe .
```

- 图标源文件：`assets/icon.ico`；版本号/产品名：`versioninfo.json`
- `vendor/` 已随仓库提供，支持完全离线构建（`-mod=vendor` 默认生效）

## 目录结构

```
├── main.go / config.go        # 入口：双形态分流（无参=GUI，其余=CLI 原语义）+ 参数定义
├── console_windows.go         # GUI 子系统下的控制台回接（CLI 输出）
├── gui/                       # Web 服务与生命周期（SSE 页面在场检测/自动退出）
│   ├── logger/                #   按日滚动 JSONL 运行日志
│   └── web/                   #   前端单页（原生 JS，零构建零外部依赖，go:embed 内嵌）
├── i18n/                      # 轻量双语层（中文原文作键，en 字典回退）
├── processor/                 # 统计引擎（继承自 scc 4.1.0，白名单内仅展示层汉化）
├── vendor/                    # 依赖（离线构建）
├── assets/icon.ico            # 应用图标
├── versioninfo.json           # Windows 版本信息与图标配置
└── scripts/build-sccv.ps1     # 构建脚本
```

## 统计口径说明

- 语言识别、注释/空白/复杂度统计与 scc 4.1.0 完全一致（引擎未做任何修改）
- 未识别扩展名的文件默认不计入，可在向导中勾选「包含未识别类型」纳入（按 Unknown 呈现）
- 二进制文件自动跳过；`.git`/`.hg`/`.svn` 默认排除

## 许可证

本项目遵循 [MIT License](LICENSE)（继承自 scc）。
感谢 [Ben Boyter](https://github.com/boyter/scc) 的 [scc](https://github.com/boyter/scc) 项目——本工具的统计引擎与语言规则均来自该项目。
