/* sccv 前端分布图纯函数库（T-013 · F7 语言分布图数据面）
 * 冻结契约：.work-log/测试方案.md §0.2-8 · test_lib_charts.mjs（TC-046~049 charts 面）：
 *   pieSlices(rows)：rows=[{name,value}] → [{name,value,startAngle,endAngle}]，
 *     弧度制、自 0 顺时针累计、末角=2π；空集或 total=0 → []。
 *   donutSlices(rows, innerRadius)：角度与 pieSlices 一致 + innerRadius 透传。
 *   polarToCartesian(cx,cy,r,angle)：屏幕坐标系 y 向下（x=cx+r·cos，y=cy+r·sin）。
 * 几何思路参照 processor/report_render.go donutArcs 的"占比→弧段顺时针累计"，实现独立
 * （角度弧度制 + SVG path 直出），未复制其代码。
 * 其余展示辅助（非冻结面，供 charts.js 消费）：
 *   arcPath（扇形/环段 path d）、colorForLanguage（固定调色板 + 语言名稳定映射）、
 *   selectRows（按名选集过滤，语义与 lib_stats.filterLanguages 对齐）、legendData（图例组装）。
 * 双挂载（DEC-023）：node 经 module.exports；浏览器挂 window.SCCV_LIB_CHARTS（分域）
 *   并平铺进 window.SCCV_LIB（与 test/unit.mjs 垫片合并兼容）。
 * 安全：无 DOM、无 IO、无内联字符串拼标记。
 */
