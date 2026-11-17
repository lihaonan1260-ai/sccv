/* sccv 向导目录树纯函数库（OPT-001/T-201）
 * 无 DOM、无副作用：后端 /api/tree 载荷净化为带三态勾选状态的树、
 * collectExcluded(根, 树)=最顶层未勾节点的 root 相对斜杠段集（父未勾→整子树丢弃，最小化）、
 * 绝对路径 → root 相对段换算。断言：gui/web/test/test_lib_tree.mjs（node 直跑 + test/unit.html 双载体）。
 * 节点模型：{name, path, state:'checked'|'unchecked'|'partial', children:[...]}（normalizeTree 产出，默认全勾）。
 * 双挂载（DEC-023）：node 经 module.exports；浏览器挂 window.SCCV_LIB_TREE 并平铺进 window.SCCV_LIB。
 */
(function (global) {
  'use strict';

  /* ---------- 载荷净化 ---------- */

  /* payload {nodes:[{name,path,children}], truncated} → {nodes, truncated}；
   * 条目须有非空 name 与 path，children 递归净化；默认 state='checked'。
   * 非法载荷（null/缺 nodes/条目缺字段）静默剔除，绝不抛错。 */
  function normalizeTree(payload) {
    var out = { nodes: [], truncated: false };
    if (!payload || typeof payload !== 'object') return out;
    out.truncated = !!payload.truncated;
    var list = Array.isArray(payload.nodes) ? payload.nodes : [];
    for (var i = 0; i < list.length; i++) {
      var node = buildNode(list[i]);
      if (node) out.nodes.push(node);
    }
    return out;
  }

  function buildNode(raw) {
    if (!raw || typeof raw !== 'object') return null;
    if (!nonEmptyString(raw.name) || !nonEmptyString(raw.path)) return null;
    var children = [];
    var list = Array.isArray(raw.children) ? raw.children : [];
    for (var i = 0; i < list.length; i++) {
      var child = buildNode(list[i]);
      if (child) children.push(child);
    }
    return { name: raw.name, path: raw.path, state: 'checked', children: children };
  }

  function nonEmptyString(v) {
    return typeof v === 'string' && v !== '';
  }

  /* ---------- 路径换算 ---------- */

  /* 绝对路径 → root 之下的斜杠段（"src/gen"）；root 归一为正斜杠并去尾分隔符。
   * path 等于 root → ''；不在 root 之下或输入非法 → null。 */
  function relativeSegment(root, path) {
    if (!nonEmptyString(root) || !nonEmptyString(path)) return null;
    var r = root.replace(/\\/g, '/').replace(/\/+$/, '');
    var p = path.replace(/\\/g, '/');
    if (p === r) return '';
    if (p.indexOf(r + '/') !== 0) return null;
    return p.slice(r.length + 1);
  }

  /* ---------- 排除集收集 ---------- */

  /* collectExcluded(root, nodes)：最顶层未勾节点的 root 相对斜杠段列表。
   * 勾选模型（DEC-O02 单模型）：unchecked=整子树排除（不再下钻=最小化）；
   * partial=部分排除（下钻收集未勾后代）；checked=全包含（跳过）。
   * 顺序=树序；非法输入 → []。 */
  function collectExcluded(root, nodes) {
    var out = [];
    if (!Array.isArray(nodes)) return out;
    walkCollect(root, nodes, out);
    return out;
  }

  function walkCollect(root, nodes, out) {
    for (var i = 0; i < nodes.length; i++) {
      var node = nodes[i];
      if (!node || typeof node !== 'object') continue;
      if (node.state === 'unchecked') {
        var seg = relativeSegment(root, node.path);
        if (seg) out.push(seg);
        continue; /* 整子树丢弃：不下钻（嵌套最小化） */
      }
      if (node.state === 'checked') continue; /* 全包含：子树必全勾 */
      if (Array.isArray(node.children) && node.children.length) {
        walkCollect(root, node.children, out); /* partial：下钻 */
      }
    }
  }

  /* ---------- 双挂载导出 ---------- */

  var SCCV_LIB_TREE = {
    normalizeTree: normalizeTree,
    relativeSegment: relativeSegment,
    collectExcluded: collectExcluded
  };

  if (typeof module !== 'undefined' && module.exports) module.exports = SCCV_LIB_TREE;

  if (typeof global !== 'undefined') {
    global.SCCV_LIB_TREE = SCCV_LIB_TREE;
    var flat = global.SCCV_LIB;
    if (!flat || typeof flat !== 'object') {
      flat = {};
      global.SCCV_LIB = flat;
    }
    for (var name in SCCV_LIB_TREE) {
      if (Object.prototype.hasOwnProperty.call(SCCV_LIB_TREE, name)) flat[name] = SCCV_LIB_TREE[name];
    }
  }
})(typeof window !== 'undefined' ? window : globalThis);
