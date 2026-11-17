/* sccv 前端统计视图（T-012 · F6）
 * 范围：概览卡片（文件数/总行数/代码/注释/空白/复杂度/大小）+ 语言明细表（点列头排序，前端实现）
 *       + 导出三按钮接通（/api/export/json|csv 直下载；/api/report → 轮询 → downloadUrl 下载）。
 * 联动：监听 'sccv:filterchange'（T-013/T-014 约定广播）→ 概览卡片按筛选集聚合重算（RD-008②/DI-001，
 *       sumTotals 口径与明细表校验和一致）；全选/复位(null)回快照 totals 原值。
 * 挂载：消费 app.js pollJob 的快照接缝（global.SCCV_STATS.renderSnapshot，由 app.js 在 done 时调用）。
 * 数据：聚合/占比/排序/格式化全部来自 gui/web/lib/lib_stats.js 纯函数，本文件只做 DOM 编排。
 * 安全纪律（DEC-026）：仅 textContent / createElement / setAttribute 等安全文本 API；
 *       标记拼接型写入与动态求值一律不用；无外链、无内联脚本样式；样式全部走 style.css 追加段（明暗双主题沿用 T-011 变量体系）。
 * i18n：中文原文作键；本视图新增键的 en 字典经 SCCV_I18N.registerDict 在此注册（不动 i18n.js，Q8 无页内切换）。
 */
