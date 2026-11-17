/* sccv 前端文件下钻纯函数库（T-014 · F8）
 * 无 DOM、无副作用：文件行排序 / 组合筛选 / 分页切页 / 筛选集聚合校验和。
 * 冻结契约（.work-log/测试方案.md §0.2-8，被测断言 gui/web/test/test_lib_files.mjs · TC-051~053）：
 *   sortFiles(files, key, dir)：dir='asc'|'desc'；数值键 lines/code/comment/blank/complexity/bytes，
 *     字符串键 path/language/name；稳定排序（等值键保持原相对顺序）；返回新数组、不改入参。
 *   filterFiles(files, {query, language})：query 对 path 大小写不敏感子串；language 精确匹配；
 *     双参空/null → 全量副本（不 mutate）；条件同时给定时取 AND。
 *   paginate(files, page, pageSize)：→ {rows,total,page,pages}；pages=ceil(total/pageSize)（total=0→0）；
 *     页越界 → rows=[]；pageSize 非法（≤0/非数值）→ pages=0 且 rows=[]（不死循环）。
 * 展示辅助（非冻结面，供 files.js 消费）：
 *   sumFiles(files)：→ 7 键聚合 {files,lines,code,comment,blank,complexity,bytes}，
 *     与快照 totals 同键同序——语言筛选后的聚合校验和，用于与概览卡片数字一致性对账（RD-008②）。
 * 双挂载（DEC-023）：node 经 module.exports；浏览器挂 window.SCCV_LIB（平铺）与
 *   window.SCCV_LIB_FILES（分域；test/unit.mjs 垫片做合并兼容）。
 */
(function (global) {
  'use strict';

  /* 快照 7 数值键的权威顺序（与 lib_stats/快照 schema §0.2-4 同序；聚合对象按此顺序构造） */
  var NUMERIC_KEYS = ['files', 'lines', 'code', 'comment', 'blank', 'complexity', 'bytes'];
  var STRING_KEYS = ['path', 'language', 'name'];

  function num(v) {
    var n = Number(v);
    return isFinite(n) ? n : 0;
  }

  /* ---------- 稳定排序（TC-051） ---------- */

  /* 归并式稳定排序：预生成 [{item,index}] 后按 (比较值, 原索引) 双键排序——
   * 等值键必保持原相对顺序，且不依赖引擎对 Array.sort 稳定性的实现差异。 */
  function stableSortBy(arr, compare) {
    var indexed = new Array(arr.length);
    for (var i = 0; i < arr.length; i++) indexed[i] = { item: arr[i], index: i };
    indexed.sort(function (a, b) {
      var c = compare(a.item, b.item);
      return c !== 0 ? c : a.index - b.index;
    });
    var out = new Array(arr.length);
    for (var j = 0; j < indexed.length; j++) out[j] = indexed[j].item;
    return out;
  }

  /* 数值键数值比较、字符串键码元比较（快且确定：'Main.java' < 'src/main.go'）；
   * 未知键 → 恒等比较（退化为原序副本，UI 不致命）。dir!=='desc' 一律按 asc。 */
  function makeComparator(key, dir) {
    var sign = dir === 'desc' ? -1 : 1;
    if (NUMERIC_KEYS.indexOf(key) !== -1) {
      return function (a, b) { return sign * (num(a[key]) - num(b[key])); };
    }
    if (STRING_KEYS.indexOf(key) !== -1) {
      return function (a, b) {
        var as = a[key] == null ? '' : String(a[key]);
        var bs = b[key] == null ? '' : String(b[key]);
        if (as < bs) return -sign;
        if (as > bs) return sign;
        return 0;
      };
    }
    return function () { return 0; };
  }

  function sortFiles(files, key, dir) {
    var arr = Array.isArray(files) ? files : [];
    return stableSortBy(arr, makeComparator(key, dir));
  }

  /* ---------- 组合筛选（TC-052） ---------- */

  function filterFiles(files, cond) {
    var arr = Array.isArray(files) ? files : [];
    var c = (cond && typeof cond === 'object') ? cond : {};
    var query = (typeof c.query === 'string' && c.query) ? c.query.toLowerCase() : '';
    var language = (typeof c.language === 'string' && c.language) ? c.language : null;
    if (!query && !language) return arr.slice(); /* 双空条件 → 全量副本 */
    var out = [];
    for (var i = 0; i < arr.length; i++) {
      var f = arr[i];
      if (!f) continue;
      if (language && f.language !== language) continue;
      if (query) {
        var p = typeof f.path === 'string' ? f.path.toLowerCase() : '';
        if (p.indexOf(query) === -1) continue;
      }
      out.push(f);
    }
    return out;
  }

  /* ---------- 分页切页（TC-053） ---------- */

  function paginate(files, page, pageSize) {
    var arr = Array.isArray(files) ? files : [];
    var total = arr.length;
    var size = Number(pageSize);
    var p = Math.trunc(Number(page));
    if (!isFinite(p)) p = 1;
    if (!isFinite(size) || size <= 0) return { rows: [], total: total, page: p, pages: 0 };
    if (p < 1) p = 1;
    var pages = Math.ceil(total / size);
    var start = (p - 1) * size;
    var rows = start < total ? arr.slice(start, start + size) : [];
    return { rows: rows, total: total, page: p, pages: pages };
  }

  /* ---------- 筛选集聚合校验和（RD-008②：与概览卡片联动的一致性数字） ---------- */

  /* 对任意（语言筛选/搜索后）文件集合求 7 键聚合；无筛选时与快照 totals 逐键相等。
   * 非法条目跳过（files 计数同时跳过，保证"文件数"口径=有效条目数）。 */
  function sumFiles(files) {
    var out = { files: 0, lines: 0, code: 0, comment: 0, blank: 0, complexity: 0, bytes: 0 };
    var arr = Array.isArray(files) ? files : [];
    for (var i = 0; i < arr.length; i++) {
      var f = arr[i];
      if (!f || typeof f !== 'object') continue;
      out.files += 1;
      for (var k = 0; k < NUMERIC_KEYS.length; k++) {
        out[NUMERIC_KEYS[k]] += num(f[NUMERIC_KEYS[k]]);
      }
    }
    return out;
  }

  /* ---------- 双挂载导出 ---------- */

  var SCCV_LIB_FILES = {
    sortFiles: sortFiles,
    filterFiles: filterFiles,
    paginate: paginate,
    sumFiles: sumFiles
  };

  if (typeof module !== 'undefined' && module.exports) module.exports = SCCV_LIB_FILES;

  if (typeof global !== 'undefined') {
    global.SCCV_LIB_FILES = SCCV_LIB_FILES;
    var flat = global.SCCV_LIB;
    if (!flat || typeof flat !== 'object') {
      flat = {};
      global.SCCV_LIB = flat;
    }
    for (var name in SCCV_LIB_FILES) {
      if (Object.prototype.hasOwnProperty.call(SCCV_LIB_FILES, name)) flat[name] = SCCV_LIB_FILES[name];
    }
  }
})(typeof window !== 'undefined' ? window : globalThis);
