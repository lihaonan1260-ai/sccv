/* sccv 前端文件下钻视图（T-014 · F8）
 * 范围：文件明细表（路径/语言/行数/代码/注释/空白/复杂度/大小）+ 点列头排序（升降切换）
 *       + 搜索框（输入即过滤，300ms 防抖）+ 分页控件（上一页/下一页/页码/总数）
 *       + 语言筛选联动（消费 T-013 广播的筛选状态，过滤 files[].language）
 *       + 筛选集聚合校验和（与概览卡片数字一致，RD-008②）。
 * 数据：排序/筛选/分页/聚合全部来自 gui/web/lib/lib_files.js 纯函数；只渲染当前页（≤100 行）。
 * 接缝（与 T-012 先落盘接缝同构）：
 *   ① app.js pollJob → global.SCCV_STATS.renderSnapshot（T-012 接缝）——本视图加载后桥接包装，
 *      同一快照旁路进 renderSnapshot（不改 app.js/stats.js 一行）；
 *   ② 直连面：global.SCCV_FILES.renderSnapshot(job, rootPath)；
 *   ③ 语言筛选：document 'sccv:filterchange' CustomEvent（detail.languages: string[]|null）
 *      或直连 global.SCCV_FILES.setLanguageFilter(languages)。
 * 安全纪律（DEC-026）：仅 textContent / createElement / setAttribute / className 安全文本 API，
 *       标记拼接型写入一律禁用；无外链；样式走 style.css 追加段（T-014 段）。
 * i18n：中文原文作键；本视图新增键的 en 字典经 SCCV_I18N.registerDict 在此注册（不动 i18n.js）。
 */
