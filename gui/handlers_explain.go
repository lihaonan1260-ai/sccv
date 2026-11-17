package gui

// 口径说明端点（T-301/OPT-003）：GET /api/explain?names=Go,Python 把引擎语言数据库的
// 计数规则按语言透出给前端"统计口径说明"卡。只读面（DEC-002 同源）：数据全部来自
// processor.LanguageDatabase()，逐字段裁剪复制，响应不别名引擎状态。
//
// 口径（DEC-O06）：names 逗号分隔、去空、≤ explainMaxNames 个（超限 400，快速失败）；
// 数据库不认识的名一律静默略过（与 /api/analyze 的整单 400 语义不同——本端点是说明性读面，
// 单个未知名不该让其余语言的规则说明不可用）；names 缺省时取本实例最近一次完成分析的
// 快照语言集合（DI-002 同源，latestSnapshotFor），无快照则为空结果。
//
// 防响应爆炸：lineComment/multiLine 两个清单字段按 explainFieldCut 截断为"前 8 项+总数"，
// 总数独立成字段如实回传；complexityChecks/keywords 仅透出计数（引擎侧本就是计数口径）。

import (
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/boyter/scc/v4/processor"
)

const (
	// explainNamesParam is the query parameter carrying the requested language names.
	explainNamesParam = "names"
	// explainMaxNames caps the parsed names list; anything larger is a 400.
	explainMaxNames = 50
	// explainFieldCut is how many entries of a list field travel on the wire
	// before the payload switches to "8 items + total" truncation.
	explainFieldCut = 8
)

// explainLanguage is one language row of the /api/explain payload: the counting
// rules the SPA's scope-notes card renders. List fields are truncated copies
// (first explainFieldCut entries); the totals travel beside them so the card
// can show "8 shown of N" honestly.
type explainLanguage struct {
	Name             string     `json:"name"`
	LineComment      []string   `json:"lineComment"`
	LineCommentTotal int        `json:"lineCommentTotal"`
	MultiLine        [][]string `json:"multiLine"`
	MultiLineTotal   int        `json:"multiLineTotal"`
	ComplexityChecks int        `json:"complexityChecks"`
	Keywords         int        `json:"keywords"`
}

// handleExplain serves the rule snapshot for the requested languages. The route
// sits on the same mux behind s.gate, so the token/Host/Origin defense stack of
// DEC-303 covers it like every other endpoint.
func (s *server) handleExplain(w http.ResponseWriter, r *http.Request) {
	names, ok := parseExplainNames(w, r)
	if !ok {
		return
	}
	if len(names) == 0 {
		names = explainSnapshotNames(s) // 缺省 = 快照内全部语言（无快照 → 空结果）
	}
	database := processor.LanguageDatabase()
	entries := make([]explainLanguage, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		lang, known := database[name]
		if !known {
			continue // 未知名略过（DEC-O06）
		}
		entries = append(entries, buildExplainLanguage(name, lang))
	}
	slices.SortFunc(entries, func(a, b explainLanguage) int {
		if c := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	writeJSON(w, http.StatusOK, entries)
}

// parseExplainNames reads the comma-separated names query parameter, trims and
// drops empty segments, and enforces the request-size cap with a 400.
func parseExplainNames(w http.ResponseWriter, r *http.Request) ([]string, bool) {
	raw := r.URL.Query().Get(explainNamesParam)
	if raw == "" {
		return nil, true
	}
	segments := strings.Split(raw, ",")
	names := make([]string, 0, len(segments))
	for _, segment := range segments {
		if name := strings.TrimSpace(segment); name != "" {
			names = append(names, name)
		}
	}
	if len(names) > explainMaxNames {
		writeError(w, http.StatusBadRequest, "names 数量超过上限 "+strconv.Itoa(explainMaxNames))
		return nil, false
	}
	return names, true
}

// explainSnapshotNames collects the language names of this instance's latest
// completed analysis snapshot, preserving the snapshot's own order (the empty
// result marks "no snapshot yet" honestly).
func explainSnapshotNames(s *server) []string {
	snapshot, ok := latestSnapshotFor(s)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(snapshot.Languages))
	for _, row := range snapshot.Languages {
		if row.Name != "" {
			names = append(names, row.Name)
		}
	}
	return names
}

// buildExplainLanguage projects one database entry into the wire shape with
// copied (never aliased) fields. Unknown, the synthetic category for
// unsupported files, is not a database key and therefore never reaches here.
func buildExplainLanguage(name string, lang processor.Language) explainLanguage {
	lineComment, lineCommentTotal := cutStrings(lang.LineComment)
	multiLine, multiLineTotal := cutPairs(lang.MultiLine)
	return explainLanguage{
		Name:             name,
		LineComment:      lineComment,
		LineCommentTotal: lineCommentTotal,
		MultiLine:        multiLine,
		MultiLineTotal:   multiLineTotal,
		ComplexityChecks: len(lang.ComplexityChecks),
		Keywords:         len(lang.Keywords),
	}
}

// cutStrings copies at most explainFieldCut entries and returns the untruncated
// total beside them, so a short list keeps total==len and an absent list stays
// JSON-encoded as [] instead of null.
func cutStrings(values []string) ([]string, int) {
	total := len(values)
	if total > explainFieldCut {
		values = values[:explainFieldCut]
	}
	return orEmptySlice(values), total
}

// cutPairs is cutStrings for the multi-line comment pairs, cloning each pair so
// no response row aliases the engine database.
func cutPairs(pairs [][]string) ([][]string, int) {
	total := len(pairs)
	if total > explainFieldCut {
		pairs = pairs[:explainFieldCut]
	}
	out := make([][]string, 0, len(pairs))
	for _, pair := range pairs {
		out = append(out, orEmptySlice(pair))
	}
	return out, total
}
