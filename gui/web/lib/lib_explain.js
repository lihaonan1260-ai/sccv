/* sccv 前端口径说明纯函数库（T-301 · OPT-003）
 * 无 DOM、无副作用：把 /api/explain 响应与快照归一为说明卡渲染模型。
 * 契约（被测断言 gui/web/test/test_lib_explain.mjs · 派发三例：正常/未知名略过/截断 8+总数）：
 *   buildExplainModel(rows, requestedNames)：
 *     rows 为 GET /api/explain 响应数组；name 非空字符串才保留（未知名/畸形条目略过，不抛错）；
 *     requestedNames 为快照语言名列表，模型回填 missingNames=请求了但响应中无对应条目的名
 *     （去重、保序）——即后端"未知名略过"的可见回执；
 *     lineComment/multiLine 展示层最多 EXPLAIN_FIELD_LIMIT=8 项 + total 总数（后端已按 8+总数截断，
 *     此处二次兜底同口径）：total=max(响应 total(有限数值), 实际条数)，truncated=展示项数<total；
 *     languages 按 name 不区分大小写升序；复杂度/关键词计数非负整数化；返回新对象、不改入参。
 *   buildExcludedModel(snapshot)：
 *     unknown={present,files}（快照 languages 中 name==='Unknown' 行 files 合计）；
 *     options={includeUnknown,languages} 原样回读；excludeDirs/fileTypes 仅当存在且为数组才回显
 *     （向导批次落地前的快照无此键 → null，说明卡据此如实呈现"未指定"）；
 *     defaultDenyDirs 恒为 ['.git','.hg','.svn']（引擎内置 PathDenyList 默认，gui/analyze.go 同值）；
 *     snapshot 非法（null/非对象）→ 全空态模型，不抛错。
 * 双挂载（DEC-023）：node 经 module.exports；浏览器挂 window.SCCV_LIB_EXPLAIN（分域；
 *   test/unit.mjs 垫片做合并兼容）。
 * 安全纪律（DEC-026）：纯数据归一，无任何 DOM/网络访问；展示文案一律由 explain.js 经安全文本 API 构建。
 */
