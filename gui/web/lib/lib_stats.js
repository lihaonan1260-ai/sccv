/* sccv 前端统计纯函数库（T-012）
 * 无 DOM、无副作用：按语言聚合、占比计算、语言行排序、字节/百分比展示格式化、合计推导。
 * 冻结契约（.work-log/测试方案.md §0.2-8，被测断言 gui/web/test/test_lib_stats.mjs）：
 *   aggregateByLanguage(files)：入参 [{language,lines,code,comment,blank,complexity,bytes}] →
 *     行 {name,files,lines,code,comment,blank,complexity,bytes}，按 lines 降序、同值按 name 升序；空输入 → []。
 *   shareOf(rows, field)：→ [{name,value,pct}]，pct=Math.round(v/total*10000)/100；total=0 → pct 全 0；顺序透传。
 *   filterLanguages(rows, selected)：selected 空/null → 全量副本（不 mutate、返回新数组）；否则按 name 保序过滤。
 * 双挂载（DEC-023）：node 经 module.exports；浏览器挂 window.SCCV_LIB（平铺）与
 *   window.SCCV_LIB_STATS（分域；test/unit.mjs 垫片做合并兼容）。
 * 展示辅助（非冻结面，供 stats.js 消费，断言见 test/test_stats_extra.mjs）：
 *   formatBytes / formatPct / displayLanguageName / sortLanguageRows / sumTotals；
 *   ratioSuffix（OPT-004 合计行内联占比后缀，断言见 test/test_lib_stats.mjs）。
 */