(function (global) {
  'use strict';

  var TAU = Math.PI * 2;
  var EPS = 1e-9;

  /* ---------- 调色板：固定序列；常见语言名稳定映射（与行序/筛选无关） ---------- */

  /* 固定分类色序列（14 色；兜底用，经名称哈希取槽位 → 同名恒同色） */
  var PALETTE = [
    '#4e79a7', '#f28e2b', '#59a14f', '#e15759', '#b07aa1', '#76b7b2', '#edc948',
    '#ff9da7', '#9c755f', '#bab0ac', '#8cd17d', '#499894', '#d37295', '#a0cbe8'
  ];

  /* 常见语言稳定色（业界通用语言标识色，与 scc HTML 报告观感一致；数据值为事实引用） */
  var KNOWN_COLORS = {
    'Go': '#00ADD8',
    'JavaScript': '#f1e05a',
    'TypeScript': '#3178c6',
    'Python': '#3572A5',
    'Java': '#b07219',
    'C': '#555555',
    'C++': '#f34b7d',
    'C#': '#178600',
    'Ruby': '#701516',
    'Rust': '#dea584',
    'PHP': '#4F5D95',
    'Swift': '#F05138',
    'Kotlin': '#A97BFF',
    'Shell': '#89e051',
    'HTML': '#e34c26',
    'CSS': '#563d7c',
    'Markdown': '#083fa1',
    'YAML': '#cb171e',
    'JSON': '#292929',
    'XML': '#0060ac',
    'SQL': '#e38c00',
    'Dockerfile': '#384d54',
    'Makefile': '#427819',
    'Unknown': '#999999'
  };

  /* FNV-1a 32 位哈希：语言名 → 调色板槽位（稳定、与顺序无关） */
  function hashName(name) {
    var h = 0x811c9dc5;
    var s = String(name);
    for (var i = 0; i < s.length; i++) {
      h ^= s.charCodeAt(i);
      h = Math.imul(h, 0x01000193) >>> 0;
    }
    return h >>> 0;
  }

  function colorForLanguage(name) {
    if (typeof name === 'string' && Object.prototype.hasOwnProperty.call(KNOWN_COLORS, name)) {
      return KNOWN_COLORS[name];
    }
    return PALETTE[hashName(name) % PALETTE.length];
  }

  /* ---------- 行规整：仅接受 {name:非空字符串, value:有限非负数}；越界条目在一处剔除 ---------- */

  function normalizeRows(rows) {
    if (!Array.isArray(rows)) return [];
    var out = [];
    for (var i = 0; i < rows.length; i++) {
      var r = rows[i];
      if (!r || typeof r !== 'object') continue;
      var v = Number(r.value);
      if (typeof r.name !== 'string' || r.name === '' || !isFinite(v) || v < 0) continue;
      out.push({ name: r.name, value: v });
    }
    return out;
  }

  /* ---------- 角度几何（冻结面） ---------- */

  /* 占比 → 角度：自 0 顺时针累计；末片末角精确钳到 2π（消浮点漂移，衔接断言 1e-9 内） */
  function pieSlices(rows) {
    var clean = normalizeRows(rows);
    var total = 0;
    for (var i = 0; i < clean.length; i++) total += clean[i].value;
    if (!(total > 0)) return [];
    var slices = [];
    var angle = 0;
    for (var j = 0; j < clean.length; j++) {
      var end = (j === clean.length - 1) ? TAU : angle + (clean[j].value / total) * TAU;
      slices.push({ name: clean[j].name, value: clean[j].value, startAngle: angle, endAngle: end });
      angle = end;
    }
    return slices;
  }

  function donutSlices(rows, innerRadius) {
    var base = pieSlices(rows);
    var ir = (typeof innerRadius === 'number' && isFinite(innerRadius) && innerRadius > 0) ? innerRadius : 0;
    var out = [];
    for (var i = 0; i < base.length; i++) {
      out.push({
        name: base[i].name,
        value: base[i].value,
        startAngle: base[i].startAngle,
        endAngle: base[i].endAngle,
        innerRadius: ir
      });
    }
    return out;
  }

  function polarToCartesian(cx, cy, r, angle) {
    return { x: cx + r * Math.cos(angle), y: cy + r * Math.sin(angle) };
  }

  /* ---------- SVG path 直出（展示辅助） ---------- */

  function fmt(n) {
    return String(Math.round(n * 1000) / 1000);
  }

  /* 扇形（rInner=0）或环段（rInner>0）的 path d；跨度 0/参数非法 → ''。
   * 屏幕坐标系 y 向下，角度增加方向即顺时针（sweep=1）；整圆拆两段半弧（A 无法单段 360°）。 */
  function arcPath(cx, cy, rOuter, rInner, startAngle, endAngle) {
    if (!(rOuter > 0) || !isFinite(startAngle) || !isFinite(endAngle)) return '';
    var span = endAngle - startAngle;
    if (!(span > 0)) return '';
    if (span > TAU) span = TAU;
    var ir = (typeof rInner === 'number' && isFinite(rInner) && rInner > 0) ? rInner : 0;
    if (ir >= rOuter) return '';
    var full = span >= TAU - EPS;
    var large = span > Math.PI + EPS ? 1 : 0;
    var o0 = polarToCartesian(cx, cy, rOuter, startAngle);
    var oMid = polarToCartesian(cx, cy, rOuter, startAngle + span / 2);
    var o1 = polarToCartesian(cx, cy, rOuter, startAngle + span);

    if (ir === 0) {
      if (full) {
        var f0 = polarToCartesian(cx, cy, rOuter, 0);
        var f1 = polarToCartesian(cx, cy, rOuter, Math.PI);
        return 'M' + fmt(f0.x) + ' ' + fmt(f0.y) +
          'A' + fmt(rOuter) + ' ' + fmt(rOuter) + ' 0 1 1 ' + fmt(f1.x) + ' ' + fmt(f1.y) +
          'A' + fmt(rOuter) + ' ' + fmt(rOuter) + ' 0 1 1 ' + fmt(f0.x) + ' ' + fmt(f0.y) + 'Z';
      }
      return 'M' + fmt(cx) + ' ' + fmt(cy) +
        'L' + fmt(o0.x) + ' ' + fmt(o0.y) +
        'A' + fmt(rOuter) + ' ' + fmt(rOuter) + ' 0 ' + large + ' 1 ' + fmt(o1.x) + ' ' + fmt(o1.y) + 'Z';
    }

    var i1 = polarToCartesian(cx, cy, ir, startAngle + span);
    var i0 = polarToCartesian(cx, cy, ir, startAngle);
    if (full) {
      return 'M' + fmt(o0.x) + ' ' + fmt(o0.y) +
        'A' + fmt(rOuter) + ' ' + fmt(rOuter) + ' 0 1 1 ' + fmt(oMid.x) + ' ' + fmt(oMid.y) +
        'A' + fmt(rOuter) + ' ' + fmt(rOuter) + ' 0 1 1 ' + fmt(o0.x) + ' ' + fmt(o0.y) +
        'L' + fmt(i0.x) + ' ' + fmt(i0.y) +
        'A' + fmt(ir) + ' ' + fmt(ir) + ' 0 1 0 ' + fmt(polarToCartesian(cx, cy, ir, Math.PI).x) + ' ' + fmt(polarToCartesian(cx, cy, ir, Math.PI).y) +
        'A' + fmt(ir) + ' ' + fmt(ir) + ' 0 1 0 ' + fmt(i0.x) + ' ' + fmt(i0.y) + 'Z';
    }
    return 'M' + fmt(o0.x) + ' ' + fmt(o0.y) +
      'A' + fmt(rOuter) + ' ' + fmt(rOuter) + ' 0 ' + large + ' 1 ' + fmt(o1.x) + ' ' + fmt(o1.y) +
      'L' + fmt(i1.x) + ' ' + fmt(i1.y) +
      'A' + fmt(ir) + ' ' + fmt(ir) + ' 0 ' + large + ' 0 ' + fmt(i0.x) + ' ' + fmt(i0.y) + 'Z';
  }

  /* ---------- 选集过滤与图例组装（展示辅助） ---------- */

  /* 按名选集过滤：selected 空/null/非数组 → 全量副本（与 lib_stats.filterLanguages 空选集口径一致）；
   * 数组 → 按 rows 顺序保留命中行；不 mutate 入参。 */
  function selectRows(rows, selected) {
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

  /* 图例数据：{name, value, pct, color}；pct 公式与 lib_stats.shareOf 一致
   * （Math.round(v/total*10000)/100，total=0 → 全 0），跨视图数字同口径（RD-007）。 */
  function legendData(rows) {
    var clean = normalizeRows(rows);
    var total = 0;
    for (var i = 0; i < clean.length; i++) total += clean[i].value;
    var out = [];
    for (var j = 0; j < clean.length; j++) {
      out.push({
        name: clean[j].name,
        value: clean[j].value,
        pct: total > 0 ? Math.round(clean[j].value / total * 10000) / 100 : 0,
        color: colorForLanguage(clean[j].name)
      });
    }
    return out;
  }

  /* ---------- 双挂载导出 ---------- */

  var SCCV_LIB_CHARTS = {
    pieSlices: pieSlices,
    donutSlices: donutSlices,
    polarToCartesian: polarToCartesian,
    arcPath: arcPath,
    colorForLanguage: colorForLanguage,
    selectRows: selectRows,
    legendData: legendData,
    PALETTE: PALETTE
  };

  if (typeof module !== 'undefined' && module.exports) module.exports = SCCV_LIB_CHARTS;

  if (typeof global !== 'undefined') {
    global.SCCV_LIB_CHARTS = SCCV_LIB_CHARTS;
    global.SCCV_LIB = global.SCCV_LIB || {};
    for (var k in SCCV_LIB_CHARTS) {
      if (Object.prototype.hasOwnProperty.call(SCCV_LIB_CHARTS, k) && !(k in global.SCCV_LIB)) {
        global.SCCV_LIB[k] = SCCV_LIB_CHARTS[k];
      }
    }
  }
})(typeof window !== 'undefined' ? window : globalThis);
