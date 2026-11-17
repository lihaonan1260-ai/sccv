/* sccv 前端语言分布图（T-013 · F7）
 * 范围：#charts-body 挂载点——手写 SVG 饼图/环形图（createElementNS，零外部图表库）、
 *       饼/环切换（切换态持久于内存；本图唯一主动效=切换时的弧过渡，rAF 内径插值）、
 *       图例（点击切换该语言选中态 → 广播 'sccv:filterchange'，与统计视图/明细表共用筛选口径）。
 * 接缝（与 T-014 桥接方式同构，不改 app.js/stats.js 一行）：
 *   ① 快照：桥接包装 global.SCCV_STATS.renderSnapshot（幂等标记 __sccvChartsBridge）；直连面
 *      global.SCCV_CHARTS.renderSnapshot(job, rootPath)；
 *   ② 筛选：本视图为广播源——document CustomEvent 'sccv:filterchange'，
 *      detail: {languages: string[]|null}（null=无筛选；空数组归一为 null，与 lib_stats.filterLanguages 口径一致）。
 * 安全纪律（DEC-026）：textContent / createElement(NS) / setAttribute；SVG 节点一律 createElementNS；
 *       标记拼接型写入一律不用（与 app.js/stats.js 同句式），色板经 CSSOM（node.style）赋值，不走 style 属性字符串；
 *       无外链；样式走 style.css 追加段（T-013 段）。
 * i18n：中文原文作键；本视图新增键的 en 字典经 SCCV_I18N.registerDict 在此注册（不动 i18n.js）。
 * 口径：扇区/图例数值=代码行（code），与 scc HTML 报告环形图同口径（report_render.go donutArcs 用 Code）。
 */
