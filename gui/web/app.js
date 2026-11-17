/* sccv 前端主逻辑（T-011 骨架）
 * 范围：token 捕获与剥离（DEC-014/026）、F5 目录输入交互（对话框/手输/localStorage 历史≤10 条+清除入口）、
 *       最小分析轮询（POST /api/analyze → GET /api/job/{id}；T-012 接管增强：整体超时可重试 + 快照渲染接缝）、
 *       两步向导（OPT-001 目录树三态勾选 + OPT-002 文件类型双模式；主"开始分析"按钮先弹向导，
 *       向导内「直接分析（全部包含）」= 原直跑等价快捷）、
 *       i18n 应用、退出（K9）。
 * 安全纪律（DEC-026，T-006 grep 门禁）：所有 DOM 写入仅限安全文本 API
 *       （textContent / createTextNode / setAttribute）；标记拼接型写入一律禁用；
 *       无内联脚本/样式；接口全部同源相对 /api/*，请求带 X-SCCV-Token 头。
 * 结构：上半部为无 DOM 纯函数与存储/令牌助手（node 经 module.exports 可加载，双载体单测）；
 *       下半部为浏览器 DOM 逻辑（node 下整段跳过）。
 */
(function (global) {
  'use strict';

  var TOKEN_KEY = 'sccv_token';
  var HISTORY_KEY = 'sccv_history';

  var SCCV_APP = {};
  SCCV_APP.HISTORY_LIMIT = 10;
  SCCV_APP.TOKEN_KEY = TOKEN_KEY;
  SCCV_APP.HISTORY_KEY = HISTORY_KEY;

  /* ---------- 纯函数：历史记录（DEC-011：去重置顶、上限 10 条） ---------- */

  SCCV_APP.sanitizeHistory = function (raw) {
    if (!Array.isArray(raw)) return [];
    var out = [];
    for (var i = 0; i < raw.length; i++) {
      var e = raw[i];
      if (e && typeof e.path === 'string') {
        var p = e.path.trim(); // 边界归一：剔除空白串路径（与手输 trim 口径一致）
        if (p) {
          var ts = Number(e.ts);
          out.push({ path: p, ts: isFinite(ts) ? ts : 0 });
        }
      }
    }
    return out;
  };

  SCCV_APP.pushHistory = function (list, path, ts) {
    var head = { path: path, ts: typeof ts === 'number' ? ts : 0 };
    var src = Array.isArray(list) ? list : [];
    var rest = [];
    for (var i = 0; i < src.length; i++) {
      if (src[i] && src[i].path !== path) rest.push(src[i]);
    }
    var next = [head].concat(rest);
    return next.length > SCCV_APP.HISTORY_LIMIT ? next.slice(0, SCCV_APP.HISTORY_LIMIT) : next;
  };

  /* ---------- 存储：localStorage 不可用（file:// 隐私模式等）时静默降级 ---------- */

  function storageGet(key) {
    try { return global.localStorage.getItem(key); } catch (err) { return null; }
  }

  function storageSet(key, value) {
    try { global.localStorage.setItem(key, value); return true; } catch (err) { return false; }
  }

  SCCV_APP.loadHistory = function () {
    var raw = storageGet(HISTORY_KEY);
    if (!raw) return [];
    try { return SCCV_APP.sanitizeHistory(JSON.parse(raw)); } catch (err) { return []; }
  };

  SCCV_APP.saveHistory = function (list) {
    return storageSet(HISTORY_KEY, JSON.stringify(Array.isArray(list) ? list : []));
  };

  /* ---------- token（DEC-014：URL query 载入 → sessionStorage → replaceState 剥离） ---------- */

  SCCV_APP.captureTokenFromUrl = function () {
    if (typeof window === 'undefined' || !window.location) return null;
    var token = null;
    try {
      var qs = new URLSearchParams(window.location.search);
      token = qs.get('token');
      if (token) {
        try { window.sessionStorage.setItem(TOKEN_KEY, token); } catch (err) { /* 存储不可用：仅本次内存留存 */ }
        qs.delete('token');
        var rest = qs.toString();
        var url = window.location.pathname + (rest ? '?' + rest : '') + window.location.hash;
        try { window.history.replaceState(null, '', url); } catch (err) { /* 个别环境拒绝历史改写：保持原 URL 不致命 */ }
      } else {
        try { token = window.sessionStorage.getItem(TOKEN_KEY); } catch (err) { /* 同上 */ }
      }
    } catch (err) { /* 极老内核无 URLSearchParams：后续请求仅缺 token，由 401 门禁兜底 */ }
    return token;
  };

  SCCV_APP.getToken = function () {
    if (typeof window === 'undefined') return null;
    try { return window.sessionStorage.getItem(TOKEN_KEY); } catch (err) { return null; }
  };

  /* ---------- API 访问：同源相对路径 + X-SCCV-Token 头（DEC-014） ---------- */

  SCCV_APP.apiFetch = function (path, options) {
    var opts = options ? Object.assign({}, options) : {};
    var headers = Object.assign({}, opts.headers || {});
    headers['X-SCCV-Token'] = SCCV_APP.getToken() || '';
    opts.headers = headers;
    return global.fetch(path, opts);
  };

  /* ---------- 双挂载导出 ---------- */

  if (typeof module !== 'undefined' && module.exports) module.exports = SCCV_APP;

  /* ================= 浏览器 DOM 逻辑（node 下整段跳过） ================= */

  if (typeof window === 'undefined' || typeof document === 'undefined') return;
  global.SCCV_APP = SCCV_APP;

  var els = {};
  var busy = false;
  var browseBlocked = false;
  /* BG-003：退出成功或健康探测确认服务死亡后置位。此后浏览/退出的失败
   * 话术统一为"服务已退出"，不再显示"后端不可达"。 */
  var exited = false;

  var POLL_INTERVAL_MS = 600;
  var POLL_MAX_CONSECUTIVE_ERRORS = 8;
  /* T-012 接管增强：轮询整体超时上限。到点解除按钮禁用并提示可重试，
   * 避免后端状态异常时"开始分析"永久禁用（R2：后端忙时 409，重试安全）。 */
  var POLL_OVERALL_TIMEOUT_MS = 300000;

  function $(id) { return document.getElementById(id); }

  function i18nApi() { return global.SCCV_I18N || null; }

  function t(key) {
    var api = i18nApi();
    return api && typeof api.t === 'function' ? api.t(key) : key;
  }

  function applyI18n() {
    var nodes = document.querySelectorAll('[data-i18n]');
    for (var i = 0; i < nodes.length; i++) {
      nodes[i].textContent = t(nodes[i].getAttribute('data-i18n'));
    }
    var phs = document.querySelectorAll('[data-i18n-placeholder]');
    for (var j = 0; j < phs.length; j++) {
      phs[j].setAttribute('placeholder', t(phs[j].getAttribute('data-i18n-placeholder')));
    }
    document.title = t('sccv 代码统计');
    var api = i18nApi();
    document.documentElement.setAttribute('lang', api && api.getLang() === 'en' ? 'en' : 'zh-CN');
  }

  function setStatus(msg, tone) {
    if (!els.status) return;
    els.status.textContent = msg || '';
    els.status.className = 'status' + (msg ? ' tone-' + (tone || 'muted') : '');
  }

  function setHint(msg, tone) {
    if (!els.hint) return;
    els.hint.textContent = msg || '';
    els.hint.className = 'status' + (msg ? ' tone-' + (tone || 'danger') : '');
  }

  function syncButtons() {
    if (els.analyze) els.analyze.disabled = busy;
    if (els.browse) els.browse.disabled = busy || browseBlocked;
    if (els.path) els.path.readOnly = busy;
    var section = $('scan-section');
    if (section) section.setAttribute('aria-busy', busy ? 'true' : 'false');
  }

  /* ---------- 历史渲染：仅安全文本 API 逐节点构建 ---------- */

  function renderHistory() {
    if (!els.historyList) return;
    var list = SCCV_APP.loadHistory();
    while (els.historyList.firstChild) els.historyList.removeChild(els.historyList.firstChild);
    var isEmpty = list.length === 0;
    els.historyList.hidden = isEmpty;
    if (els.historyEmpty) els.historyEmpty.hidden = !isEmpty;
    if (els.clearHistory) els.clearHistory.disabled = isEmpty;
    var frag = document.createDocumentFragment();
    for (var i = 0; i < list.length; i++) {
      (function (entry, index) {
        var li = document.createElement('li');
        var btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'history-item';
        btn.title = entry.path;
        var idx = document.createElement('span');
        idx.className = 'history-index';
        idx.textContent = String(index + 1);
        var pathSpan = document.createElement('span');
        pathSpan.className = 'history-path';
        pathSpan.textContent = entry.path;
        btn.appendChild(idx);
        btn.appendChild(pathSpan);
        btn.addEventListener('click', function () {
          if (els.path) { els.path.value = entry.path; els.path.focus(); }
          setHint('');
        });
        li.appendChild(btn);
        frag.appendChild(li);
      })(list[i], i);
    }
    els.historyList.appendChild(frag);
  }

  /* ---------- F5：浏览文件夹（POST /api/browse） ---------- */

  /* BG-003 修复的文案常量（zh 原文即键，en 见 i18n.js 字典） */
  var BROWSE_PENDING_MESSAGE = '正在打开目录选择对话框，请在弹窗中选择（首次约 2-3 秒；若未出现请检查任务栏）';
  var BROWSE_FAILED_MESSAGE = '目录选择对话框打开失败，请手动输入路径';
  var SERVICE_EXITED_MESSAGE = '服务已退出，请重新启动 sccv.exe';

  /* fetch reject 时的快速存活探测：存活 → 提示可重试；死亡 → 服务已退出话术 */
  async function probeBackendAlive() {
    try {
      var res = await SCCV_APP.apiFetch('/api/health', { method: 'GET' });
      return !!(res && res.ok);
    } catch (err) {
      return false;
    }
  }

  async function handleBrowse() {
    if (busy || browseBlocked) return;
    if (exited) { setHint(t(SERVICE_EXITED_MESSAGE), 'danger'); return; }
    setHint('');
    els.browse.disabled = true;
    setHint(t(BROWSE_PENDING_MESSAGE), 'muted'); // 对话框 2-3 秒后才出现：进行时先给反馈
    try {
      var res = await SCCV_APP.apiFetch('/api/browse', { method: 'POST' });
      setHint(''); // 出口清除进行时提示
      if (!res.ok) {
        // 文案分流（BG-003 修复点 4）：HTTP 5xx 携带后端 error 文案，原样展示，不进 catch；
        // 按钮保持可用，下次点击即可重试。
        var body = res.status >= 500 ? await safeJson(res) : null;
        setHint((body && body.error) ? body.error : t(BROWSE_FAILED_MESSAGE), 'danger');
        els.browse.disabled = false;
        return;
      }
      var data = await res.json();
      if (data && data.canceled) { els.browse.disabled = false; return; } // 用户取消：静默
      if (data && typeof data.path === 'string' && data.path) {
        els.path.value = data.path;
        els.path.focus();
      }
      els.browse.disabled = false;
    } catch (err) {
      // catch 先判 exited（BG-003 修复点 3）：退出在途连坐的请求不再报"后端不可达"
      if (exited) {
        browseBlocked = true;
        els.browse.disabled = true;
        setHint(t(SERVICE_EXITED_MESSAGE), 'danger');
        return;
      }
      if (await probeBackendAlive()) {
        // 后端存活但请求失败：不永久禁用，下次点击重试
        els.browse.disabled = false;
        setHint(t(BROWSE_FAILED_MESSAGE), 'danger');
        return;
      }
      // 健康探测确认死亡：才用"服务已退出"文案
      exited = true;
      browseBlocked = true;
      els.browse.disabled = true;
      setHint(t(SERVICE_EXITED_MESSAGE), 'danger');
    }
  }

  /* ---------- F5：开始分析（POST /api/analyze → 轮询 /api/job/{id}） ----------
   * 二批定稿：主"开始分析"按钮先弹两步向导（openWizard）；向导内
   * 「直接分析（全部包含）」= 原直跑等价快捷（黄金值路径，请求体逐字节同构）。 */

  async function runAnalysis(body, rootPath, filterNote) {
    if (busy) return;
    activeFilterNote = filterNote || '';
    busy = true;
    els.analyze.classList.add('is-loading');
    syncButtons();
    setStatus(t('分析中…'), 'muted');
    try {
      var res = await SCCV_APP.apiFetch('/api/analyze', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body)
      });
      if (res.status === 409) {
        setStatus(t('已有分析任务在进行中，请稍候'), 'warn');
        busy = false;
        els.analyze.classList.remove('is-loading');
        syncButtons();
        return;
      }
      if (!res.ok) {
        var errBody = await safeJson(res);
        setStatus((errBody && errBody.error) ? errBody.error : t('请求失败'), 'danger');
        busy = false;
        els.analyze.classList.remove('is-loading');
        syncButtons();
        return;
      }
      var data = await res.json();
      if (!data || !data.jobId) {
        setStatus(t('请求失败'), 'danger');
        busy = false;
        els.analyze.classList.remove('is-loading');
        syncButtons();
        return;
      }
      await pollJob(data.jobId, rootPath);
    } catch (err) {
      setStatus(t('无法连接后端服务，请确认 sccv 正在运行'), 'danger');
      busy = false;
      els.analyze.classList.remove('is-loading');
      syncButtons();
    }
  }

  /* 原直跑路径（向导「直接分析（全部包含）」复用）：单根全量，无任何筛选字段。 */
  function handleAnalyzeDirect() {
    if (busy) return;
    var path = currentPath();
    if (!path) {
      setHint(t('请先输入要分析的文件夹路径'), 'warn');
      if (els.path) els.path.focus();
      return;
    }
    setHint('');
    runAnalysis({
      path: path,
      includeUnknown: !!(els.includeUnknown && els.includeUnknown.checked),
      languages: null
    }, path);
  }

  async function safeJson(res) {
    try { return await res.json(); } catch (err) { return null; }
  }

  async function pollJob(jobId, rootPath) {
    var consecutiveErrors = 0;
    var startedAt = Date.now();
    while (true) {
      if (Date.now() - startedAt > POLL_OVERALL_TIMEOUT_MS) {
        // T-012 接管增强：整体超时，解除按钮禁用并提示可重试（避免永久禁用）
        finishAnalyze(false, t('分析耗时较长，已停止等待；后端可能仍在处理，可稍后重试'), 'warn');
        return;
      }
      var payload = null;
      try {
        var res = await SCCV_APP.apiFetch('/api/job/' + encodeURIComponent(jobId));
        if (!res.ok) throw new Error('HTTP ' + res.status);
        payload = await res.json();
        consecutiveErrors = 0;
      } catch (err) {
        consecutiveErrors += 1;
        if (consecutiveErrors >= POLL_MAX_CONSECUTIVE_ERRORS) {
          finishAnalyze(false, t('分析失败') + '：' + t('无法连接后端服务，请确认 sccv 正在运行'), 'danger');
          return;
        }
      }
      if (payload && payload.status === 'done') {
        recordHistory(rootPath); // DEC-011：分析成功后记录（去重置顶、上限 10）
        renderSnapshotHook(payload.job, rootPath); // T-012 接管：快照 → 统计视图（stats.js）
        finishAnalyze(true, t('分析完成'), 'good');
        return payload;
      }
      if (payload && payload.status === 'error') {
        finishAnalyze(false, t('分析失败') + '：' + ((payload && payload.error) || t('请求失败')), 'danger');
        return;
      }
      await new Promise(function (resolve) { setTimeout(resolve, POLL_INTERVAL_MS); });
    }
  }

  /* T-012 接缝：快照交给统计视图渲染层（stats.js 挂 global.SCCV_STATS）；渲染异常不阻断主流程 */
  function renderSnapshotHook(job, rootPath) {
    if (global.SCCV_STATS && typeof global.SCCV_STATS.renderSnapshot === 'function') {
      try { global.SCCV_STATS.renderSnapshot(job, rootPath); } catch (err) { /* 渲染失败仅影响视图，不影响分析状态机 */ }
    }
    if (global.SCCV_EXPLAIN && typeof global.SCCV_EXPLAIN.render === 'function') { try { global.SCCV_EXPLAIN.render(job, rootPath); } catch (err) { /* 说明卡渲染失败不阻断主流程（T-301） */ } }
  }

  function finishAnalyze(ok, msg, tone) {
    if (ok && activeFilterNote) {
      msg = msg + '（' + activeFilterNote + '）'; // 结果页筛选回显：已排除 N 目录 / M 类型
      activeFilterNote = '';
    }
    setStatus(msg, tone);
    busy = false;
    els.analyze.classList.remove('is-loading');
    syncButtons();
    if (ok) revealResults();
  }

  function recordHistory(path) {
    var next = SCCV_APP.pushHistory(SCCV_APP.loadHistory(), path);
    SCCV_APP.saveHistory(next);
    renderHistory();
  }

  /* 唯一主动效：结果区一次性编排浮现（style.css #results-area.reveal） */
  function revealResults() {
    if (!els.resultsArea) return;
    els.resultsArea.classList.remove('reveal');
    void els.resultsArea.offsetWidth; // 强制重排以重触发动画
    els.resultsArea.classList.add('reveal');
  }

  /* ---------- 两步向导（OPT-001 目录树三态勾选 + OPT-002 文件类型双模式） ----------
   * 第一步：/api/tree 层级树（默认全勾；取消勾即排除；父三态由子聚合；父取消=整子树；
   *         常用排除快捷项 chips）。第二步：/api/extensions 扩展名清单（模式单选
   *         不限/仅分析勾选类型/排除勾选类型；重叠的类型以排除为准）。
   * 向导内选项全部可回退（重置=全勾/不限）；「直接分析（全部包含）」= 原直跑等价快捷。
   * 安全纪律（DEC-301）：一切动态 DOM 仅安全文本 API（createElement/textContent/setAttribute）。 */

  var WIZARD_CHIP_NAMES = ['node_modules', '.git', 'target', 'dist'];
  var wizard = {
    open: false,
    step: 1,
    root: '',
    nodes: [],          // lib_tree.normalizeTree 产出的状态树（state: checked/unchecked/partial）
    treeTruncated: false,
    extLoaded: false,
    extTruncated: false,
    extScanned: 0,
    extExtensionless: 0,
    extRows: [],        // [{ext, files, checked}]
    extMode: 'none'     // none | include | exclude
  };
  var activeFilterNote = ''; // 本次分析的状态行筛选摘要（finishAnalyze 成功时消费）

  function el(tag, cls, text) {
    var node = document.createElement(tag);
    if (cls) node.className = cls;
    if (text !== undefined && text !== null) node.textContent = String(text);
    return node;
  }

  function currentPath() {
    return els.path && els.path.value ? els.path.value.trim() : '';
  }

  function resetWizardState() {
    wizard.root = '';
    wizard.nodes = [];
    wizard.treeTruncated = false;
    wizard.extLoaded = false;
    wizard.extRows = [];
    wizard.extTruncated = false;
    wizard.extScanned = 0;
    wizard.extExtensionless = 0;
    wizard.extMode = 'none';
    syncExtModeRadios();
    renderWizardExtList();
    if (els.wizardExcludedNote) els.wizardExcludedNote.textContent = '';
    setWizardTreeStatus('');
    setWizardExtStatus('');
  }

  async function openWizard() {
    if (busy) return;
    var path = currentPath();
    if (!path) {
      setHint(t('请先输入要分析的文件夹路径'), 'warn');
      if (els.path) els.path.focus();
      return;
    }
    setHint('');
    resetWizardState();
    wizard.open = true;
    wizard.step = 1;
    if (els.wizard) els.wizard.hidden = false;
    if (els.wizardTreeStep) els.wizardTreeStep.hidden = false;
    if (els.wizardExtStep) els.wizardExtStep.hidden = true;
    renderWizardStep();
    await loadWizardTree(path);
  }

  function closeWizard() {
    wizard.open = false;
    if (els.wizard) els.wizard.hidden = true;
    resetWizardState();
  }

  function renderWizardStep() {
    if (els.wizardStepLabel) {
      els.wizardStepLabel.textContent = wizard.step === 2 ? t('第 2 步，共 2 步') : t('第 1 步，共 2 步');
    }
  }

  function wizardGoStep1() {
    wizard.step = 1;
    if (els.wizardTreeStep) els.wizardTreeStep.hidden = false;
    if (els.wizardExtStep) els.wizardExtStep.hidden = true;
    renderWizardStep();
  }

  function wizardGoStep2() {
    wizard.step = 2;
    if (els.wizardTreeStep) els.wizardTreeStep.hidden = true;
    if (els.wizardExtStep) els.wizardExtStep.hidden = false;
    renderWizardStep();
    if (!wizard.extLoaded) loadWizardExtensions();
  }

  function setWizardTreeStatus(msg, tone) {
    if (!els.wizardTreeStatus) return;
    els.wizardTreeStatus.textContent = msg || '';
    els.wizardTreeStatus.className = 'status' + (msg ? ' tone-' + (tone || 'muted') : '');
  }

  function setWizardExtStatus(msg, tone) {
    if (!els.wizardExtStatus) return;
    els.wizardExtStatus.textContent = msg || '';
    els.wizardExtStatus.className = 'status' + (msg ? ' tone-' + (tone || 'muted') : '');
  }

  /* ---------- 第一步：目录树（懒渲染子层 + 三态勾选） ---------- */

  async function loadWizardTree(path) {
    setWizardTreeStatus(t('目录树加载中…'), 'muted');
    try {
      var res = await SCCV_APP.apiFetch('/api/tree', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: path })
      });
      if (!res.ok) {
        var body = await safeJson(res);
        setWizardTreeStatus((body && body.error) ? body.error : t('目录树加载失败'), 'danger');
        return;
      }
      var data = await res.json();
      var lib = global.SCCV_LIB_TREE;
      var model = lib && typeof lib.normalizeTree === 'function'
        ? lib.normalizeTree(data)
        : { nodes: [], truncated: false };
      wizard.root = data && typeof data.root === 'string' ? data.root : path;
      wizard.nodes = model.nodes;
      wizard.treeTruncated = !!model.truncated;
      setWizardTreeStatus(
        wizard.treeTruncated ? t('目录较多，部分深层目录未展示') : '',
        wizard.treeTruncated ? 'warn' : 'muted'
      );
      renderWizardTree();
      updateWizardMeta();
    } catch (err) {
      setWizardTreeStatus(t('无法连接后端服务，请确认 sccv 正在运行'), 'danger');
    }
  }

  function baseName(p) {
    var s = String(p || '').replace(/\\/g, '/');
    var parts = s.split('/');
    for (var i = parts.length - 1; i >= 0; i--) {
      if (parts[i]) return parts[i];
    }
    return s;
  }

  function renderWizardTree() {
    if (!els.wizardTree) return;
    while (els.wizardTree.firstChild) els.wizardTree.removeChild(els.wizardTree.firstChild);
    els.wizardTree.appendChild(buildRootRow());
    appendWizardNodes(els.wizardTree, wizard.nodes, 1);
  }

  function buildRootRow() {
    var row = el('div', 'tree-row');
    var cb = el('input');
    cb.type = 'checkbox';
    cb.checked = true;
    cb.disabled = true; // 分析根目录恒包含：整根排除=无意义作业，后端 400 兜底
    cb.setAttribute('aria-label', t('分析根目录（始终包含）'));
    row.appendChild(cb);
    var name = el('span', 'tree-name tree-root-name', baseName(wizard.root));
    name.title = wizard.root;
    row.appendChild(name);
    return row;
  }

  /* 懒渲染：仅已展开分支产出 DOM；子层首次展开才构建（节点数据已在内存，无需二次请求） */
  function appendWizardNodes(container, nodes, depth) {
    for (var i = 0; i < nodes.length; i++) {
      var node = nodes[i];
      container.appendChild(buildNodeRow(node));
      if (node.open && node.children && node.children.length) {
        var childBox = el('div', 'tree-children');
        appendWizardNodes(childBox, node.children, depth + 1);
        container.appendChild(childBox);
      }
    }
  }

  function buildNodeRow(node) {
    var row = el('div', 'tree-row');
    var toggle = el('button', 'tree-toggle');
    toggle.type = 'button';
    if (node.children && node.children.length) {
      toggle.textContent = node.open ? '▾' : '▸';
      toggle.setAttribute('aria-label', node.open ? t('收起子目录') : t('展开子目录'));
      toggle.addEventListener('click', function () {
        node.open = !node.open;
        renderWizardTree();
      });
    } else {
      toggle.disabled = true;
    }
    row.appendChild(toggle);
    var cb = el('input');
    cb.type = 'checkbox';
    cb.checked = node.state === 'checked';
    cb.indeterminate = node.state === 'partial'; // 三态：勾/半选/空
    cb.addEventListener('click', function () {
      setWizardSubtree(node, cb.checked ? 'checked' : 'unchecked'); // 父级勾/取消=整子树联动
      recomputeWizardStates();
      renderWizardTree();
      updateWizardMeta();
    });
    row.appendChild(cb);
    var name = el('span', 'tree-name', node.name);
    name.title = node.path;
    row.appendChild(name);
    return row;
  }

  function setWizardSubtree(node, state) {
    node.state = state;
    for (var i = 0; i < node.children.length; i++) setWizardSubtree(node.children[i], state);
  }

  /* 后序聚合：父态=子态聚合（全勾=checked/全空=unchecked/混合=partial）；叶子保持自身两态 */
  function recomputeWizardStates() {
    recomputeWizardChildren(wizard.nodes);
  }

  function recomputeWizardChildren(nodes) {
    for (var i = 0; i < nodes.length; i++) {
      var node = nodes[i];
      if (node.children && node.children.length) {
        recomputeWizardChildren(node.children);
        var checked = 0;
        var unchecked = 0;
        for (var j = 0; j < node.children.length; j++) {
          if (node.children[j].state === 'checked') checked++;
          else if (node.children[j].state === 'unchecked') unchecked++;
          else { checked++; unchecked++; } // partial 计入两侧 → 父必为 partial
        }
        node.state = checked === node.children.length
          ? 'checked'
          : (unchecked === node.children.length ? 'unchecked' : 'partial');
      } else if (node.state !== 'unchecked' && node.state !== 'checked') {
        node.state = 'checked';
      }
    }
  }

  function wizardExcludedSegments() {
    var lib = global.SCCV_LIB_TREE;
    if (!lib || typeof lib.collectExcluded !== 'function') return [];
    return lib.collectExcluded(wizard.root, wizard.nodes);
  }

  function updateWizardMeta() {
    if (els.wizardExcludedNote) {
      var segments = wizardExcludedSegments();
      els.wizardExcludedNote.textContent = segments.length
        ? t('已排除') + ' ' + segments.length + ' ' + t('个目录')
        : t('全部包含，未排除任何目录');
    }
    syncWizardChips();
  }

  function resetWizardTree() {
    for (var i = 0; i < wizard.nodes.length; i++) setWizardSubtree(wizard.nodes[i], 'checked');
    renderWizardTree();
    updateWizardMeta();
  }

  /* ---------- 常用排除快捷项 chips ---------- */

  function buildWizardChips() {
    if (!els.wizardChips) return;
    while (els.wizardChips.firstChild) els.wizardChips.removeChild(els.wizardChips.firstChild);
    for (var i = 0; i < WIZARD_CHIP_NAMES.length; i++) {
      (function (name) {
        var chip = el('button', 'chip', name);
        chip.type = 'button';
        chip.addEventListener('click', function () { toggleWizardChip(name); });
        els.wizardChips.appendChild(chip);
      })(WIZARD_CHIP_NAMES[i]);
    }
  }

  function findWizardNodesByName(nodes, name, out) {
    for (var i = 0; i < nodes.length; i++) {
      if (nodes[i].name === name) out.push(nodes[i]);
      if (nodes[i].children && nodes[i].children.length) findWizardNodesByName(nodes[i].children, name, out);
    }
  }

  /* chip 一击切换：任一同名目录仍在包含态 → 全部取消勾；否则全部恢复勾（状态自树推导） */
  function toggleWizardChip(name) {
    var matches = [];
    findWizardNodesByName(wizard.nodes, name, matches);
    if (!matches.length) {
      if (name === '.git') setWizardTreeStatus(t('.git 目录由引擎默认排除，无需手动排除'), 'muted');
      return;
    }
    var anyIncluded = false;
    for (var i = 0; i < matches.length; i++) {
      if (matches[i].state !== 'unchecked') { anyIncluded = true; break; }
    }
    var target = anyIncluded ? 'unchecked' : 'checked';
    for (var j = 0; j < matches.length; j++) setWizardSubtree(matches[j], target);
    recomputeWizardStates();
    renderWizardTree();
    updateWizardMeta();
  }

  function syncWizardChips() {
    if (!els.wizardChips) return;
    var chips = els.wizardChips.children;
    for (var i = 0; i < chips.length && i < WIZARD_CHIP_NAMES.length; i++) {
      var matches = [];
      findWizardNodesByName(wizard.nodes, WIZARD_CHIP_NAMES[i], matches);
      var allOff = matches.length > 0;
      for (var j = 0; j < matches.length; j++) {
        if (matches[j].state !== 'unchecked') { allOff = false; break; }
      }
      chips[i].className = 'chip' + (allOff ? ' chip-active' : '');
    }
  }

  /* ---------- 第二步：文件类型（/api/extensions + 双模式） ---------- */

  async function loadWizardExtensions() {
    setWizardExtStatus(t('文件类型加载中…'), 'muted');
    try {
      var res = await SCCV_APP.apiFetch('/api/extensions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: currentPath() })
      });
      if (!res.ok) {
        var body = await safeJson(res);
        setWizardExtStatus((body && body.error) ? body.error : t('文件类型加载失败'), 'danger');
        return;
      }
      var data = await res.json();
      wizard.extRows = [];
      var rows = data && Array.isArray(data.extensions) ? data.extensions : [];
      for (var i = 0; i < rows.length; i++) {
        var r = rows[i];
        if (r && typeof r.ext === 'string' && r.ext) {
          wizard.extRows.push({ ext: r.ext, files: typeof r.files === 'number' ? r.files : 0, checked: true });
        }
      }
      wizard.extLoaded = true;
      wizard.extTruncated = !!(data && data.truncated);
      wizard.extScanned = data && typeof data.scanned === 'number' ? data.scanned : 0;
      wizard.extExtensionless = data && typeof data.extensionless === 'number' ? data.extensionless : 0;
      setWizardExtStatus(
        wizard.extTruncated ? t('文件较多，仅统计前 5 万个文件的扩展名') : '',
        wizard.extTruncated ? 'warn' : 'muted'
      );
      renderWizardExtList();
    } catch (err) {
      setWizardExtStatus(t('无法连接后端服务，请确认 sccv 正在运行'), 'danger');
    }
  }

  function renderWizardExtList() {
    if (!els.wizardExtList) return;
    while (els.wizardExtList.firstChild) els.wizardExtList.removeChild(els.wizardExtList.firstChild);
    for (var i = 0; i < wizard.extRows.length; i++) {
      (function (row) {
        var wrap = el('label', 'ext-row');
        var cb = el('input');
        cb.type = 'checkbox';
        cb.checked = row.checked;
        cb.disabled = wizard.extMode === 'none'; // 不限=列表禁用（0号裁决④）
        cb.addEventListener('change', function () { row.checked = cb.checked; });
        wrap.appendChild(cb);
        wrap.appendChild(el('span', 'ext-name', row.ext));
        wrap.appendChild(el('span', 'ext-count', '(' + row.files + ')'));
        els.wizardExtList.appendChild(wrap);
      })(wizard.extRows[i]);
    }
    if (els.wizardExtNote) {
      els.wizardExtNote.textContent = wizard.extExtensionless
        ? t('无扩展名文件') + ' ' + wizard.extExtensionless + ' ' + t('个')
        : '';
    }
    if (els.wizardExtList) els.wizardExtList.className = 'wizard-ext-list' + (wizard.extMode === 'none' ? ' disabled' : '');
  }

  function syncExtModeRadios() {
    var radios = document.getElementsByName('wizard-ext-mode');
    for (var i = 0; i < radios.length; i++) {
      radios[i].checked = radios[i].value === wizard.extMode;
    }
  }

  function onExtModeChange() {
    var radios = document.getElementsByName('wizard-ext-mode');
    for (var i = 0; i < radios.length; i++) {
      if (radios[i].checked) {
        wizard.extMode = radios[i].value;
        break;
      }
    }
    renderWizardExtList();
  }

  function wizardExtSetAll(all) {
    for (var i = 0; i < wizard.extRows.length; i++) wizard.extRows[i].checked = all;
    renderWizardExtList();
  }

  function wizardExtInvert() {
    for (var i = 0; i < wizard.extRows.length; i++) wizard.extRows[i].checked = !wizard.extRows[i].checked;
    renderWizardExtList();
  }

  /* ---------- 向导提交 ---------- */

  function wizardDirectAnalyze() {
    closeWizard();
    handleAnalyzeDirect(); // 原直跑等价快捷：黄金值路径
  }

  function wizardAnalyze() {
    var path = currentPath();
    if (!path) {
      closeWizard();
      setHint(t('请先输入要分析的文件夹路径'), 'warn');
      return;
    }
    var body = {
      path: path,
      includeUnknown: !!(els.includeUnknown && els.includeUnknown.checked),
      languages: null
    };
    var notes = [];
    var segments = wizardExcludedSegments();
    if (segments.length) {
      body.excludeDirs = segments; // root 相对斜杠段（后端校验+补集覆盖翻译）
      notes.push(t('已排除') + ' ' + segments.length + ' ' + t('个目录'));
    }
    var checked = [];
    for (var i = 0; i < wizard.extRows.length; i++) {
      if (wizard.extRows[i].checked) checked.push(wizard.extRows[i].ext);
    }
    if (wizard.extMode === 'include' && checked.length) {
      body.fileTypes = checked;
      notes.push(t('已按') + ' ' + checked.length + ' ' + t('种类型筛选'));
    } else if (wizard.extMode === 'exclude' && checked.length) {
      body.excludeFileTypes = checked;
      notes.push(t('已排除') + ' ' + checked.length + ' ' + t('种类型'));
    }
    closeWizard();
    runAnalysis(body, path, notes.join(' · '));
  }

  /* ---------- 退出（K9：POST /api/shutdown，响应后服务端优雅退出） ---------- */

  /* 退出遮罩（BG-003 修复点 3）：退出成功后全屏明示状态并拦截残余输入。
   * 安全纪律（DEC-026）：仅安全文本 API 构建（createElement/textContent）。 */
  function showExitMask() {
    if (document.getElementById('exit-mask')) return;
    var mask = document.createElement('div');
    mask.id = 'exit-mask';
    mask.className = 'exit-mask';
    var msg = document.createElement('div');
    msg.className = 'exit-mask-message';
    msg.textContent = t('已退出，可以安全关闭此页面');
    mask.appendChild(msg);
    document.body.appendChild(mask);
  }

  async function handleExit() {
    if (!els.exit) return;
    if (exited) return;
    els.exit.disabled = true;
    try {
      var res = await SCCV_APP.apiFetch('/api/shutdown', { method: 'POST' });
      if (res.ok) {
        exited = true;
        showExitMask();
        setStatus(t('已退出，可以安全关闭此页面'), 'muted');
        setHint('');
        if (els.analyze) els.analyze.disabled = true;
        if (els.browse) els.browse.disabled = true;
        if (els.path) els.path.readOnly = true;
      } else {
        els.exit.disabled = false;
        setHint(t('请求失败'), 'danger');
      }
    } catch (err) {
      // catch 先判 exited（BG-003 修复点 3）：不再回落到"后端不可达"话术
      if (exited) {
        setHint(t(SERVICE_EXITED_MESSAGE), 'danger');
        return;
      }
      els.exit.disabled = false;
      setHint(t('无法连接后端服务，请确认 sccv 正在运行'), 'danger');
    }
  }

  /* ---------- 页面生命周期通道（BG-004）----------
   * SSE 在场流（/api/events，断连静默——EventSource 按 retry 间隔自动重连）+
   * 慢心跳兜底（20s 一次 /api/heartbeat）。服务端规则：SSE 归零 15s 宽限
   * （重连/心跳即取消）且作业槽空闲 → 优雅退出；last-seen >120s 且空闲 →
   * 兜底退出。页面关闭两路信号同断，服务端据此判定。 */
  var HEARTBEAT_INTERVAL_MS = 20000;
  var lifecycleHeartbeatTimer = null;

  function startLifecycleChannels() {
    var token = SCCV_APP.getToken() || '';
    try {
      var source = new EventSource('/api/events?token=' + encodeURIComponent(token));
      source.onerror = function () { /* 静默：EventSource 自动重连（刷新/瞬断由服务端 15s 宽限覆盖） */ };
    } catch (err) { /* 无 EventSource 环境：仅慢心跳兜底，自动退出退化为 120s 档 */ }
    if (lifecycleHeartbeatTimer) return; // 防御：心跳定时器只挂一份
    lifecycleHeartbeatTimer = setInterval(function () {
      if (exited) { // 已退出：心跳自停（退出按钮路径）
        clearInterval(lifecycleHeartbeatTimer);
        lifecycleHeartbeatTimer = null;
        return;
      }
      SCCV_APP.apiFetch('/api/heartbeat', { method: 'GET' }).catch(function () { /* 服务已退出：静默 */ });
    }, HEARTBEAT_INTERVAL_MS);
  }

  /* ---------- /api/config：语言来源（失败默认 zh）+ 版本占位填充 ---------- */

  async function loadConfig() {
    try {
      var res = await SCCV_APP.apiFetch('/api/config');
      if (!res.ok) return;
      var data = await res.json();
      if (data && typeof data.lang === 'string') {
        var api = i18nApi();
        if (api && typeof api.setLang === 'function') api.setLang(data.lang);
        applyI18n();
      }
      if (data && typeof data.version === 'string' && data.version && els.version) {
        els.version.textContent = 'v' + data.version;
      }
    } catch (err) { /* 后端未就绪：保持默认 zh 与版本占位，页面骨架照常可用 */ }
  }

  function bindEvents() {
    if (els.browse) els.browse.addEventListener('click', handleBrowse);
    if (els.analyze) els.analyze.addEventListener('click', openWizard); // 二批定稿：主按钮先弹向导
    if (els.clearHistory) {
      els.clearHistory.addEventListener('click', function () {
        SCCV_APP.saveHistory([]);
        renderHistory();
      });
    }
    if (els.exit) els.exit.addEventListener('click', handleExit);
    if (els.path) {
      els.path.addEventListener('keydown', function (ev) {
        if (ev.key === 'Enter') { ev.preventDefault(); openWizard(); }
      });
    }
    // 两步向导
    if (els.wizardClose) els.wizardClose.addEventListener('click', closeWizard);
    if (els.wizardDirect) els.wizardDirect.addEventListener('click', wizardDirectAnalyze);
    if (els.wizardReset) els.wizardReset.addEventListener('click', resetWizardTree);
    if (els.wizardNext) els.wizardNext.addEventListener('click', wizardGoStep2);
    if (els.wizardBack) els.wizardBack.addEventListener('click', wizardGoStep1);
    if (els.wizardAnalyze) els.wizardAnalyze.addEventListener('click', wizardAnalyze);
    if (els.wizardExtAll) els.wizardExtAll.addEventListener('click', function () { wizardExtSetAll(true); });
    if (els.wizardExtInvert) els.wizardExtInvert.addEventListener('click', wizardExtInvert);
    var modeRadios = document.getElementsByName('wizard-ext-mode');
    for (var i = 0; i < modeRadios.length; i++) {
      modeRadios[i].addEventListener('change', onExtModeChange);
    }
  }

  function domInit() {
    els.version = $('version-label');
    els.exit = $('btn-exit');
    els.browse = $('btn-browse');
    els.path = $('path-input');
    els.analyze = $('btn-analyze');
    els.includeUnknown = $('cb-include-unknown');
    els.status = $('scan-status');
    els.hint = $('scan-hint');
    els.historyList = $('history-list');
    els.historyEmpty = $('history-empty');
    els.clearHistory = $('btn-clear-history');
    els.resultsArea = $('results-area');
    // 两步向导（OPT-001/002）
    els.wizard = $('wizard');
    els.wizardStepLabel = $('wizard-step-label');
    els.wizardTreeStep = $('wizard-tree-step');
    els.wizardExtStep = $('wizard-ext-step');
    els.wizardTreeStatus = $('wizard-tree-status');
    els.wizardChips = $('wizard-chips');
    els.wizardTree = $('wizard-tree');
    els.wizardExcludedNote = $('wizard-excluded-note');
    els.wizardExtStatus = $('wizard-ext-status');
    els.wizardExtList = $('wizard-ext-list');
    els.wizardExtNote = $('wizard-ext-note');
    els.wizardClose = $('btn-wizard-close');
    els.wizardDirect = $('btn-wizard-direct');
    els.wizardReset = $('btn-wizard-reset');
    els.wizardNext = $('btn-wizard-next');
    els.wizardBack = $('btn-wizard-back');
    els.wizardAnalyze = $('btn-wizard-analyze');
    els.wizardExtAll = $('btn-wizard-ext-all');
    els.wizardExtInvert = $('btn-wizard-ext-invert');
    if (!els.analyze || !els.path) return; // 非 App 页面（如 test/unit.html）：跳过绑定
    buildWizardChips();
    renderHistory();
    bindEvents();
    loadConfig();
    startLifecycleChannels(); // BG-004：SSE 在场流 + 慢心跳兜底（token 已在脚本载入时捕获）
  }

  /* token 捕获必须先于一切请求：脚本位于 body 末尾，location 此刻可用 */
  SCCV_APP.captureTokenFromUrl();

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', domInit);
  } else {
    domInit();
  }
})(typeof window !== 'undefined' ? window : globalThis);