(function (global) {
  'use strict';

  /* 汇总行 7 数值键的权威顺序（构造对象必须按此顺序，冻结断言以 JSON 字符串全量比对） */
  var NUMERIC_KEYS = ['files', 'lines', 'code', 'comment', 'blank', 'complexity', 'bytes'];

  function num(v) {
    var n = Number(v);
    return isFinite(n) ? n : 0;
  }

  /* ---------- 聚合 ---------- */

  /* 按语言聚合文件条目；language 非空字符串的条目才计入（非法条目直接跳过）。 */
  function aggregateByLanguage(files) {
    if (!Array.isArray(files)) return [];
    var byName = Object.create(null);
    var order = [];
    for (var i = 0; i < files.length; i++) {
      var f = files[i];
      if (!f || typeof f.language !== 'string' || !f.language) continue;
      var name = f.language;
      var row = byName[name];
      if (!row) {
        row = { name: name, files: 0, lines: 0, code: 0, comment: 0, blank: 0, complexity: 0, bytes: 0 };
        byName[name] = row;
        order.push(name);
      }
      row.files += 1;
      for (var k = 1; k < NUMERIC_KEYS.length; k++) {
        var key = NUMERIC_KEYS[k];
        row[key] += num(f[key]);
      }
    }
    var rows = [];
    for (var j = 0; j < order.length; j++) rows.push(byName[order[j]]);
    rows.sort(function (a, b) {
      if (b.lines !== a.lines) return b.lines - a.lines; /* lines 降序 */
      return a.name < b.name ? -1 : (a.name > b.name ? 1 : 0); /* 同值名称升序 */
    });
    return rows;
  }

  /* ---------- 占比 ---------- */

  /* rows → [{name,value,pct}]；pct=Math.round(v/total*10000)/100；total=0 → 全 0；顺序透传。 */
  function shareOf(rows, field) {
    if (!Array.isArray(rows)) return [];
    var total = 0;
    var i, r;
    for (i = 0; i < rows.length; i++) {
      r = rows[i];
      if (r) total += num(r[field]);
    }
    var out = [];
    for (i = 0; i < rows.length; i++) {
      r = rows[i];
      if (!r) continue;
      var v = num(r[field]);
      out.push({
        name: r.name,
        value: v,
        pct: total > 0 ? Math.round(v / total * 10000) / 100 : 0
      });
    }
    return out;
  }

  /* 语言选集过滤：空/null 选集 → 全量副本；否则保留 name 命中选集的行，顺序保持；不改入参。 */
  function filterLanguages(rows, selected) {
    if (!Array.isArray(rows)) return [];
    var copy = rows.slice();
    if (!Array.isArray(selected) || selected.length === 0) return copy;
    var want = Object.create(null);
    for (var i = 0; i < selected.length; i++) {
      if (typeof selected[i] === 'string' && selected[i]) want[selected[i]] = true;
    }
    var out = [];
    for (var j = 0; j < copy.length; j++) {
      if (copy[j] && want[copy[j].name]) out.push(copy[j]);
    }
    return out;
  }

  /* ---------- 展示格式化 ---------- */

  var BYTE_UNITS = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];

  /* 字节人类可读：1024 进制；<1024 整数字节；其余保留 1 位小数（整数小数省略）。负值按 0。 */
  function formatBytes(n) {
    var v = num(n);
    if (v < 0) v = 0;
    var u = 0;
    while (v >= 1024 && u < BYTE_UNITS.length - 1) {
      v /= 1024;
      u += 1;
    }
    if (u === 0) return Math.round(v) + ' B';
    return (Math.round(v * 10) / 10) + ' ' + BYTE_UNITS[u];
  }

  /* 百分比展示：最多 1 位小数、整数不带小数点（48.28→"48.3%"、100→"100%"）。 */
  function formatPct(p) {
    var v = Math.round(num(p) * 10) / 10;
    return (v % 1 === 0 ? String(v) : v.toFixed(1)) + '%';
  }

  /* OPT-004：合计行内联占比后缀——n 占 total 的百分比（shareOf 同口径：round(v/total*10000)/100，
   * 格式化唯一出口=formatPct），返回形如 " (52.3%)" 的后缀；total<=0（lines=0）→ 空后缀（只显原值）。 */
  function ratioSuffix(n, total) {
    var t = num(total);
    if (t <= 0) return '';
    return ' (' + formatPct(Math.round(num(n) / t * 10000) / 100) + ')';
  }

  /* K4：Unknown 为数据值保留英文，呈现层标注中文"未识别 (Unknown)"。 */
  function displayLanguageName(name) {
    if (name === 'Unknown') return '未识别 (Unknown)';
    return String(name == null ? '' : name);
  }

  /* ---------- 语言行排序 ---------- */

  var LANG_SORTABLE = {
    name: true, files: true, lines: true, code: true,
    comment: true, blank: true, complexity: true, bytes: true
  };

  /* 语言行排序：返回新数组（不 mutate；ES2019+ sort 稳定）。
   * 数值键数值比较；name 字符串比较；dir='asc'|'desc'（缺省 desc）；未知键回退 lines/desc。 */
  function sortLanguageRows(rows, key, dir) {
    if (!Array.isArray(rows)) return [];
    var out = rows.slice();
    var k = LANG_SORTABLE[key] ? key : 'lines';
    var mul = dir === 'asc' ? 1 : -1;
    out.sort(function (a, b) {
      if (!a || !b) return a ? -1 : 1;
      if (k === 'name') {
        var an = String(a.name == null ? '' : a.name);
        var bn = String(b.name == null ? '' : b.name);
        if (an !== bn) return (an < bn ? -1 : 1) * mul;
        return 0;
      }
      var av = num(a[k]);
      var bv = num(b[k]);
      if (av !== bv) return (av - bv) * mul;
      return 0;
    });
    return out;
  }

  /* ---------- 合计推导 ---------- */

  /* 由语言行求和得 totals（快照含合法 totals 时的正常路径不经此函数；缺失时的兜底推导）。 */
  function sumTotals(rows) {
    var totals = { files: 0, lines: 0, code: 0, comment: 0, blank: 0, complexity: 0, bytes: 0 };
    if (!Array.isArray(rows)) return totals;
    for (var i = 0; i < rows.length; i++) {
      var r = rows[i];
      if (!r) continue;
      for (var k = 0; k < NUMERIC_KEYS.length; k++) {
        var key = NUMERIC_KEYS[k];
        totals[key] += num(r[key]);
      }
    }
    return totals;
  }

  /* ---------- 双挂载导出 ---------- */

  var SCCV_LIB_STATS = {
    aggregateByLanguage: aggregateByLanguage,
    shareOf: shareOf,
    filterLanguages: filterLanguages,
    formatBytes: formatBytes,
    formatPct: formatPct,
    ratioSuffix: ratioSuffix,
    displayLanguageName: displayLanguageName,
    sortLanguageRows: sortLanguageRows,
    sumTotals: sumTotals
  };

  if (typeof module !== 'undefined' && module.exports) module.exports = SCCV_LIB_STATS;

  if (typeof global !== 'undefined') {
    global.SCCV_LIB_STATS = SCCV_LIB_STATS;
    var flat = global.SCCV_LIB;
    if (!flat || typeof flat !== 'object') {
      flat = {};
      global.SCCV_LIB = flat;
    }
    for (var name in SCCV_LIB_STATS) {
      if (Object.prototype.hasOwnProperty.call(SCCV_LIB_STATS, name)) flat[name] = SCCV_LIB_STATS[name];
    }
  }
})(typeof window !== 'undefined' ? window : globalThis);
