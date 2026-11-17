/* sccv 前端口径说明视图（T-301 · OPT-003）
 * 范围：结果页"统计口径说明"可折叠卡（index.html #explain-section 骨架）——
 *   ①各语言统计规则：数据全部来自 GET /api/explain（后端从 LanguageDatabase 透出，前端不硬编码规则），
 *     按语言分组展示"单行注释符号 / 多行注释符号 / 复杂度检查项 / 关键词"摘要（字段 8 项+总数截断呈现）；
 *   ②"未计入统计"说明：未识别类型文件（Unknown 行联动）/ 二进制文件 / 被排除目录与文件类型 三类规则性说明。
 * 挂载：消费 app.js renderSnapshotHook 的快照接缝（global.SCCV_EXPLAIN.render，由 app.js 在 done 时调用）。
 * 联动：①类"查看 Unknown 行"按钮经既有接缝 global.SCCV_FILES.setLanguageFilter(['Unknown'])
 *   过滤文件明细表（不广播 'sccv:filterchange'，概览卡片/分布图不受影响）+ 滚动至文件明细区。
 * 数据：响应/快照归一全部来自 gui/web/lib/lib_explain.js 纯函数，本文件只做 DOM 编排与取数。
 * 安全纪律（DEC-026，T-006 grep 门禁）：所有 DOM 写入仅限安全文本 API
 *   （textContent / createElement / setAttribute / className）；标记拼接型写入一律禁用。
 * i18n：中文原文作键；en 字典归口 i18n.js（本文件不重复注册）。
 */