(function (global) {
  'use strict';

  /* 展示层字段截断上限（与后端 handlers_explain.go explainFieldCut=8 同值同义：8 项+总数） */
  var EXPLAIN_FIELD_LIMIT = 8;
  /* /api/explain names 参数上限（与后端 explainMaxNames=50 同值；超限前端不传 names，走后端缺省） */
  var EXPLAIN_MAX_NAMES = 50;

  function isPlainObject(v) {
    return !!v && typeof v === 'object' && !Array.isArray(v);
  }

  function isNonEmptyString(v) {
    return typeof v === 'string' && v.length > 0;
  }

  function countOf(v) {
    var n = Number(v);
    if (!isFinite(n) || n < 0) n = 0;
    return Math.floor(n);
  }

  /* 字符串列表归一：仅保留非空字符串项 */
  function stringList(v) {
    if (!Array.isArray(v)) return [];
    var out = [];
    for (var i = 0; i < v.length; i++) {
      if (isNonEmptyString(v[i])) out.push(v[i]);
    }
    return out;
  }

  /* 多行注释对归一：每对归一为非空字符串数组（如 ['/*','*\/']） */
  function pairList(v) {
    if (!Array.isArray(v)) return [];
    var out = [];
    for (var i = 0; i < v.length; i++) {
      var pair = stringList(v[i]);
      if (pair.length > 0) out.push(pair);
    }
    return out;
  }

  /* 截断 8+总数（展示层兜底，与后端同口径）：total 取 max(声明 total, 实际条数)，防展示层谎报 */
  function cutField(list, declaredTotal) {
    var shown = list.slice(0, EXPLAIN_FIELD_LIMIT);
    var total = countOf(declaredTotal);
    if (total < list.length) total = list.length;
    return { shown: shown, total: total, truncated: shown.length < total };
  }

  function buildExplainModel(rows, requestedNames) {
    var list = Array.isArray(rows) ? rows : [];
    var languages = [];
    for (var i = 0; i < list.length; i++) {
      var row = list[i];
      if (!isPlainObject(row) || !isNonEmptyString(row.name)) continue; /* 未知名/畸形条目略过 */
      var line = cutField(stringList(row.lineComment), row.lineCommentTotal);
      var multi = cutField(pairList(row.multiLine), row.multiLineTotal);
      languages.push({
        name: row.name,
        lineComment: line.shown,
        lineCommentTotal: line.total,
        lineCommentTruncated: line.truncated,
        multiLine: multi.shown,
        multiLineTotal: multi.total,
        multiLineTruncated: multi.truncated,
        complexityChecks: countOf(row.complexityChecks),
        keywords: countOf(row.keywords)
      });
    }
    languages.sort(function (a, b) {
      var la = a.name.toLowerCase(), lb = b.name.toLowerCase();
      if (la !== lb) return la < lb ? -1 : 1;
      return a.name < b.name ? -1 : (a.name > b.name ? 1 : 0);
    });

    /* missingNames：请求了但响应无数据的名（后端"未知名略过"的可见回执），去重保序 */
    var wanted = Array.isArray(requestedNames) ? requestedNames : [];
    var seen = Object.create(null);
    for (var k = 0; k < languages.length; k++) seen[languages[k].name] = true;
    var missing = [];
    var wantedSeen = Object.create(null);
    for (var j = 0; j < wanted.length; j++) {
      var name = wanted[j];
      if (!isNonEmptyString(name) || wantedSeen[name]) continue;
      wantedSeen[name] = true;
      if (!seen[name]) missing.push(name);
    }
    return { languages: languages, missingNames: missing, maxNames: EXPLAIN_MAX_NAMES };
  }

  /* 引擎内置排除目录（PathDenyList 默认，gui/analyze.go applyAnalysisFlags 同值；只读副本） */
  var DEFAULT_DENY_DIRS = ['.git', '.hg', '.svn'];

  function buildExcludedModel(snapshot) {
    var languages = isPlainObject(snapshot) && Array.isArray(snapshot.languages) ? snapshot.languages : [];
    var unknownFiles = 0;
    for (var i = 0; i < languages.length; i++) {
      var row = languages[i];
      if (isPlainObject(row) && row.name === 'Unknown') unknownFiles += countOf(row.files);
    }

    var options = isPlainObject(snapshot) && isPlainObject(snapshot.options) ? snapshot.options : {};
    /* excludeDirs/fileTypes：仅当存在且为字符串数组才回显（向导批次前快照无此键 → null） */
    function stringArrayOr(v) {
      if (!Array.isArray(v)) return null;
      var out = [];
      for (var k = 0; k < v.length; k++) {
        if (isNonEmptyString(v[k])) out.push(v[k]);
      }
      return out;
    }

    return {
      unknown: { present: unknownFiles > 0, files: unknownFiles },
      options: {
        includeUnknown: !!options.includeUnknown,
        languages: Array.isArray(options.languages) ? options.languages : null,
        excludeDirs: stringArrayOr(options.excludeDirs),
        fileTypes: stringArrayOr(options.fileTypes)
      },
      defaultDenyDirs: DEFAULT_DENY_DIRS.slice()
    };
  }

  var SCCV_LIB_EXPLAIN = {
    EXPLAIN_FIELD_LIMIT: EXPLAIN_FIELD_LIMIT,
    EXPLAIN_MAX_NAMES: EXPLAIN_MAX_NAMES,
    buildExplainModel: buildExplainModel,
    buildExcludedModel: buildExcludedModel
  };

  if (typeof module !== 'undefined' && module.exports) module.exports = SCCV_LIB_EXPLAIN;
  if (typeof global !== 'undefined') global.SCCV_LIB_EXPLAIN = SCCV_LIB_EXPLAIN;
})(typeof window !== 'undefined' ? window : globalThis);