(function (global) {
  'use strict';

  if (typeof window === 'undefined' || typeof document === 'undefined') return;

  /* ---------- i18n：本视图新增键的 en 字典（缺键回退中文原文；与 T-012 重复键取同值，注册幂等） ---------- */

  var EN_DICT = {
    '路径': 'Path',
    '语言': 'Language',
    '行数': 'Lines',
    '代码': 'Code',
    '注释': 'Comment',
    '空白': 'Blank',
    '复杂度': 'Complexity',
    '大小': 'Size',
    '文件数': 'Files',
    '总行数': 'Total Lines',
    '搜索文件路径': 'Search file paths',
    '上一页': 'Prev',
    '下一页': 'Next',
    '第 {a} / {b} 页 · 共 {c} 个文件': 'Page {a} / {b} · {c} files',
    '第 {a} / {b} 页 · 匹配 {c} 个文件': 'Page {a} / {b} · {c} matching files',
    '没有匹配的文件': 'No matching files',
    '当前快照无文件明细': 'The current snapshot has no file details'
  };

  if (global.SCCV_I18N && typeof global.SCCV_I18N.registerDict === 'function') {
    global.SCCV_I18N.registerDict('en', EN_DICT);
  }

  /* ---------- 常量与状态 ---------- */

  var PAGE_SIZE = 100;      /* 每页行数：大表只渲染当前页 */
  var SEARCH_DEBOUNCE_MS = 300;

  var COLUMNS = [
    { key: 'path', label: '路径', num: false },
    { key: 'language', label: '语言', num: false },
    { key: 'lines', label: '行数', num: true },
    { key: 'code', label: '代码', num: true },
    { key: 'comment', label: '注释', num: true },
    { key: 'blank', label: '空白', num: true },
    { key: 'complexity', label: '复杂度', num: true },
    { key: 'bytes', label: '大小', num: true }
  ];

  var state = {
    files: null,            /* 快照 files[]（null=尚无快照） */
    languageFilter: null,   /* T-013 广播的语言筛选（null=无筛选；空数组归一为 null） */
    query: '',              /* 搜索框（视图局部，不参与卡片聚合口径） */
    sortKey: 'path',
    sortDir: 'asc',
    page: 1
  };

  var els = {};
  var searchTimer = null;
  var mounted = false;

  /* ---------- 基础助手 ---------- */

  function $(id) { return document.getElementById(id); }

  function t(key) {
    var api = global.SCCV_I18N;
    return api && typeof api.t === 'function' ? api.t(key) : key;
  }

  function interpolate(key, params) {
    var s = t(key);
    return s.replace(/\{(\w+)\}/g, function (m, k) {
      return params && Object.prototype.hasOwnProperty.call(params, k) ? String(params[k]) : m;
    });
  }

  function el(tag, cls, text) {
    var node = document.createElement(tag);
    if (cls) node.className = cls;
    if (text !== undefined) node.textContent = text;
    return node;
  }

  function clear(node) {
    if (!node) return;
    while (node.firstChild) node.removeChild(node.firstChild);
  }

  function libFiles() {
    return global.SCCV_LIB_FILES || global.SCCV_LIB || {};
  }

  /* 与概览卡片同一格式化口径（lib_stats 优先；缺失时本地兜底，语义一致） */
  function formatBytes(n) {
    var f = global.SCCV_LIB_STATS && typeof global.SCCV_LIB_STATS.formatBytes === 'function'
      ? global.SCCV_LIB_STATS.formatBytes : null;
    if (f) return f(n);
    var v = Number(n);
    if (!isFinite(v) || v < 0) v = 0;
    var units = ['B', 'KB', 'MB', 'GB', 'TB'];
    var u = 0;
    while (v >= 1024 && u < units.length - 1) { v /= 1024; u += 1; }
    return u === 0 ? Math.round(v) + ' B' : (Math.round(v * 10) / 10) + ' ' + units[u];
  }

  function displayLanguageName(name) {
    var f = global.SCCV_LIB_STATS && typeof global.SCCV_LIB_STATS.displayLanguageName === 'function'
      ? global.SCCV_LIB_STATS.displayLanguageName : null;
    if (f) return f(name);
    return name == null ? '' : String(name);
  }

  /* ---------- 接缝归一 ---------- */

  function normalizeLanguageFilter(languages) {
    if (!languages) return null;
    if (typeof languages === 'string') languages = [languages];
    if (!Array.isArray(languages)) return null;
    var seen = Object.create(null);
    var out = [];
    for (var i = 0; i < languages.length; i++) {
      var name = languages[i];
      if (typeof name === 'string' && name && !seen[name]) {
        seen[name] = true; /* 去重：广播列表重复项会导致并集管线重复计入同一文件 */
        out.push(name);
      }
    }
    return out.length ? out : null; /* 空数组=无筛选（与 lib_stats.filterLanguages 口径一致） */
  }

  /* ---------- 数据管线：语言筛选（共享口径）→ 搜索（视图局部）→ 排序 → 分页 ---------- */

  function derived() {
    var lib = libFiles();
    var all = Array.isArray(state.files) ? state.files : [];
    /* 语言筛选（T-013 广播为多选数组）：filterFiles 契约 language 为单值精确匹配，
     * 多选=逐语言过滤后并集（sumFiles 与排序均与顺序无关） */
    var base;
    if (typeof lib.filterFiles !== 'function') {
      base = all.slice(); /* lib 缺失时兜底：仍能以原序渲染（理论不可达：lib 先于本文件加载） */
    } else if (!state.languageFilter) {
      base = lib.filterFiles(all, {});
    } else {
      base = [];
      for (var i = 0; i < state.languageFilter.length; i++) {
        base = base.concat(lib.filterFiles(all, { language: state.languageFilter[i] }));
      }
    }
    var searched = base;
    if (state.query) {
      searched = typeof lib.filterFiles === 'function'
        ? lib.filterFiles(base, { query: state.query })
        : base;
    }
    var sorted = searched;
    if (typeof lib.sortFiles === 'function') {
      sorted = lib.sortFiles(searched, state.sortKey, state.sortDir);
    }
    var pag = typeof lib.paginate === 'function'
      ? lib.paginate(sorted, state.page, PAGE_SIZE)
      : { rows: sorted.slice(0, PAGE_SIZE), total: sorted.length, page: 1, pages: 1 };
    /* 页越界收敛：筛选集缩小时当前页可能超出，钳到最后一页重切 */
    if (pag.pages > 0 && pag.page > pag.pages) {
      state.page = pag.pages;
      pag = typeof lib.paginate === 'function'
        ? lib.paginate(sorted, state.page, PAGE_SIZE)
        : pag;
    } else if (pag.pages === 0) {
      state.page = 1;
    }
    var sum = typeof lib.sumFiles === 'function'
      ? lib.sumFiles(base) /* 校验和口径=语言筛选集（与概览卡片一致，RD-008②）；搜索不影响 */
      : { files: base.length, lines: 0, code: 0, comment: 0, blank: 0, complexity: 0, bytes: 0 };
    return { base: base, searched: searched, pag: pag, sum: sum };
  }

  /* ---------- 渲染：骨架（mount 一次） + 动态区（每次刷新） ---------- */

  function mount(root) {
    var host = root || $('files-body');
    if (!host) return; /* 非 App 页面（如 test/unit.html）：不自动挂载 */
    if (mounted && els.root === host) return;
    clear(host);
    host.setAttribute('data-state', 'done');
    els.root = host;
    mounted = true;

    /* 工具行：搜索框 + 校验和 */
    var toolbar = el('div', 'files-toolbar');
    els.search = document.createElement('input');
    els.search.type = 'search';
    els.search.className = 'files-search';
    els.search.setAttribute('placeholder', t('搜索文件路径'));
    els.search.setAttribute('aria-label', t('搜索文件路径'));
    els.search.setAttribute('autocomplete', 'off');
    els.search.setAttribute('spellcheck', 'false');
    toolbar.appendChild(els.search);
    els.summary = el('p', 'files-summary');
    toolbar.appendChild(els.summary);
    host.appendChild(toolbar);

    /* 表格：列头排序按钮（复用 T-012 .sort-btn 体系，方向经 aria-sort 呈现） */
    var wrap = el('div', 'files-table-wrap');
    els.table = el('table', 'data-table files-table');
    var thead = document.createElement('thead');
    var headRow = document.createElement('tr');
    els.headCells = {};
    for (var c = 0; c < COLUMNS.length; c++) {
      var col = COLUMNS[c];
      var th = document.createElement('th');
      th.setAttribute('scope', 'col');
      if (col.num) th.classList.add('num-col');
      var btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'sort-btn';
      btn.setAttribute('data-key', col.key);
      btn.textContent = t(col.label);
      btn.setAttribute('data-i18n', col.label);
      btn.addEventListener('click', onSortClick);
      th.appendChild(btn);
      els.headCells[col.key] = th;
      headRow.appendChild(th);
    }
    thead.appendChild(headRow);
    els.table.appendChild(thead);
    els.tbody = document.createElement('tbody');
    els.table.appendChild(els.tbody);
    wrap.appendChild(els.table);
    host.appendChild(wrap);

    els.empty = el('p', 'files-empty');
    host.appendChild(els.empty);

    /* 分页行 */
    var pager = el('div', 'files-pagination');
    els.prev = el('button', 'btn btn-ghost files-page-btn');
    els.prev.type = 'button';
    els.prev.textContent = t('上一页');
    els.prev.setAttribute('data-i18n', '上一页');
    els.prev.addEventListener('click', function () { gotoPage(state.page - 1); });
    els.info = el('span', 'files-page-info');
    els.next = el('button', 'btn btn-ghost files-page-btn');
    els.next.type = 'button';
    els.next.textContent = t('下一页');
    els.next.setAttribute('data-i18n', '下一页');
    els.next.addEventListener('click', function () { gotoPage(state.page + 1); });
    pager.appendChild(els.prev);
    pager.appendChild(els.info);
    pager.appendChild(els.next);
    host.appendChild(pager);

    els.search.addEventListener('input', onSearchInput);
    render();
  }

  function onSortClick(ev) {
    var btn = ev.currentTarget;
    var key = btn && btn.getAttribute ? btn.getAttribute('data-key') : null;
    if (!key) return;
    if (state.sortKey === key) {
      state.sortDir = state.sortDir === 'asc' ? 'desc' : 'asc'; /* 同列再点：升降切换 */
    } else {
      state.sortKey = key;
      state.sortDir = 'asc';
    }
    state.page = 1;
    render();
  }

  function onSearchInput() {
    if (searchTimer) clearTimeout(searchTimer);
    var value = els.search ? els.search.value : '';
    searchTimer = setTimeout(function () {
      searchTimer = null;
      var q = typeof value === 'string' ? value.trim().toLowerCase() : '';
      if (q === state.query) return;
      state.query = q;
      state.page = 1;
      render();
    }, SEARCH_DEBOUNCE_MS);
  }

  function gotoPage(p) {
    if (!isFinite(p)) return;
    state.page = Math.trunc(p);
    render();
  }

  function render() {
    if (!mounted) return;
    var d = derived();
    var hasSnapshot = Array.isArray(state.files);
    var snapshotEmpty = hasSnapshot && state.files.length === 0;

    /* 列头方向指示（aria-sort 驱动 CSS 箭头，无需 JS 改样式） */
    for (var key in els.headCells) {
      if (!Object.prototype.hasOwnProperty.call(els.headCells, key)) continue;
      var th = els.headCells[key];
      if (key === state.sortKey) {
        th.setAttribute('aria-sort', state.sortDir === 'asc' ? 'ascending' : 'descending');
      } else {
        th.removeAttribute('aria-sort');
      }
    }

    /* 校验和行：语言筛选集聚合（无筛选时=快照 totals，与概览卡片逐键一致） */
    clear(els.summary);
    if (hasSnapshot) {
      var sum = d.sum;
      var pairs = [
        ['文件数', String(sum.files)],
        ['总行数', String(sum.lines)],
        ['代码', String(sum.code)],
        ['注释', String(sum.comment)],
        ['空白', String(sum.blank)],
        ['复杂度', String(sum.complexity)],
        ['大小', formatBytes(sum.bytes)]
      ];
      for (var i = 0; i < pairs.length; i++) {
        if (i > 0) els.summary.appendChild(el('span', 'files-summary-sep', ' · '));
        var lab = el('span', 'files-summary-label', t(pairs[i][0]));
        lab.setAttribute('data-i18n', pairs[i][0]);
        els.summary.appendChild(lab);
        els.summary.appendChild(el('span', 'files-summary-value', ' ' + pairs[i][1]));
      }
    }

    /* 表体：仅当前页 */
    clear(els.tbody);
    var rows = d.pag.rows;
    var frag = document.createDocumentFragment();
    if (hasSnapshot && rows.length === 0) {
      els.empty.textContent = snapshotEmpty && !state.query && !state.languageFilter
        ? t('当前快照无文件明细')
        : t('没有匹配的文件');
      els.empty.hidden = false;
    } else {
      els.empty.textContent = '';
      els.empty.hidden = true;
      for (var r = 0; r < rows.length; r++) frag.appendChild(buildRow(rows[r]));
    }
    els.tbody.appendChild(frag);

    /* 分页信息与按钮态 */
    var pages = d.pag.pages;
    var total = d.pag.total;
    els.info.textContent = interpolate(
      state.query ? '第 {a} / {b} 页 · 匹配 {c} 个文件' : '第 {a} / {b} 页 · 共 {c} 个文件',
      { a: String(Math.min(state.page, Math.max(pages, 1))), b: String(Math.max(pages, 1)), c: String(total) }
    );
    els.prev.disabled = state.page <= 1 || pages === 0;
    els.next.disabled = state.page >= pages || pages === 0;
  }

  function buildRow(f) {
    var tr = document.createElement('tr');
    var path = f && typeof f.path === 'string' ? f.path : '';
    var pathTd = el('td', 'files-cell-path', path);
    pathTd.title = path; /* 超长路径悬停可读全文（title 属性安全） */
    tr.appendChild(pathTd);
    tr.appendChild(el('td', 'lang-cell', displayLanguageName(f ? f.language : '')));
    var numeric = ['lines', 'code', 'comment', 'blank', 'complexity'];
    for (var i = 0; i < numeric.length; i++) {
      tr.appendChild(el('td', 'num-col', String(numOf(f ? f[numeric[i]] : null))));
    }
    tr.appendChild(el('td', 'num-col', formatBytes(f ? f.bytes : 0)));
    return tr;
  }

  function numOf(v) {
    var n = Number(v);
    return isFinite(n) ? n : 0;
  }

  /* ---------- 接缝：快照与语言筛选 ---------- */

  /* 快照入口：app.js pollJob（经 SCCV_STATS 桥接）或 SCCV_FILES.renderSnapshot 直连。
   * 懒挂载：首个快照到达时才替换 #files-body 占位（分析前保留 index.html 空态文案）。 */
  function renderSnapshot(job) {
    var files = job && typeof job === 'object' && Array.isArray(job.files) ? job.files : null;
    state.files = files;
    state.page = 1;
    if (!mounted && files && $('files-body')) mount($('files-body'));
    if (mounted) render();
  }

  /* 语言筛选入口：T-013 广播（'sccv:filterchange' detail.languages）或直连 */
  function setLanguageFilter(languages) {
    state.languageFilter = normalizeLanguageFilter(languages);
    state.page = 1;
    if (mounted) render();
  }

  /* 桥接 T-012 已落盘接缝：包装 global.SCCV_STATS.renderSnapshot，快照旁路进本视图。
   * 幂等（__sccvFilesBridge 标记）；stats.js 缺席时静默跳过（直连/事件通道仍可用）。 */
  function bridgeSnapshotSeam() {
    var stats = global.SCCV_STATS;
    if (!stats || typeof stats.renderSnapshot !== 'function') return;
    if (stats.renderSnapshot.__sccvFilesBridge) return;
    var orig = stats.renderSnapshot;
    var wrapped = function (job, rootPath) {
      var r = orig(job, rootPath);
      try { renderSnapshot(job, rootPath); } catch (err) { /* 明细表渲染失败不阻断统计视图 */ }
      return r;
    };
    wrapped.__sccvFilesBridge = true;
    stats.renderSnapshot = wrapped;
  }

  /* T-013 语言筛选广播（约定事件名；detail: {languages: string[]|null}）。 */
  document.addEventListener('sccv:filterchange', function (ev) {
    var detail = ev && ev.detail;
    if (!detail || typeof detail !== 'object' || !('languages' in detail)) return;
    setLanguageFilter(detail.languages);
  });

  /* ---------- 初始化 ---------- */

  function domInit() {
    bridgeSnapshotSeam(); /* 快照接缝先接（分析可在任意时刻完成）；表格随首个快照懒挂载 */
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', domInit);
  } else {
    domInit();
  }

  global.SCCV_FILES = {
    mount: mount,               /* 浏览器 harness 可注入挂载点后手动 mount */
    renderSnapshot: renderSnapshot,
    setLanguageFilter: setLanguageFilter,
    PAGE_SIZE: PAGE_SIZE
  };
})(typeof window !== 'undefined' ? window : globalThis);