(function (global) {
  'use strict';

  if (typeof window === 'undefined' || typeof document === 'undefined') return;

  /* ---------- i18n：本视图新增键的 en 字典（缺键回退中文原文；注册幂等） ---------- */

  var EN_DICT = {
    '饼图': 'Pie',
    '环形图': 'Donut',
    '全选': 'All',
    '反选': 'Invert',
    '代码行': 'code lines',
    '按代码行占比 · 点击图例项筛选语言': 'Share of code lines · Click a legend item to filter',
    '当前快照无语言数据': 'No language data in the current snapshot',
    '语言分布图': 'Language distribution chart',
    '图表形态': 'Chart style',
    '语言筛选': 'Language filter'
  };

  if (global.SCCV_I18N && typeof global.SCCV_I18N.registerDict === 'function') {
    global.SCCV_I18N.registerDict('en', EN_DICT);
  }

  /* ---------- 常量与状态 ---------- */

  var SVG_NS = 'http://www.w3.org/2000/svg';
  var MODE_PIE = 'pie';
  var MODE_DONUT = 'donut';
  var DONUT_RATIO = 0.58;      /* 环形内径/外径 */
  var VIEW_SIZE = 240;         /* viewBox 边长 */
  var CENTER = VIEW_SIZE / 2;
  var R_OUTER = 112;
  var ANIM_MS = 360;           /* 本图唯一主动效时长（切换弧过渡） */

  var state = {
    rows: [],          /* [{name, value}]，value=代码行（快照面，只读引用） */
    selected: null,    /* null=全部（无筛选）；数组=按名勾选（永不为空，空集归一回 null） */
    mode: MODE_PIE,    /* 切换态持久于内存（模块级，随会话存续） */
    innerRatio: 0      /* 当前内径比例（弧过渡动画插值态） */
  };

  var els = {};
  var rafId = 0;
  var mounted = false;

  /* ---------- 基础助手 ---------- */

  function $(id) { return document.getElementById(id); }

  function t(key) {
    var api = global.SCCV_I18N;
    return api && typeof api.t === 'function' ? api.t(key) : key;
  }

  function el(tag, cls, text) {
    var node = document.createElement(tag);
    if (cls) node.className = cls;
    if (text !== undefined) node.textContent = text;
    return node;
  }

  function svgEl(name, attrs) {
    var node = document.createElementNS(SVG_NS, name);
    if (attrs) {
      for (var k in attrs) {
        if (Object.prototype.hasOwnProperty.call(attrs, k)) node.setAttribute(k, attrs[k]);
      }
    }
    return node;
  }

  function clear(node) {
    if (!node) return;
    while (node.firstChild) node.removeChild(node.firstChild);
  }

  function libCharts() {
    return global.SCCV_LIB_CHARTS || global.SCCV_LIB || {};
  }

  function num(v) {
    var n = Number(v);
    return isFinite(n) && n >= 0 ? n : 0;
  }

  /* K4：Unknown 数据值保留英文，呈现"未识别 (Unknown)"（lib_stats 优先，本地兜底同文案） */
  function displayLanguageName(name) {
    var f = global.SCCV_LIB_STATS && typeof global.SCCV_LIB_STATS.displayLanguageName === 'function'
      ? global.SCCV_LIB_STATS.displayLanguageName : null;
    if (f) return f(name);
    return name === 'Unknown' ? '未识别 (Unknown)' : String(name == null ? '' : name);
  }

  function fmtPct(p) {
    var f = global.SCCV_LIB_STATS && typeof global.SCCV_LIB_STATS.formatPct === 'function'
      ? global.SCCV_LIB_STATS.formatPct : null;
    if (f) return f(p);
    var v = Math.round(num(p) * 10) / 10;
    return (v % 1 === 0 ? String(v) : v.toFixed(1)) + '%';
  }

  function prefersReducedMotion() {
    return !!(global.matchMedia && global.matchMedia('(prefers-reduced-motion: reduce)').matches);
  }

  /* ---------- 快照面：languages[]（§5 schema）→ [{name, value=code}] ---------- */

  function languageRows(job) {
    if (!job || typeof job !== 'object') return [];
    var out = [];
    var langs = Array.isArray(job.languages) ? job.languages : null;
    if (langs && langs.length > 0) {
      for (var i = 0; i < langs.length; i++) {
        var l = langs[i];
        if (!l || typeof l !== 'object' || typeof l.name !== 'string' || !l.name) continue;
        out.push({ name: l.name, value: num(l.code) });
      }
      return out;
    }
    /* languages 缺失时与统计视图同口径：由 files[] 聚合兜底（lib_stats.aggregateByLanguage） */
    var libS = global.SCCV_LIB_STATS;
    if (Array.isArray(job.files) && libS && typeof libS.aggregateByLanguage === 'function') {
      var agg = libS.aggregateByLanguage(job.files);
      for (var j = 0; j < agg.length; j++) {
        out.push({ name: agg[j].name, value: num(agg[j].code) });
      }
    }
    return out;
  }

  /* ---------- 选集归一与广播 ---------- */

  /* 空选集归一为 null（=全部/无筛选），与 lib_stats.filterLanguages、files.js 归一口径一致 */
  function normalizeSelection(list) {
    if (!Array.isArray(list)) return null;
    var known = Object.create(null);
    for (var i = 0; i < state.rows.length; i++) known[state.rows[i].name] = true;
    var out = [];
    for (var j = 0; j < list.length; j++) {
      var n = list[j];
      if (typeof n === 'string' && n && known[n] && out.indexOf(n) === -1) out.push(n);
    }
    return out.length ? out : null;
  }

  function selectedNames() {
    if (state.selected) return state.selected.slice();
    var all = [];
    for (var i = 0; i < state.rows.length; i++) all.push(state.rows[i].name);
    return all;
  }

  /* T-014 约定事件：'sccv:filterchange'，detail.languages: string[]|null */
  function broadcastFilter(languages) {
    try {
      document.dispatchEvent(new CustomEvent('sccv:filterchange', { detail: { languages: languages } }));
    } catch (err) { /* 极老内核无 CustomEvent：SCCV_FILES.setLanguageFilter 直连通道仍可用 */ }
  }

  function applySelection(list) {
    state.selected = normalizeSelection(list);
    refresh();
    broadcastFilter(state.selected ? state.selected.slice() : null);
  }

  function toggleLanguage(name) {
    if (typeof name !== 'string' || !name) return;
    var names = selectedNames();
    var idx = names.indexOf(name);
    if (idx >= 0) names.splice(idx, 1); else names.push(name);
    applySelection(names);
  }

  function invertSelection() {
    var current = state.selected ? state.selected : [];
    var keep = Object.create(null);
    for (var i = 0; i < current.length; i++) keep[current[i]] = true;
    var inv = [];
    for (var j = 0; j < state.rows.length; j++) {
      if (!keep[state.rows[j].name]) inv.push(state.rows[j].name);
    }
    applySelection(inv); /* 空补集归一为全选 */
  }

  /* ---------- 挂载与渲染 ---------- */

  function mount(host) {
    var root = host || $('charts-body');
    if (!root) return; /* 非 App 页面（如 test/unit.html）：不自动挂载 */
    if (mounted && els.root === root) { refresh(); return; }
    clear(root);
    els.root = root;
    root.setAttribute('data-state', 'done');
    mounted = true;

    /* 工具行：饼/环切换组 + 全选/反选 */
    var toolbar = el('div', 'charts-toolbar');
    var toggle = el('div', 'charts-toggle');
    toggle.setAttribute('role', 'group');
    toggle.setAttribute('aria-label', t('图表形态'));
    els.btnPie = el('button', 'charts-mode-btn');
    els.btnPie.type = 'button';
    els.btnPie.setAttribute('data-mode', MODE_PIE);
    els.btnPie.textContent = t('饼图');
    els.btnPie.addEventListener('click', function () { switchMode(MODE_PIE); });
    els.btnDonut = el('button', 'charts-mode-btn');
    els.btnDonut.type = 'button';
    els.btnDonut.setAttribute('data-mode', MODE_DONUT);
    els.btnDonut.textContent = t('环形图');
    els.btnDonut.addEventListener('click', function () { switchMode(MODE_DONUT); });
    toggle.appendChild(els.btnPie);
    toggle.appendChild(els.btnDonut);

    var actions = el('div', 'charts-filter-actions');
    els.btnAll = el('button', 'link-btn');
    els.btnAll.type = 'button';
    els.btnAll.textContent = t('全选');
    els.btnAll.addEventListener('click', function () { applySelection(null); });
    els.btnInvert = el('button', 'link-btn');
    els.btnInvert.type = 'button';
    els.btnInvert.textContent = t('反选');
    els.btnInvert.addEventListener('click', invertSelection);
    actions.appendChild(els.btnAll);
    actions.appendChild(els.btnInvert);

    toolbar.appendChild(toggle);
    toolbar.appendChild(actions);
    root.appendChild(toolbar);

    /* 图区：手写 SVG（createElementNS） */
    var figure = el('div', 'charts-figure');
    els.svg = svgEl('svg', {
      'class': 'charts-svg',
      viewBox: '0 0 ' + VIEW_SIZE + ' ' + VIEW_SIZE,
      role: 'img',
      'aria-label': t('语言分布图')
    });
    els.slices = svgEl('g');
    els.centerValue = svgEl('text', {
      x: CENTER, y: CENTER - 4, 'text-anchor': 'middle', 'class': 'charts-center-value'
    });
    els.centerLabel = svgEl('text', {
      x: CENTER, y: CENTER + 16, 'text-anchor': 'middle', 'class': 'charts-center-label'
    });
    els.svg.appendChild(els.slices);
    els.svg.appendChild(els.centerValue);
    els.svg.appendChild(els.centerLabel);
    figure.appendChild(els.svg);
    els.empty = el('p', 'empty-state charts-empty');
    figure.appendChild(els.empty);
    root.appendChild(figure);

    els.caption = el('p', 'charts-caption');
    els.caption.textContent = t('按代码行占比 · 点击图例项筛选语言');
    root.appendChild(els.caption);

    /* 图例：语言筛选的唯一操作面 */
    els.legend = el('ul', 'charts-legend');
    els.legend.setAttribute('role', 'group');
    els.legend.setAttribute('aria-label', t('语言筛选'));
    root.appendChild(els.legend);

    refresh();
  }

  function switchMode(next) {
    if (next !== MODE_PIE && next !== MODE_DONUT) return;
    if (next === state.mode) return;
    var from = state.innerRatio;
    var to = next === MODE_DONUT ? DONUT_RATIO : 0;
    state.mode = next;
    syncModeButtons();
    if (rafId) { global.cancelAnimationFrame(rafId); rafId = 0; }
    if (prefersReducedMotion() || typeof global.requestAnimationFrame !== 'function') {
      state.innerRatio = to; /* 减动效偏好：直接落位，不做弧过渡 */
      updateFigure();
      return;
    }
    var start = 0;
    var step = function (ts) {
      if (!start) start = ts;
      var p = Math.min((ts - start) / ANIM_MS, 1);
      var ease = 1 - Math.pow(1 - p, 3); /* easeOutCubic */
      state.innerRatio = from + (to - from) * ease;
      updateFigure();
      if (p < 1) rafId = global.requestAnimationFrame(step);
      else rafId = 0;
    };
    rafId = global.requestAnimationFrame(step);
  }

  function syncModeButtons() {
    if (els.btnPie) els.btnPie.setAttribute('aria-pressed', String(state.mode === MODE_PIE));
    if (els.btnDonut) els.btnDonut.setAttribute('aria-pressed', String(state.mode === MODE_DONUT));
  }

  /* 扇区层 + 环心标签（弧过渡动画逐帧重画，当前可见占比与图例/明细表同源） */
  function updateFigure() {
    if (!els.slices) return;
    var lib = libCharts();
    clear(els.slices);
    clear(els.centerValue);
    clear(els.centerLabel);

    var rows = lib.selectRows ? lib.selectRows(state.rows, state.selected) : state.rows.slice();
    var donut = state.innerRatio > 0.0001;
    var slices = donut
      ? (lib.donutSlices ? lib.donutSlices(rows, state.innerRatio * R_OUTER) : [])
      : (lib.pieSlices ? lib.pieSlices(rows) : []);

    var total = 0;
    for (var i = 0; i < rows.length; i++) total += rows[i].value;

    for (var j = 0; j < slices.length; j++) {
      var s = slices[j];
      var d = lib.arcPath
        ? lib.arcPath(CENTER, CENTER, R_OUTER, s.innerRadius || 0, s.startAngle, s.endAngle)
        : '';
      if (!d) continue; /* 零跨度切片不可见，图例仍保留其条目 */
      var path = svgEl('path', {
        d: d,
        fill: lib.colorForLanguage ? lib.colorForLanguage(s.name) : '#999999',
        'class': 'charts-slice'
      });
      path.setAttribute('data-name', s.name);
      var title = svgEl('title');
      var share = total > 0 ? Math.round(s.value / total * 10000) / 100 : 0;
      title.textContent = displayLanguageName(s.name) + ' · ' + String(s.value) + ' · ' + fmtPct(share);
      path.appendChild(title);
      els.slices.appendChild(path);
    }

    if (donut && total > 0) {
      els.centerValue.textContent = String(total);
      els.centerLabel.textContent = t('代码行');
    }
  }

  /* 图例：始终呈现全部语言（未勾选项置灰可点回），数值=代码行绝对值，pct=当前可见集内占比 */
  function renderLegend() {
    if (!els.legend) return;
    var lib = libCharts();
    clear(els.legend);
    var frag = document.createDocumentFragment();
    var allMeta = lib.legendData ? lib.legendData(state.rows) : [];
    var visible = lib.selectRows ? lib.selectRows(state.rows, state.selected) : state.rows.slice();
    var visibleMeta = lib.legendData ? lib.legendData(visible) : [];

    var valueByName = Object.create(null);
    var colorByName = Object.create(null);
    for (var i = 0; i < allMeta.length; i++) {
      valueByName[allMeta[i].name] = allMeta[i].value;
      colorByName[allMeta[i].name] = allMeta[i].color;
    }
    var pctByName = Object.create(null);
    for (var j = 0; j < visibleMeta.length; j++) pctByName[visibleMeta[j].name] = visibleMeta[j].pct;

    for (var k = 0; k < state.rows.length; k++) {
      (function (name) {
        var isOn = !state.selected || state.selected.indexOf(name) !== -1;
        var li = el('li', 'charts-legend-item');
        var btn = el('button', 'legend-item' + (isOn ? '' : ' is-off'));
        btn.type = 'button';
        btn.setAttribute('aria-pressed', String(isOn));
        btn.title = displayLanguageName(name);
        var swatch = el('span', 'legend-swatch');
        swatch.style.backgroundColor = colorByName[name] || '#999999'; /* CSSOM，不经 style 属性串 */
        var nameSpan = el('span', 'legend-name', displayLanguageName(name));
        var valSpan = el('span', 'legend-value',
          String(valueByName[name] || 0) + ' · ' + (isOn ? fmtPct(pctByName[name] || 0) : '—'));
        btn.appendChild(swatch);
        btn.appendChild(nameSpan);
        btn.appendChild(valSpan);
        btn.addEventListener('click', function () { toggleLanguage(name); });
        li.appendChild(btn);
        frag.appendChild(li);
      })(state.rows[k].name);
    }
    els.legend.appendChild(frag);
  }

  function refresh() {
    if (!mounted) return;
    syncModeButtons();
    var hasRows = state.rows.length > 0;
    if (els.svg) els.svg.setAttribute('class', 'charts-svg' + (hasRows ? '' : ' is-hidden'));
    if (els.empty) els.empty.textContent = hasRows ? '' : t('当前快照无语言数据');
    if (els.btnAll) els.btnAll.disabled = !hasRows;
    if (els.btnInvert) els.btnInvert.disabled = !hasRows;
    updateFigure();
    renderLegend();
  }

  /* ---------- 接缝：快照（与 T-014 桥接同构） ---------- */

  /* 快照入口：app.js pollJob（经 SCCV_STATS 桥接旁路）或 SCCV_CHARTS.renderSnapshot 直连。
   * 新快照复位语言筛选（前端视图过滤语义，不改快照数据），并广播复位保持三视图一致。 */
  function renderSnapshot(job) {
    state.rows = languageRows(job);
    state.selected = null;
    if (!mounted && $('charts-body')) mount($('charts-body'));
    if (mounted) {
      refresh();
      broadcastFilter(null);
    }
  }

  function bridgeSnapshotSeam() {
    var stats = global.SCCV_STATS;
    if (!stats || typeof stats.renderSnapshot !== 'function') return;
    if (stats.renderSnapshot.__sccvChartsBridge) return;
    var orig = stats.renderSnapshot;
    var wrapped = function (job, rootPath) {
      var r = orig(job, rootPath);
      try { renderSnapshot(job, rootPath); } catch (err) { /* 分布图渲染失败不阻断统计视图 */ }
      return r;
    };
    wrapped.__sccvChartsBridge = true;
    stats.renderSnapshot = wrapped;
  }

  /* ---------- 对外最小面（探针/harness 用） ---------- */

  global.SCCV_CHARTS = {
    mount: mount,
    renderSnapshot: renderSnapshot,
    getMode: function () { return state.mode; },
    getSelectedLanguages: function () { return state.selected ? state.selected.slice() : null; }
  };

  /* ---------- 初始化 ---------- */

  function domInit() {
    bridgeSnapshotSeam(); /* 快照接缝先接（分析可在任意时刻完成）；图表随首个快照懒挂载 */
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', domInit);
  } else {
    domInit();
  }
})(typeof window !== 'undefined' ? window : globalThis);
