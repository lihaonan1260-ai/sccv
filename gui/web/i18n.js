/* sccv 前端 i18n（T-011）
 * 机制与 Go 侧 i18n 包同构：中文原文作键；zh 恒等返回；en 查字典，缺键回退键。
 * 语言来源：app.js 经 GET /api/config 的 lang 字段 setLang；失败默认 zh（Q8 无页面内语言切换）。
 * 双挂载：浏览器 window.SCCV_I18N；node 经 module.exports（DEC-023 纯函数可测约定）。
 */
(function (global) {
  'use strict';

  var DICTS = {
    zh: {},
    en: {
      'sccv 代码统计': 'sccv Code Statistics',
      '退出': 'Exit',
      '分析目标': 'Analysis Target',
      '浏览文件夹': 'Browse Folder',
      '输入或粘贴文件夹路径，也可点击左侧按钮选择': 'Type or paste a folder path, or pick one with the button',
      '开始分析': 'Analyze',
      '包含未识别类型': 'Include unrecognized types',
      '最近分析': 'Recent',
      '清除历史': 'Clear history',
      '暂无历史记录': 'No history yet',
      '概览': 'Overview',
      '完成一次分析后，这里将呈现文件、代码行等总览指标。': 'After an analysis finishes, totals such as files and code lines will appear here.',
      '语言明细': 'Languages',
      '完成一次分析后，这里将按语言呈现行数与占比明细。': 'After an analysis finishes, per-language line counts and shares will appear here.',
      '分布图': 'Distribution',
      '完成一次分析后，这里将呈现语言分布饼图与环形图。': 'After an analysis finishes, the language pie and donut charts will appear here.',
      '文件明细': 'Files',
      '完成一次分析后，这里将呈现可排序、可搜索的文件下钻表。': 'After an analysis finishes, a sortable and searchable file drill-down table will appear here.',
      '导出': 'Export',
      'HTML 报告': 'HTML Report',
      /* DI-103 定性回传：导出跟随分析范围（引擎级），"筛选"本意=视图筛选（语言勾选）——文案随之精确化 */
      '导出当前分析范围内的全部数据（不含页面筛选）': 'Exports everything within the current analysis scope (page-level filters are not applied)',
      /* M-001/DEC-307：HTML 报告导出知情提示（与 index.html 导出区 data-i18n 键一致） */
      '导出的 HTML 报告标题可能包含由仓库 remote 地址派生的仓库名；勾选“包含 git 历史”时，报告将含提交作者（姓名/邮箱）等历史信息。报告仅在本机内存中生成，服务端不做任何持久化。': 'The exported HTML report title may include a repository name derived from the repo remote URL; if "Include git history" is checked, the report will also contain history details such as commit authors (names/emails). The report is generated in local memory only — the server does not persist anything.',
      '分析中…': 'Analyzing…',
      '分析完成': 'Analysis complete',
      '分析失败': 'Analysis failed',
      '请求失败': 'Request failed',
      '请先输入要分析的文件夹路径': 'Enter a folder path to analyze first',
      '无法连接后端服务，请确认 sccv 正在运行': 'Cannot reach the backend service. Make sure sccv is running.',
      '目录选择器不可用（后端不可达），请手动输入路径': 'The folder picker is unavailable (backend unreachable). Type the path manually.',
      /* BG-003：浏览进行时提示 / 分流话术 / 退出话术（与 app.js 常量逐字节一致） */
      '正在打开目录选择对话框，请在弹窗中选择（首次约 2-3 秒；若未出现请检查任务栏）': 'Opening the folder picker dialog — choose a folder in the popup (first open takes 2-3 s; if it does not show up, check the taskbar).',
      '目录选择对话框打开失败，请手动输入路径': 'The folder picker dialog failed to open. Type the path manually.',
      '服务已退出，请重新启动 sccv.exe': 'The service has exited. Please restart sccv.exe.',
      '已有分析任务在进行中，请稍候': 'An analysis task is already running. Please wait.',
      '已退出，可以安全关闭此页面': 'Exited. It is safe to close this page.',
      /* BG-004：页面生命周期轻提示（与 index.html data-i18n 键一致；静态节点，
         语言切换经 applyI18n 重译，无需 JS 参与文案） */
      '关闭此页面后，程序将在约 30 秒内自动退出；也可随时点击右上角退出': 'After you close this page, the program exits automatically within about 30 seconds. You can also click Exit at the top right at any time.',
      /* T-301/OPT-003：统计口径说明卡（index.html 骨架静态键 + explain.js 动态键，zh 原文即键） */
      '统计口径说明': 'Statistical Scope Notes',
      '各语言统计规则': 'Per-language counting rules',
      '未计入统计的内容': 'What is not counted',
      '完成一次分析后，这里将按语言展示注释符号等统计规则。': 'After an analysis finishes, the comment symbols and other counting rules of each language will appear here.',
      '完成一次分析后，这里将说明哪些内容未计入统计。': 'After an analysis finishes, an explanation of what is not counted will appear here.',
      '单行注释': 'Single-line comments',
      '多行注释': 'Multi-line comments',
      '复杂度检查项': 'Complexity checks',
      '关键词': 'Keywords',
      '无': 'None',
      '等共 {a} 个': 'and {a} in total',
      '{a} 条': '{a}',
      '说明数据加载中…': 'Loading rule details…',
      '说明数据加载失败': 'Rule details are unavailable',
      '未识别类型文件': 'Unrecognized-type files',
      '二进制文件': 'Binary files',
      '被排除的目录与文件类型': 'Excluded directories and file types',
      '引擎按扩展名判别语言失败时，文件默认不计入统计。': 'Files whose language cannot be identified from the extension are not counted by default.',
      '本次分析计入 Unknown 类 {a} 个文件。': 'This analysis counted {a} file(s) under Unknown.',
      '本次分析未勾选“包含未识别类型”，此类文件未计入。': '"Include unrecognized types" was not checked for this analysis, so such files are not counted.',
      '查看文件明细中的 Unknown 行': 'Show the Unknown rows in the file details',
      '如何调整：在分析目标勾选“包含未识别类型”后重新分析。': 'To adjust: check "Include unrecognized types" under Analysis Target and analyze again.',
      'scc 检测到二进制特征的文件不计入统计。': 'Files that scc detects as binary are not counted.',
      '具体数量引擎未统计。': 'The engine does not track exact counts of skipped files.',
      '分析只统计目标目录内未被排除的文本文件；引擎默认排除 {a} 目录。': 'Only text files inside the target directory that are not excluded are counted; the engine always excludes the {a} directories.',
      '本次分析的排除目录：{a}': 'Excluded directories of this analysis: {a}',
      '本次分析未指定额外排除目录。': 'No additional excluded directories were specified for this analysis.',
      '本次分析的文件类型筛选：{a}': 'File-type filter of this analysis: {a}',
      '如何调整：在分析向导中调整排除目录与文件类型后重新分析。': 'To adjust: change the excluded directories and file types in the analysis wizard and analyze again.',
      /* OPT-005（T-102）：语言明细表列头问号悬浮 7 键（键=中文文案，zh 恒等；文案与 stats.js LANG_COLUMNS hint 逐字一致） */
      '该语言统计到的文件个数。': 'Number of files identified for this language.',
      '该语言全部文件的总行数（代码+注释+空白）。': 'Total lines across all files of this language (code + comments + blanks).',
      '被计为代码的行数。': 'Lines counted as code.',
      '被计为注释的行数。': 'Lines counted as comments.',
      '空行行数。': 'Blank lines.',
      '圈复杂度（scc 口径）：按关键字与分支计数。': 'Cyclomatic complexity (scc rules): counted from keywords and branches.',
      '该语言行数占全部行数的百分比。': 'Percentage of total lines contributed by this language.',
      /* OPT-001/002（T-201/T-202）：两步向导（zh 原文即键；动态计数文案按段组合拼装） */
      '选择分析范围': 'Choose Analysis Scope',
      '第 1 步，共 2 步': 'Step 1 of 2',
      '第 2 步，共 2 步': 'Step 2 of 2',
      '收起': 'Collapse',
      '常用排除': 'Common exclusions',
      '直接分析（全部包含）': 'Analyze everything directly',
      '下一步': 'Next',
      '上一步': 'Back',
      '重置': 'Reset',
      '目录树加载中…': 'Loading directory tree…',
      '目录树加载失败': 'Failed to load the directory tree',
      '目录较多，部分深层目录未展示': 'Large directory tree — some deeper directories are not listed.',
      '.git 目录由引擎默认排除，无需手动排除': '.git is already excluded by the engine by default — no manual exclusion needed.',
      '分析根目录（始终包含）': 'Analysis root (always included)',
      '收起子目录': 'Collapse subdirectories',
      '展开子目录': 'Expand subdirectories',
      '已排除': 'Excluded',
      '个目录': 'directories',
      '全部包含，未排除任何目录': 'Everything included — no directories excluded.',
      '文件类型加载中…': 'Loading file types…',
      '文件类型加载失败': 'Failed to load file types',
      '文件较多，仅统计前 5 万个文件的扩展名': 'Too many files — extensions are counted for the first 50,000 files only.',
      '不限': 'No limit',
      '仅分析勾选类型': 'Analyze only the checked types',
      '排除勾选类型': 'Exclude the checked types',
      '全选': 'Select all',
      '反选': 'Invert',
      '无扩展名文件': 'Extension-less files',
      '个': 'in total',
      '已按': 'Filtered by',
      '种类型筛选': 'selected types',
      '种类型': 'types'
    }
  };

  var currentLang = 'zh';

  function hasDict(lang) {
    return Object.prototype.hasOwnProperty.call(DICTS, lang);
  }

  function setLang(lang) {
    currentLang = hasDict(lang) ? lang : 'zh';
    return currentLang;
  }

  function getLang() {
    return currentLang;
  }

  function t(key) {
    if (currentLang === 'en' && hasDict('en') && Object.prototype.hasOwnProperty.call(DICTS.en, key)) {
      return DICTS.en[key];
    }
    return key; // zh 恒等；en 缺键回退中文原文
  }

  function registerDict(lang, dict) {
    if (!lang || !dict || typeof dict !== 'object') return false;
    if (!hasDict(lang)) DICTS[lang] = {};
    var target = DICTS[lang];
    for (var k in dict) {
      if (Object.prototype.hasOwnProperty.call(dict, k)) target[k] = dict[k];
    }
    return true;
  }

  var SCCV_I18N = {
    setLang: setLang,
    getLang: getLang,
    t: t,
    registerDict: registerDict
  };

  if (typeof module !== 'undefined' && module.exports) module.exports = SCCV_I18N;
  if (typeof global !== 'undefined') global.SCCV_I18N = SCCV_I18N;
})(typeof window !== 'undefined' ? window : globalThis);