(function (global) {
  'use strict';

  if (typeof window === 'undefined' || typeof document === 'undefined') return;

  var els = {};
  /* 取数序号：仅最新快照的请求允许渲染（连续两次分析时旧响应作废，避免旧数据覆盖新卡） */
  var pendingToken = 0;

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

  function lib() {
    return global.SCCV_LIB_EXPLAIN || {};
  }

  /* 语言名展示口径与统计视图一致（lib_stats.displayLanguageName：Unknown → 未识别 (Unknown)） */
  function displayName(name) {
    var f = global.SCCV_LIB_STATS && typeof global.SCCV_LIB_STATS.displayLanguageName === 'function'
      ? global.SCCV_LIB_STATS.displayLanguageName : null;
    return f ? f(name) : String(name == null ? '' : name);
  }

  function sep() {
    var api = global.SCCV_I18N;
    return api && typeof api.getLang === 'function' && api.getLang() === 'en' ? ', ' : '、';
  }

  /* ---------- ①各语言统计规则 ---------- */

  /* 快照语言名列表：≤上限时按名直查；超上限/无快照语言 → 不带 names（后端缺省=快照内全部语言） */
  function requestedNames(job) {
    var names = [];
    if (job && Array.isArray(job.languages)) {
      for (var i = 0; i < job.languages.length; i++) {
        var row = job.languages[i];
        var n = row && typeof row === 'object' ? row.name : null;
        if (typeof n === 'string' && n) names.push(n);
      }
    }
    return names;
  }

  function explainQuery(names) {
    var max = typeof lib().EXPLAIN_MAX_NAMES === 'number' ? lib().EXPLAIN_MAX_NAMES : 50;
    if (names.length === 0 || names.length > max) return '';
    return '?names=' + encodeURIComponent(names.join(','));
  }

  /* 列表值文案：8 项内全列；截断时"符号、符号 等共 N 个"（与后端 8+总数截断同口径） */
  function listValue(shown, total, truncated, format) {
    if (!shown || shown.length === 0) return t('无');
    var parts = [];
    for (var i = 0; i < shown.length; i++) parts.push(format(shown[i]));
    var text = parts.join(sep());
    if (truncated) text += sep() + interpolate('等共 {a} 个', { a: String(total) });
    return text;
  }

  function pairText(pair) {
    return Array.isArray(pair) ? (pair[0] || '') + ' ' + (pair[1] || '') : String(pair == null ? '' : pair);
  }

  function renderRulesBody(body, rows, names) {
    var libE = lib();
    if (typeof libE.buildExplainModel !== 'function') { renderRulesFailure(body); return; }
    var model = libE.buildExplainModel(rows, names);
    var languages = model && Array.isArray(model.languages) ? model.languages : [];

    clear(body);
    body.setAttribute('data-state', 'done');
    if (languages.length === 0) {
      body.appendChild(el('p', 'empty-state', t('说明数据加载失败')));
      return;
    }
    var frag = document.createDocumentFragment();
    for (var i = 0; i < languages.length; i++) {
      frag.appendChild(buildLanguageGroup(languages[i]));
    }
    body.appendChild(frag);
  }

  function buildLanguageGroup(entry) {
    var group = el('div', 'explain-lang');
    group.appendChild(el('span', 'explain-lang-name', displayName(entry.name)));
    var facts = el('ul', 'explain-facts');

    /* 单行注释符号（数据来自 /api/explain；本视图不硬编码任何规则） */
    facts.appendChild(buildFact('单行注释',
      listValue(entry.lineComment, entry.lineCommentTotal, entry.lineCommentTruncated, function (s) { return s; })));
    /* 多行注释符号（每对展示为"开始 结束"） */
    facts.appendChild(buildFact('多行注释',
      listValue(entry.multiLine, entry.multiLineTotal, entry.multiLineTruncated, pairText)));
    /* 复杂度检查项与关键词：仅计数口径（后端不透出清单） */
    facts.appendChild(buildFact('复杂度检查项', interpolate('{a} 条', { a: String(entry.complexityChecks) })));
    if (entry.keywords > 0) {
      facts.appendChild(buildFact('关键词', interpolate('{a} 条', { a: String(entry.keywords) })));
    }
    group.appendChild(facts);
    return group;
  }

  function buildFact(labelKey, value) {
    var li = document.createElement('li');
    var label = el('span', 'explain-fact-label', t(labelKey));
    label.setAttribute('data-i18n', labelKey);
    li.appendChild(label);
    li.appendChild(el('span', 'explain-fact-value', value));
    return li;
  }

  function renderRulesFailure(body) {
    clear(body);
    body.setAttribute('data-state', 'error');
    body.appendChild(el('p', 'empty-state', t('说明数据加载失败')));
  }

  function fetchRules(job) {
    var body = els.rulesBody;
    if (!body) return;
    var names = requestedNames(job);
    var token = ++pendingToken;

    clear(body);
    body.setAttribute('data-state', 'loading');
    body.appendChild(el('p', 'empty-state', t('说明数据加载中…')));

    if (!global.SCCV_APP || typeof global.SCCV_APP.apiFetch !== 'function') {
      renderRulesFailure(body);
      return;
    }
    global.SCCV_APP.apiFetch('/api/explain' + explainQuery(names), { method: 'GET' })
      .then(function (res) {
        if (!res.ok) throw new Error('HTTP ' + res.status);
        return res.json();
      })
      .then(function (rows) {
        if (token !== pendingToken) return; /* 更新的快照已接管渲染 */
        renderRulesBody(body, rows, names);
      })
      .catch(function () {
        if (token !== pendingToken) return;
        renderRulesFailure(body);
      });
  }

  /* ---------- ②未计入统计说明（三类规则性说明；无精确跳过计数，如实呈现） ---------- */

  function buildExcludedItem(titleKey, ruleNodeList) {
    var item = el('div', 'explain-item');
    item.appendChild(el('div', 'explain-item-title', t(titleKey)));
    for (var i = 0; i < ruleNodeList.length; i++) item.appendChild(ruleNodeList[i]);
    return item;
  }

  function ruleLine(key, params) {
    return el('p', 'explain-item-rule', interpolate(key, params || {}));
  }

  function jumpToUnknown() {
    var files = global.SCCV_FILES;
    if (files && typeof files.setLanguageFilter === 'function') {
      try { files.setLanguageFilter(['Unknown']); } catch (err) { /* 过滤失败仍尝试滚动定位 */ }
    }
    var section = $('files-section');
    if (section && typeof section.scrollIntoView === 'function') {
      section.scrollIntoView({ behavior: 'smooth', block: 'start' });
    }
  }

  function renderExcluded(model) {
    var body = els.excludedBody;
    if (!body) return;
    clear(body);
    body.setAttribute('data-state', 'done');
    var m = model || { unknown: { present: false, files: 0 }, options: {}, defaultDenyDirs: [] };

    /* ①未识别类型文件（Unknown 行联动高亮：点击跳转/过滤文件明细 Unknown 行） */
    var unknownNodes = [ruleLine('引擎按扩展名判别语言失败时，文件默认不计入统计。')];
    if (m.unknown && m.unknown.present) {
      unknownNodes.push(ruleLine('本次分析计入 Unknown 类 {a} 个文件。', { a: String(m.unknown.files) }));
      var jump = el('button', 'link-btn explain-jump', t('查看文件明细中的 Unknown 行'));
      jump.type = 'button';
      jump.addEventListener('click', jumpToUnknown);
      unknownNodes.push(jump);
    } else {
      unknownNodes.push(ruleLine('本次分析未勾选“包含未识别类型”，此类文件未计入。'));
    }
    unknownNodes.push(ruleLine('如何调整：在分析目标勾选“包含未识别类型”后重新分析。'));
    body.appendChild(buildExcludedItem('未识别类型文件', unknownNodes));

    /* ②二进制文件（说明文案=派发冻结口径；引擎不产出跳过计数，如实说明） */
    body.appendChild(buildExcludedItem('二进制文件', [
      ruleLine('scc 检测到二进制特征的文件不计入统计。'),
      ruleLine('具体数量引擎未统计。')
    ]));

    /* ③被排除的目录/文件类型（回显当前 options；向导批次落地前快照无此键 → 如实"未指定"） */
    var opts = m.options || {};
    var deny = Array.isArray(m.defaultDenyDirs) ? m.defaultDenyDirs.join(sep()) : '';
    var excludedNodes = [ruleLine('分析只统计目标目录内未被排除的文本文件；引擎默认排除 {a} 目录。', { a: deny })];
    if (Array.isArray(opts.excludeDirs) && opts.excludeDirs.length > 0) {
      excludedNodes.push(ruleLine('本次分析的排除目录：{a}', { a: opts.excludeDirs.join(sep()) }));
    } else {
      excludedNodes.push(ruleLine('本次分析未指定额外排除目录。'));
    }
    if (Array.isArray(opts.fileTypes) && opts.fileTypes.length > 0) {
      excludedNodes.push(ruleLine('本次分析的文件类型筛选：{a}', { a: opts.fileTypes.join(sep()) }));
    }
    excludedNodes.push(ruleLine('具体数量引擎未统计。'));
    excludedNodes.push(ruleLine('如何调整：在分析向导中调整排除目录与文件类型后重新分析。'));
    body.appendChild(buildExcludedItem('被排除的目录与文件类型', excludedNodes));
  }

  /* ---------- 快照接缝：app.js renderSnapshotHook 在 done 时调用 ---------- */

  function render(job) {
    if (!els.rulesBody && !els.excludedBody) return;
    var libE = lib();
    var model = typeof libE.buildExcludedModel === 'function' ? libE.buildExcludedModel(job) : null;
    renderExcluded(model);
    fetchRules(job);
  }

  /* ---------- 初始化 ---------- */

  function domInit() {
    els.rulesBody = $('explain-rules-body');
    els.excludedBody = $('explain-excluded-body');
    if (!els.rulesBody && !els.excludedBody) return; /* 非 App 页面（如 test/unit.html）：跳过 */
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', domInit);
  } else {
    domInit();
  }

  global.SCCV_EXPLAIN = {
    render: render
  };
})(typeof window !== 'undefined' ? window : globalThis);