(function (global) {
  'use strict';

  if (typeof window === 'undefined' || typeof document === 'undefined') return;

  /* ---------- i18n：本视图新增键的 en 字典（缺键回退中文原文） ---------- */

  var EN_DICT = {
    '文件数': 'Files',
    '总行数': 'Total Lines',
    '代码': 'Code',
    '注释': 'Comment',
    '空白': 'Blank',
    '复杂度': 'Complexity',
    '大小': 'Size',
    '语言': 'Language',
    '行数': 'Lines',
    '占比': 'Share',
    '合计': 'Total',
    '点击列头排序': 'Click a column header to sort',
    '导出功能将在后续版本就绪': 'Export will become available in a later version',
    '暂无可导出的分析结果，请先完成一次分析': 'Nothing to export yet. Run an analysis first.',
    '导出失败': 'Export failed',
    '报告生成中…': 'Generating report…',
    '报告生成失败': 'Report generation failed',
    '报告生成耗时较长，请稍后重试': 'The report is taking long. Please retry later.',
    '已开始下载': 'Download started',
    /* pollJob 整体超时提示（T-012 接管增强时新增于 app.js 的键，en 归口在此注册） */
    '分析耗时较长，已停止等待；后端可能仍在处理，可稍后重试': 'Analysis is taking long. Waiting stopped; the backend may still be processing — you can retry later.'
  };

  if (global.SCCV_I18N && typeof global.SCCV_I18N.registerDict === 'function') {
    global.SCCV_I18N.registerDict('en', EN_DICT);
  }

  /* ---------- 语言明细表列定义 ---------- */

  var LANG_COLUMNS = [
    { key: 'name', label: '语言', sortable: true, num: false },
    { key: 'files', label: '文件数', sortable: true, num: true, hint: '该语言统计到的文件个数。' },
    { key: 'lines', label: '行数', sortable: true, num: true, hint: '该语言全部文件的总行数（代码+注释+空白）。' },
    { key: 'code', label: '代码', sortable: true, num: true, hint: '被计为代码的行数。' },
    { key: 'comment', label: '注释', sortable: true, num: true, hint: '被计为注释的行数。' },
    { key: 'blank', label: '空白', sortable: true, num: true, hint: '空行行数。' },
    { key: 'complexity', label: '复杂度', sortable: true, num: true, hint: '圈复杂度（scc 口径）：按关键字与分支计数。' },
    { key: 'pct', label: '占比', sortable: false, num: true, hint: '该语言行数占全部行数的百分比。' }
  ];

  var NUMERIC_KEYS = ['files', 'lines', 'code', 'comment', 'blank', 'complexity', 'bytes'];

  var EXPORT_POLL_INTERVAL_MS = 800;
  var EXPORT_POLL_MAX_ERRORS = 8;
  var EXPORT_REPORT_TIMEOUT_MS = 300000; /* 报告整体超时：到点解除按钮禁用并提示重试 */

  var els = {};
  var sortState = { key: 'lines', dir: 'desc' };
  var state = {
    rows: [],
    totals: null,
    filterLangs: null, /* 当前语言筛选（'sccv:filterchange' 广播；null=全量）；卡片口径 RD-008② */
    lastPath: '',
    includeUnknown: false,
    exportBusy: false
  };

  function $(id) { return document.getElementById(id); }

  function t(key) {
    var api = global.SCCV_I18N;
    return api && typeof api.t === 'function' ? api.t(key) : key;
  }

  function lib() {
    return global.SCCV_LIB_STATS || global.SCCV_LIB || {};
  }

  function clear(node) {
    if (!node) return;
    while (node.firstChild) node.removeChild(node.firstChild);
  }

  function sleep(ms) {
    return new Promise(function (resolve) { setTimeout(resolve, ms); });
  }

  function el(tag, cls, text) {
    var node = document.createElement(tag);
    if (cls) node.className = cls;
    if (text !== undefined) node.textContent = text;
    return node;
  }

  /* ---------- 数据归一 ---------- */

  function num(v) {
    var n = Number(v);
    return isFinite(n) ? n : 0;
  }

  function normalizeRows(raw) {
    var out = [];
    for (var i = 0; i < raw.length; i++) {
      var r = raw[i];
      if (!r || typeof r.name !== 'string' || !r.name) continue;
      var row = { name: r.name, files: 0, lines: 0, code: 0, comment: 0, blank: 0, complexity: 0, bytes: 0 };
      for (var k = 0; k < NUMERIC_KEYS.length; k++) {
        row[NUMERIC_KEYS[k]] = num(r[NUMERIC_KEYS[k]]);
      }
      out.push(row);
    }
    return out;
  }

  function validTotals(raw) {
    if (!raw || typeof raw !== 'object') return null;
    var out = {};
    for (var i = 0; i < NUMERIC_KEYS.length; i++) {
      var v = Number(raw[NUMERIC_KEYS[i]]);
      if (!isFinite(v)) return null; /* 任一键非法 → 整体判无效，走推导兜底 */
      out[NUMERIC_KEYS[i]] = v;
    }
    return out;
  }

  /* ---------- 概览卡片（F6；筛选联动口径 RD-008②） ---------- */

  /* 卡片 totals：无筛选 → 快照原值；有筛选 → filterLanguages 筛选集聚合
   * （sumTotals 口径与文件明细表校验和一致，DI-001）。语言明细表本体保持全量（F6 静态面）。 */
  function currentTotals() {
    var libS = lib();
    if (state.filterLangs && typeof libS.filterLanguages === 'function' && typeof libS.sumTotals === 'function') {
      return libS.sumTotals(libS.filterLanguages(state.rows, state.filterLangs));
    }
    return state.totals;
  }

  function renderOverview() {
    if (!els.overviewBody) return;
    clear(els.overviewBody);
    els.overviewBody.setAttribute('data-state', 'done');
    var totals = currentTotals() || {};
    var defs = [
      ['文件数', String(num(totals.files))],
      ['总行数', String(num(totals.lines))],
      ['代码', String(num(totals.code))],
      ['注释', String(num(totals.comment))],
      ['空白', String(num(totals.blank))],
      ['复杂度', String(num(totals.complexity))],
      ['大小', (lib().formatBytes || function (n) { return n + ' B'; })(num(totals.bytes))]
    ];
    var frag = document.createDocumentFragment();
    for (var i = 0; i < defs.length; i++) {
      var card = el('div', 'card');
      var label = el('div', 'metric-label', t(defs[i][0]));
      label.setAttribute('data-i18n', defs[i][0]);
      card.appendChild(label);
      card.appendChild(el('div', 'metric-value', defs[i][1]));
      frag.appendChild(card);
    }
    els.overviewBody.appendChild(frag);
  }

  /* ---------- 语言明细表（点列头排序） ---------- */

  function shareMapByLines() {
    var map = Object.create(null);
    var share = lib().shareOf;
    if (typeof share !== 'function') return map;
    var rows = share(state.rows, 'lines');
    for (var i = 0; i < rows.length; i++) map[rows[i].name] = rows[i].pct;
    return map;
  }

  /* OPT-005：列头问号悬浮（原生 title 载体；固定小宽由 style.css .col-hint 保证，
   * 不挤占 num-col 数字对齐；置于 sort-btn 兄弟位，applyAriaSort/点击委托零触碰）。 */
  function buildColHint(hintKey) {
    var q = document.createElement('span');
    q.className = 'col-hint';
    q.textContent = '?';
    q.setAttribute('title', t(hintKey));
    q.setAttribute('aria-label', t(hintKey));
    return q;
  }

  function renderLanguageTable() {
    if (!els.languageBody) return;
    clear(els.languageBody);
    els.languageBody.setAttribute('data-state', 'done');

    var libS = lib();
    var hint = el('p', 'table-hint', t('点击列头排序'));
    hint.setAttribute('data-i18n', '点击列头排序');
    els.languageBody.appendChild(hint);

    var table = el('table', 'data-table');
    var thead = document.createElement('thead');
    var headRow = document.createElement('tr');
    for (var c = 0; c < LANG_COLUMNS.length; c++) {
      var col = LANG_COLUMNS[c];
      var th = document.createElement('th');
      th.setAttribute('scope', 'col');
      if (col.num) th.classList.add('num-col');
      if (col.sortable) {
        var btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'sort-btn';
        btn.setAttribute('data-key', col.key);
        btn.textContent = t(col.label);
        btn.setAttribute('data-i18n', col.label);
        th.appendChild(btn);
        if (col.hint) th.appendChild(buildColHint(col.hint)); /* 问号为 sort-btn 兄弟节点：点击不进排序委托 */
      } else {
        th.textContent = t(col.label);
        th.setAttribute('data-i18n', col.label);
        if (col.hint) th.appendChild(buildColHint(col.hint));
      }
      applyAriaSort(th, col.key);
      headRow.appendChild(th);
    }
    thead.appendChild(headRow);
    table.appendChild(thead);

    var tbody = document.createElement('tbody');
    var sortFn = libS.sortLanguageRows;
    var rows = typeof sortFn === 'function'
      ? sortFn(state.rows, sortState.key, sortState.dir)
      : state.rows.slice();
    var shares = shareMapByLines();
    for (var i = 0; i < rows.length; i++) {
      tbody.appendChild(buildLanguageRow(rows[i], shares));
    }
    table.appendChild(tbody);

    table.appendChild(buildTotalsFoot());
    els.languageBody.appendChild(table);
  }

  function buildLanguageRow(row, shares) {
    var tr = document.createElement('tr');
    var nameCell = el('td', 'lang-cell', (lib().displayLanguageName || function (n) { return n; })(row.name));
    tr.appendChild(nameCell);
    var numeric = ['files', 'lines', 'code', 'comment', 'blank', 'complexity'];
    for (var i = 0; i < numeric.length; i++) {
      tr.appendChild(el('td', 'num-col', String(num(row[numeric[i]]))));
    }
    var pct = shares[row.name];
    tr.appendChild(el('td', 'num-col', (lib().formatPct || function (p) { return p + '%'; })(typeof pct === 'number' ? pct : 0)));
    return tr;
  }

  function buildTotalsFoot() {
    var tfoot = document.createElement('tfoot');
    var tr = document.createElement('tr');
    var totals = state.totals || {};
    tr.appendChild(el('td', 'lang-cell', t('合计')));
    var libS = lib();
    var ratio = typeof libS.ratioSuffix === 'function' ? libS.ratioSuffix : null;
    var totalLines = num(totals.lines);
    var numeric = ['files', 'lines', 'code', 'comment', 'blank', 'complexity'];
    for (var i = 0; i < numeric.length; i++) {
      var key = numeric[i];
      var v = num(totals[key]);
      var text = String(v);
      /* OPT-004：code/comment/blank 三格内联追加占总行数百分比（lines=0 → 空后缀只显原值）；
       * 其余格（files/lines/complexity/末列 100%）与明细行占比列保持原样。 */
      if (ratio && (key === 'code' || key === 'comment' || key === 'blank')) {
        text += ratio(v, totalLines);
      }
      tr.appendChild(el('td', 'num-col', text));
    }
    tr.appendChild(el('td', 'num-col', '100%'));
    tfoot.appendChild(tr);
    return tfoot;
  }

  function applyAriaSort(th, key) {
    if (sortState.key === key) {
      th.setAttribute('aria-sort', sortState.dir === 'asc' ? 'ascending' : 'descending');
    } else {
      th.removeAttribute('aria-sort');
    }
  }

  function onLanguageBodyClick(ev) {
    var target = ev.target;
    var btn = target && typeof target.closest === 'function' ? target.closest('.sort-btn') : null;
    if (!btn) return;
    var key = btn.getAttribute('data-key');
    if (!key) return;
    if (sortState.key === key) {
      sortState.dir = sortState.dir === 'asc' ? 'desc' : 'asc';
    } else {
      sortState.key = key;
      sortState.dir = key === 'name' ? 'asc' : 'desc';
    }
    renderLanguageTable();
  }

  /* ---------- 导出三按钮（DEC-012/DEC-101：全量口径；后端 T-032/033 就绪前 stub 返回 501） ---------- */

  function setExportStatus(msg, tone) {
    if (!els.exportStatus) return;
    els.exportStatus.textContent = msg || '';
    els.exportStatus.className = 'status' + (msg ? ' tone-' + (tone || 'muted') : '');
  }

  function syncExportButtons() {
    var has = !!state.totals;
    var busy = state.exportBusy;
    if (els.expHtml) els.expHtml.disabled = !has || busy;
    if (els.expJson) els.expJson.disabled = !has || busy;
    if (els.expCsv) els.expCsv.disabled = !has || busy;
  }

  function downloadBlob(blob, filename) {
    var url = URL.createObjectURL(blob);
    var a = document.createElement('a');
    a.href = url;
    a.download = filename;
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(function () { URL.revokeObjectURL(url); }, 4000);
  }

  async function exportDirect(kind, filename) {
    if (state.exportBusy) return;
    if (!state.totals) {
      setExportStatus(t('暂无可导出的分析结果，请先完成一次分析'), 'warn');
      return;
    }
    state.exportBusy = true;
    syncExportButtons();
    setExportStatus('');
    try {
      var res = await global.SCCV_APP.apiFetch('/api/export/' + kind, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ includeFiles: false })
      });
      if (res.status === 501) { /* 后端 stub（T-032 未就绪） */
        setExportStatus(t('导出功能将在后续版本就绪'), 'warn');
        return;
      }
      if (!res.ok) {
        var body = await safeJson(res);
        if (res.status === 409) {
          setExportStatus(t('暂无可导出的分析结果，请先完成一次分析'), 'warn');
        } else {
          setExportStatus(t('导出失败') + '（HTTP ' + res.status + ((body && body.error) ? ' ' + body.error : '') + '）', 'danger');
        }
        return;
      }
      var blob = await res.blob();
      downloadBlob(blob, filename);
      setExportStatus(t('已开始下载') + ' ' + filename, 'good');
    } catch (err) {
      setExportStatus(t('无法连接后端服务，请确认 sccv 正在运行'), 'danger');
    } finally {
      state.exportBusy = false;
      syncExportButtons();
    }
  }

  async function exportReport() {
    if (state.exportBusy) return;
    if (!state.lastPath || !state.totals) {
      setExportStatus(t('暂无可导出的分析结果，请先完成一次分析'), 'warn');
      return;
    }
    state.exportBusy = true;
    if (els.expHtml) els.expHtml.classList.add('is-loading');
    syncExportButtons();
    setExportStatus(t('报告生成中…'), 'muted');
    try {
      var res = await global.SCCV_APP.apiFetch('/api/report', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          path: state.lastPath,
          languages: null,
          includeUnknown: state.includeUnknown,
          includeHistory: false /* DEC-013：默认 false（git 观察器耗时） */
        })
      });
      if (res.status === 501) { /* 后端 stub（T-033 未就绪） */
        setExportStatus(t('导出功能将在后续版本就绪'), 'warn');
        return;
      }
      if (res.status === 409) {
        setExportStatus(t('已有分析任务在进行中，请稍候'), 'warn');
        return;
      }
      if (!res.ok) {
        var body = await safeJson(res);
        setExportStatus(t('报告生成失败') + '（HTTP ' + res.status + ((body && body.error) ? ' ' + body.error : '') + '）', 'danger');
        return;
      }
      var data = await res.json();
      if (!data || !data.jobId) {
        setExportStatus(t('报告生成失败'), 'danger');
        return;
      }
      var downloadUrl = await pollReportJob(data.jobId);
      var fileRes = await global.SCCV_APP.apiFetch(downloadUrl);
      if (!fileRes.ok) {
        setExportStatus(t('报告生成失败') + '（HTTP ' + fileRes.status + '）', 'danger');
        return;
      }
      var blob = await fileRes.blob();
      downloadBlob(blob, 'sccv-report.html');
      setExportStatus(t('已开始下载') + ' sccv-report.html', 'good');
    } catch (err) {
      if (err && err.message === 'report-timeout') {
        setExportStatus(t('报告生成耗时较长，请稍后重试'), 'warn');
      } else {
        setExportStatus(t('无法连接后端服务，请确认 sccv 正在运行'), 'danger');
      }
    } finally {
      state.exportBusy = false;
      if (els.expHtml) els.expHtml.classList.remove('is-loading');
      syncExportButtons();
    }
  }

  async function pollReportJob(jobId) {
    var startedAt = Date.now();
    var consecutiveErrors = 0;
    while (true) {
      if (Date.now() - startedAt > EXPORT_REPORT_TIMEOUT_MS) {
        throw new Error('report-timeout');
      }
      var payload = null;
      try {
        var res = await global.SCCV_APP.apiFetch('/api/job/' + encodeURIComponent(jobId));
        if (!res.ok) throw new Error('HTTP ' + res.status);
        payload = await res.json();
        consecutiveErrors = 0;
      } catch (err) {
        consecutiveErrors += 1;
        if (consecutiveErrors >= EXPORT_POLL_MAX_ERRORS) throw err;
      }
      if (payload && payload.status === 'done') {
        if (payload.job && typeof payload.job.downloadUrl === 'string' && payload.job.downloadUrl) {
          return payload.job.downloadUrl;
        }
        throw new Error('no downloadUrl');
      }
      if (payload && payload.status === 'error') {
        throw new Error((payload && payload.error) || 'report error');
      }
      await sleep(EXPORT_POLL_INTERVAL_MS);
    }
  }

  async function safeJson(res) {
    try { return await res.json(); } catch (err) { return null; }
  }

    /* ---------- 语言筛选联动（DI-001 / RD-008②：卡片随筛选集聚合） ---------- */

  /* 空数组/非法入参归一 null（与 files.js normalizeLanguageFilter 同口径） */
  function normalizeFilterLangs(languages) {
    if (!Array.isArray(languages)) return null;
    var out = [];
    for (var i = 0; i < languages.length; i++) {
      if (typeof languages[i] === 'string' && languages[i]) out.push(languages[i]);
    }
    return out.length > 0 ? out : null;
  }

  /* T-013/T-014 约定事件：'sccv:filterchange'，detail.languages: string[]|null */
  function onFilterChange(ev) {
    var detail = ev && ev.detail;
    if (!detail || typeof detail !== 'object' || !('languages' in detail)) return;
    state.filterLangs = normalizeFilterLangs(detail.languages);
    renderOverview(); /* 仅卡片联动；语言明细表保持全量口径（F6 静态面） */
  }

  /* ---------- 快照接缝：app.js pollJob 在 done 时调用 ---------- */

  function renderSnapshot(job, rootPath) {
    var libS = lib();
    if (!job || typeof job !== 'object' || typeof libS.aggregateByLanguage !== 'function') return;
    state.rows = (Array.isArray(job.languages) && job.languages.length > 0)
      ? normalizeRows(job.languages)
      : libS.aggregateByLanguage(job.files);
    state.totals = validTotals(job.totals) || (typeof libS.sumTotals === 'function' ? libS.sumTotals(state.rows) : null);
    state.lastPath = (typeof rootPath === 'string' && rootPath) ? rootPath : (job.rootPath || '');
    state.includeUnknown = !!(job.options && job.options.includeUnknown);
    renderOverview();
    renderLanguageTable();
    syncExportButtons();
  }

  /* ---------- 初始化 ---------- */

  function domInit() {
    els.overviewBody = $('overview-body');
    els.languageBody = $('language-body');
    els.exportStatus = $('export-status');
    els.expHtml = $('btn-export-html');
    els.expJson = $('btn-export-json');
    els.expCsv = $('btn-export-csv');
    if (!els.overviewBody && !els.languageBody) return; /* 非 App 页面（如 test/unit.html）：跳过 */
    if (els.languageBody) els.languageBody.addEventListener('click', onLanguageBodyClick);
    document.addEventListener('sccv:filterchange', onFilterChange); /* DI-001：卡片筛选联动 */
    if (els.expHtml) els.expHtml.addEventListener('click', exportReport);
    if (els.expJson) els.expJson.addEventListener('click', function () { exportDirect('json', 'sccv-export.json'); });
    if (els.expCsv) els.expCsv.addEventListener('click', function () { exportDirect('csv', 'sccv-export.csv'); });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', domInit);
  } else {
    domInit();
  }

  global.SCCV_STATS = {
    renderSnapshot: renderSnapshot
  };
})(typeof window !== 'undefined' ? window : globalThis);
