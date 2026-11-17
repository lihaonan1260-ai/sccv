package gui

// The analyze job worker (T-004): POST /api/analyze validates a directory,
// claims the single R2 job slot and runs processor.ProcessResult in a
// goroutine; GET /api/job/{id} polls the state machine until the immutable
// snapshot is published.
//
// Consumption contract (DEC-002): the only engine entry point used here is
// ProcessResult — Process is off limits because it calls os.Exit eleven times
// (processor.go:1058-1218). Every processor global the run depends on is
// reset from the DEC-005 default snapshot before each job, so the numbers a
// snapshot reports are byte-for-byte what `scc <dir>` would print for the
// same tree (RD-006-②) and nothing leaks into the next request (R2).

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/boyter/scc/v4/gui/logger"
	"github.com/boyter/scc/v4/processor"
)

// analyzeMu serializes analysis jobs (R2: the processor carries roughly
// ninety package-level globals plus the processFlags rewrite chain, so two
// concurrent runs are unrepresentable; a request arriving while the slot is
// held answers 409). It stays a package-level seam because the frozen
// API-layer test (TestAnalyzeBusyReturns409) holds this lock from outside
// while asserting the 409 path; the handler claims it with TryLock and the
// winning goroutine releases it when the job reaches a terminal state.
var analyzeMu sync.Mutex

// maxAnalysisBodyBytes caps analyze request bodies (§7: 1MB); a path never
// needs more, so anything larger is refused before it is read in full.
const maxAnalysisBodyBytes = 1 << 20

// uncUnsupportedMessage is the frozen DEC-027 rejection copy for UNC and
// device paths (TC-032 asserts substrings of it byte-exactly).
const uncUnsupportedMessage = "暂不支持 UNC/网络路径，请映射为盘符后重试"

// snapshotTypeAnalyze pins the snapshot discriminator of the §5 schema.
const snapshotTypeAnalyze = "analyze"

// jobPathPrefix is the route prefix handleJob strips to recover the job id.
const jobPathPrefix = "/api/job/"

// badRequest marks input the analyze endpoint refuses with 400; its message
// is served verbatim as the {"error":"..."} body so invalid input fails fast
// and loud in the product language.
type badRequest struct{ message string }

func (e *badRequest) Error() string { return e.message }

// analyzeRequest is the POST /api/analyze body (§5 row 6). Languages nil
// means "count every language"; a non-null slice must map through the engine
// language database (K3) or the request is rejected. ExcludeDirs carries
// root-relative slash path segments ("docs", "src/gen") for the wizard's
// unchecked subtrees (OPT-001); fileTypes/excludeFileTypes carry dot-less
// lowercase extensions for the dual-mode type filter (OPT-002).
type analyzeRequest struct {
	Path             string   `json:"path"`
	IncludeUnknown   bool     `json:"includeUnknown"`
	Languages        []string `json:"languages"`
	ExcludeDirs      []string `json:"excludeDirs"`
	FileTypes        []string `json:"fileTypes"`
	ExcludeFileTypes []string `json:"excludeFileTypes"`
}

// analysisOptions is the validated option set a job runs with. languages is
// echoed verbatim into the snapshot (nil stays null, §0.2-4). The remaining
// fields are resolved by handleAnalyze from the validated request: the wire
// only names candidate segments/extensions, while these carry the resolved
// walk plan (multi-root complement or deny-list fallback) and echo forms.
type analysisOptions struct {
	includeUnknown   bool
	languages        []string
	excludeDirs      []string // clean root-relative slash segments, deduped (echo)
	excludeAbs       []string // absolute normalized excluded dirs, sorted (echo)
	walkRoots        []string // cover mode: exact complement multi-root walk set
	denySuffixes     []string // fallback mode: root-relative slash suffixes
	fileTypes        []string // dot-less lowercase extension allow list (nil off)
	excludeFileTypes []string // dot-less lowercase extension exclude list (nil off)
}

func (r analyzeRequest) toOptions() analysisOptions {
	return analysisOptions{includeUnknown: r.IncludeUnknown, languages: r.Languages}
}

// jobState is the explicit state machine of the single analysis slot. A job
// is created in running and leaves it exactly once, to done or error; idle
// describes the slot before any job exists and never reaches the wire.
type jobState string

const (
	jobStateIdle    jobState = "idle"
	jobStateRunning jobState = "running"
	jobStateDone    jobState = "done"
	jobStateError   jobState = "error"
)

// jobAcceptPayload is the 202 body of a successful POST /api/analyze.
type jobAcceptPayload struct {
	JobID  string `json:"jobId"`
	Status string `json:"status"`
}

// jobStatusPayload is the GET /api/job/{id} body: running carries only the
// status, done adds the snapshot, error adds the failure reason.
type jobStatusPayload struct {
	Status string            `json:"status"`
	Job    *analysisSnapshot `json:"job,omitempty"`
	Error  string            `json:"error,omitempty"`
}

// analysisSnapshot is the §0.2-4 contract the SPA and the export endpoints
// consume. Rows are built once and never mutated after publication, so
// readers can hold the pointer without a lock (R5: no ULOC field exists —
// percentages are computed front-end, ULOC counting stays off).
type analysisSnapshot struct {
	JobID       string                 `json:"jobId"`
	Type        string                 `json:"type"`
	RootPath    string                 `json:"rootPath"`
	GeneratedAt string                 `json:"generatedAt"`
	DurationMs  int64                  `json:"durationMs"`
	Options     analysisOptionsPayload `json:"options"`
	Totals      analysisTotalsPayload  `json:"totals"`
	Languages   []analysisLanguageRow  `json:"languages"`
	Files       []analysisFileRow      `json:"files"`
}

// analysisOptionsPayload echoes the option set back on the snapshot (§0.2-4).
// The three OPT-001/002 slices stay null when the wizard did not use them
// (additive keys: absent request options keep the frozen key set intact).
type analysisOptionsPayload struct {
	IncludeUnknown   bool     `json:"includeUnknown"`
	Languages        []string `json:"languages"`
	ExcludeDirs      []string `json:"excludeDirs"`
	FileTypes        []string `json:"fileTypes"`
	ExcludeFileTypes []string `json:"excludeFileTypes"`
}

type analysisTotalsPayload struct {
	Files      int64 `json:"files"`
	Lines      int64 `json:"lines"`
	Code       int64 `json:"code"`
	Comment    int64 `json:"comment"`
	Blank      int64 `json:"blank"`
	Complexity int64 `json:"complexity"`
	Bytes      int64 `json:"bytes"`
}

type analysisLanguageRow struct {
	Name       string `json:"name"`
	Files      int64  `json:"files"`
	Lines      int64  `json:"lines"`
	Code       int64  `json:"code"`
	Comment    int64  `json:"comment"`
	Blank      int64  `json:"blank"`
	Complexity int64  `json:"complexity"`
	Bytes      int64  `json:"bytes"`
}

type analysisFileRow struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	Language   string `json:"language"`
	Lines      int64  `json:"lines"`
	Code       int64  `json:"code"`
	Comment    int64  `json:"comment"`
	Blank      int64  `json:"blank"`
	Complexity int64  `json:"complexity"`
	Bytes      int64  `json:"bytes"`
}

// analysisJob is one entry of the state machine. Its mutable fields are
// written exactly once by the owning goroutine under jobRegistryMu and read
// under the same lock; the published snapshot pointer is immutable.
type analysisJob struct {
	id       string
	owner    *server // instance that accepted the job; keys the snapshot store
	state    jobState
	snapshot *analysisSnapshot
	errText  string
}

// The job slot state is process-global on purpose: analyzeMu (the R2 slot, a
// frozen-test seam) and the processor globals it guards are package-level,
// so per-instance slot state would understate what can actually run at once.
// The finished-snapshot store is the deliberate exception (DI-002, TC-064):
// it is keyed by owning instance, so a freshly started server reports "no
// snapshot" even when an earlier instance in the same process completed an
// analysis — instance isolation is the contract. Entries outlive their
// instance (a shut-down server leaves its snapshot behind): harmless by
// magnitude (a handful of instances, one snapshot each) and unreachable
// afterwards, because the only lookup key was that instance.
var (
	jobRegistryMu  sync.Mutex
	currentJob     *analysisJob                      // single R2 slot: the most recently accepted job
	latestByServer = map[*server]*analysisSnapshot{} // per-instance last completed snapshot (DEC-012)
	jobCounter     atomic.Int64                      // monotonic job id source (process-local, token-gated)
)

// flagDefaults freezes the DEC-005 flag snapshot once per process. It is
// read at Start so the first job cannot observe globals a previous in-process
// run has left behind.
var flagDefaults = sync.OnceValue(snapshotProcessorDefaults)

// jobSlotBusy reports whether the single R2 slot is currently occupied — an
// analysis or report job that has not reached a terminal state (BG-004
// read-only seam for the page lifecycle: the auto exit must never fire while
// work is in flight). It never blocks and never mutates slot or lock state,
// so it cannot collide with the TryLock claim path the handlers use; reading
// the registries under their lock is the whole cost.
func jobSlotBusy() bool {
	jobRegistryMu.Lock()
	defer jobRegistryMu.Unlock()
	if job := currentJob; job != nil && job.state == jobStateRunning {
		return true
	}
	if job := currentReportJob; job != nil && job.state == jobStateRunning {
		return true
	}
	return false
}

// processorDefaultsSnapshot carries the CLI-effective defaults the per-job
// reset needs. Only entries whose CLI default differs from a bare false/nil
// zero value are kept; everything else is reset to its zero value directly.
type processorDefaultsSnapshot struct {
	sortBy                          string
	pathDenyList                    []string
	excludeFilename                 []string
	generatedMarkers                []string
	minifiedGeneratedLineByteLength int
	largeLineCount                  int64
	largeByteCount                  int64
}

// snapshotProcessorDefaults distills the cobra-registered defaults of
// config.go:288-491. Three slice flags register empty and merge their real
// default back post-parse (config.go:525-527), so the package globals alone
// would under-report them; the literal values of config.go:43-45 are pinned
// here. Numeric defaults are read from the pristine globals, which match the
// registered flag defaults exactly.
func snapshotProcessorDefaults() processorDefaultsSnapshot {
	return processorDefaultsSnapshot{
		sortBy:                          "files",
		pathDenyList:                    []string{".git", ".hg", ".svn"},
		excludeFilename:                 []string{"package-lock.json", "Cargo.lock", "yarn.lock", "pubspec.lock", "Podfile.lock", "pnpm-lock.yaml"},
		generatedMarkers:                []string{"do not edit", "<auto-generated />"},
		minifiedGeneratedLineByteLength: processor.MinifiedGeneratedLineByteLength,
		largeLineCount:                  processor.LargeLineCount,
		largeByteCount:                  processor.LargeByteCount,
	}
}

// handleAnalyze validates the request, claims the single job slot and answers
// 202 with the job id; the analysis itself runs on its own goroutine. Any
// request arriving while the slot is held — analyze or report — gets 409
// busy (DEC-206), which is why the TryLock happens only after validation.
func (s *server) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeAnalyzeRequest(w, r)
	if !ok {
		return
	}
	absPath, err := normalizeAnalysisPath(req.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// OPT-002: the two allow channels both end up writing AllowListExtensions,
	// so letting them through together would silently stack two filters —
	// refuse the combination instead (DEC-O03).
	if len(req.FileTypes) != 0 && len(req.Languages) != 0 {
		writeError(w, http.StatusBadRequest, "fileTypes 与 languages 不可同用")
		return
	}
	extensions, err := resolveLanguageAllowList(req.Languages)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	opts := req.toOptions()
	fileTypes, err := normalizeExtensionList(req.FileTypes)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	excludeFileTypes, err := normalizeExtensionList(req.ExcludeFileTypes)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	opts.fileTypes = fileTypes
	opts.excludeFileTypes = excludeFileTypes
	if len(req.ExcludeDirs) != 0 {
		// OPT-001: translate the unchecked wizard subtrees into the walk plan
		// before the slot is claimed, so a bad exclusion never occupies R2.
		plan, err := resolveExclusions(absPath, req.ExcludeDirs, osDirLister)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		opts.excludeDirs = plan.segments
		opts.excludeAbs = plan.absPaths
		opts.walkRoots = plan.walkRoots
		opts.denySuffixes = plan.denySuffixes
	}
	if !analyzeMu.TryLock() {
		writeError(w, http.StatusConflict, "busy")
		return
	}
	job := startAnalysisJob(s, absPath, opts, extensions)
	writeJSON(w, http.StatusAccepted, jobAcceptPayload{JobID: job.id, Status: string(jobStateRunning)})
}

// decodeJSONBody reads one JSON body under the shared 1MB cap (§7) with the
// shared refusal copy; every POST endpoint decodes through it so the
// transport-level contract stays identical across the API surface.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxAnalysisBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "请求体超过 1MB 上限")
			return false
		}
		writeError(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return false
	}
	return true
}

// decodeAnalyzeRequest reads the JSON body under the 1MB cap (§7). The path
// policy lives in normalizeAnalysisPath; only transport-level validity is
// decided here.
func decodeAnalyzeRequest(w http.ResponseWriter, r *http.Request) (analyzeRequest, bool) {
	var req analyzeRequest
	if !decodeJSONBody(w, r, &req) {
		return req, false
	}
	return req, true
}

// normalizeAnalysisPath applies the DEC-027 policy: Abs (which implies Clean)
// resolves ".." segments and mixed separators before anything else looks at
// the path, UNC and device forms are refused outright (DEC-305 — a hung
// network path would occupy the R2 slot forever), and only an existing
// directory survives.
func normalizeAnalysisPath(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", &badRequest{"path 不能为空"}
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", &badRequest{"路径无法解析: " + raw}
	}
	// Clean folds forward slashes into a \\-prefixed UNC on Windows, so the
	// normalized form already covers //server/share there. The raw input is
	// checked too, keeping the policy platform-independent: a leading double
	// separator is network/device syntax (\\.\, \\?\, \\server, //server)
	// wherever this GUI runs.
	if strings.HasPrefix(abs, `\\`) || strings.HasPrefix(raw, `\\`) || strings.HasPrefix(raw, "//") {
		return "", &badRequest{uncUnsupportedMessage}
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", &badRequest{"路径不存在或不可访问: " + abs}
	}
	if !info.IsDir() {
		return "", &badRequest{"路径不是目录: " + abs}
	}
	return abs, nil
}

// resolveLanguageAllowList maps requested engine language names to the
// extension allow list the processor consumes (K3: via LanguageDatabase, the
// same dot-less extensions the CLI's --include-ext matches against). Names
// the database does not know are rejected as a whole — a silent partial
// filter would answer a question nobody asked.
func resolveLanguageAllowList(languages []string) ([]string, error) {
	if len(languages) == 0 {
		return nil, nil // null (or empty) means no filter
	}
	database := processor.LanguageDatabase()
	extensions := make([]string, 0, len(languages))
	var unknown []string
	for _, name := range languages {
		lang, ok := database[name]
		if !ok {
			unknown = append(unknown, name)
			continue
		}
		extensions = append(extensions, lang.Extensions...)
	}
	if len(unknown) != 0 {
		return nil, &badRequest{"不支持的语言: " + strings.Join(unknown, ", ")}
	}
	if len(extensions) == 0 {
		return nil, nil // resolvable names that carry no extensions: filter degenerates to off
	}
	return extensions, nil
}

// normalizeExtensionList canonicalizes an OPT-002 extension filter: entries
// arrive dot-less lowercase from the wizard, but trimming, lowercasing and
// leading-dot stripping are applied defensively so ".go" and "Go" mean the
// same thing the CLI's --include-ext would. Empty or separator-bearing
// entries are refused — an extension never carries a path separator.
func normalizeExtensionList(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		e := strings.ToLower(strings.TrimSpace(v))
		e = strings.TrimLeft(e, ".")
		if e == "" {
			return nil, &badRequest{"扩展名不能为空"}
		}
		if strings.ContainsAny(e, `/\`) || strings.ContainsAny(e, " \t") {
			return nil, &badRequest{"非法扩展名: " + v}
		}
		out = append(out, e)
	}
	return out, nil
}

// dirLister returns the names of a directory's direct children (directories
// and files alike). It is injected into expandRootCover so the complement
// translation stays a pure, filesystem-free function the tests can drive
// from an in-memory tree.
type dirLister func(dir string) ([]string, error)

// osDirLister is the production lister: one ReadDir per split directory,
// names only, no file content is ever read.
func osDirLister(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names, nil
}

// maxCoverRoots bounds the exact complement cover; a translation that would
// need more walk roots degrades to the deny-list fallback (DEC-O01).
const maxCoverRoots = 64

// walkModeCover / walkModeDenyList name the two translation modes; cover is
// the exact multi-root walk, deny-list is the >64-root suffix degradation.
const (
	walkModeCover    = "cover"
	walkModeDenyList = "denylist"
)

// errExclusionCoversRoot marks an exclusion set that leaves nothing to walk:
// the copy is served verbatim as the 400 body (交接协议 Notes-8 定稿文案).
var errExclusionCoversRoot = &badRequest{"所选排除项覆盖了整个目录"}

// errCoverTooLarge is the internal abort signal of the cover walk; it never
// leaves expandRootCover (callers see the fallback mode instead).
var errCoverTooLarge = errors.New("gui: complement cover exceeds root budget")

// exclusionPlan is the resolved OPT-001 walk plan: the echo forms plus the
// two mutually exclusive walk scopes cover and fallback produce.
type exclusionPlan struct {
	segments     []string // clean root-relative slash segments, request order
	absPaths     []string // absolute normalized excluded dirs, sorted (echo)
	walkRoots    []string // cover mode: exact complement walk set
	denySuffixes []string // fallback mode: root-relative slash suffixes (nil in cover mode)
}

// resolveExclusions validates excludeDirs segments against the root and
// translates them into the walk plan the processor consumes. Segments are
// root-relative slash paths; absolute forms, ".." escapes, missing paths and
// non-directories are refused (fail fast before the R2 slot is claimed).
func resolveExclusions(root string, segments []string, list dirLister) (exclusionPlan, error) {
	plan := exclusionPlan{}
	seen := make(map[string]bool, len(segments))
	absolute := make([]string, 0, len(segments))
	for _, raw := range segments {
		seg, err := cleanExcludeSegment(raw)
		if err != nil {
			return plan, err
		}
		full := filepath.Join(root, filepath.FromSlash(seg))
		rel, relErr := filepath.Rel(root, full)
		if relErr != nil || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return plan, &badRequest{"排除路径越出分析目录范围: " + raw}
		}
		info, statErr := os.Stat(full)
		if statErr != nil {
			return plan, &badRequest{"排除路径不存在或不可访问: " + full}
		}
		if !info.IsDir() {
			return plan, &badRequest{"排除路径不是目录: " + full}
		}
		if !seen[seg] {
			seen[seg] = true
			plan.segments = append(plan.segments, seg)
			absolute = append(absolute, full)
		}
	}
	plan.absPaths = slices.Clone(absolute)
	slices.Sort(plan.absPaths)

	roots, mode, err := expandRootCover(root, absolute, list)
	if err != nil {
		return plan, err
	}
	if mode == walkModeCover {
		plan.walkRoots = roots
		return plan, nil
	}
	// Fallback (>64 roots): single-root walk with a suffix deny list merged
	// into the CLI defaults by applyAnalysisFlags; the walker matches path
	// suffixes, so a same-named directory elsewhere is excluded too — the UI
	// states this caveat verbatim (DEC-O01).
	suffixes := make([]string, 0, len(absolute))
	for _, p := range minimizeExclusions(absolute) {
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return plan, relErr
		}
		suffixes = append(suffixes, filepath.ToSlash(rel))
	}
	slices.Sort(suffixes)
	plan.denySuffixes = suffixes
	return plan, nil
}

// cleanExcludeSegment normalizes one wire segment: trimmed, forward slashes,
// path.Clean folded; anything that cleans to the root itself or keeps a ".."
// escape is refused outright.
func cleanExcludeSegment(raw string) (string, error) {
	seg := strings.TrimSpace(raw)
	if seg == "" {
		return "", &badRequest{"排除路径不能为空"}
	}
	seg = path.Clean(filepath.ToSlash(seg))
	if seg == "." || seg == ".." || strings.HasPrefix(seg, "../") || path.IsAbs(seg) {
		return "", &badRequest{"非法排除路径（须为分析目录下的相对路径）: " + raw}
	}
	return seg, nil
}

// expandRootCover translates excluded absolute directories under root into
// the minimal multi-root walk set covering everything except the excluded
// subtrees (DEC-O01). Up to maxCoverRoots roots the answer is the exact
// complement (mode "cover"); beyond that it degrades to the single-root
// walk (mode "denylist") whose suffix deny list the caller merges. An
// exclusion set that leaves nothing to walk answers errExclusionCoversRoot.
func expandRootCover(root string, excluded []string, list dirLister) ([]string, string, error) {
	if len(excluded) == 0 {
		return []string{root}, walkModeCover, nil
	}
	top := minimizeExclusions(excluded)
	excludedSet := make(map[string]bool, len(top))
	for _, p := range top {
		excludedSet[p] = true
	}
	if excludedSet[root] {
		return nil, "", errExclusionCoversRoot
	}
	builder := &coverBuilder{list: list, excluded: excludedSet}
	roots, err := builder.cover(root)
	if err != nil {
		if errors.Is(err, errCoverTooLarge) {
			return []string{root}, walkModeDenyList, nil
		}
		return nil, "", err
	}
	if len(roots) == 0 {
		return nil, "", errExclusionCoversRoot
	}
	return roots, walkModeCover, nil
}

// coverBuilder accumulates the complement walk roots. Directories with an
// exclusion beneath are split (recursed into), clean subtrees are taken
// whole, and the direct files of every split directory join the root set —
// a split directory is never itself a walk root, so its own files would
// otherwise drop out of the count.
type coverBuilder struct {
	list     dirLister
	excluded map[string]bool
	count    int
}

func (c *coverBuilder) cover(dir string) ([]string, error) {
	names, err := c.list(dir)
	if err != nil {
		return nil, err
	}
	roots := make([]string, 0, len(names))
	for _, name := range names {
		child := filepath.Join(dir, name)
		if c.excluded[child] {
			continue
		}
		if c.hasExclusionBelow(child) {
			sub, err := c.cover(child)
			if err != nil {
				return nil, err
			}
			roots = append(roots, sub...)
		} else {
			roots = append(roots, child)
		}
		c.count++
		if c.count > maxCoverRoots {
			return nil, errCoverTooLarge
		}
	}
	return roots, nil
}

// hasExclusionBelow reports whether any excluded path lies strictly beneath
// dir (the excluded set is an antichain after minimization).
func (c *coverBuilder) hasExclusionBelow(dir string) bool {
	sep := string(filepath.Separator)
	for e := range c.excluded {
		if strings.HasPrefix(e, dir+sep) {
			return true
		}
	}
	return false
}

// minimizeExclusions drops any excluded path that lies beneath another one,
// collapsing the set into the antichain of topmost exclusions.
func minimizeExclusions(excluded []string) []string {
	kept := make([]string, 0, len(excluded))
	for _, p := range excluded {
		nested := false
		for _, q := range excluded {
			if q != p && isDirPrefix(q, p) {
				nested = true
				break
			}
		}
		if !nested {
			kept = append(kept, p)
		}
	}
	return kept
}

// isDirPrefix reports whether prefix is path itself or one of its ancestor
// directories (separator-aware, so "a/b" does not prefix-match "a/bb").
func isDirPrefix(prefix, path string) bool {
	return prefix == path || strings.HasPrefix(path, prefix+string(filepath.Separator))
}

// startAnalysisJob registers the job in the running state and hands it to a
// goroutine. Ownership of analyzeMu transfers with it: the slot stays closed
// from the 202 until the job reaches a terminal state (R2). The owning
// instance travels with the job so the finished snapshot lands in that
// instance's store (DI-002), never in another's.
func startAnalysisJob(owner *server, absPath string, opts analysisOptions, extensions []string) *analysisJob {
	job := &analysisJob{
		id:    strconv.FormatInt(jobCounter.Add(1), 10),
		owner: owner,
		state: jobStateRunning,
	}
	jobRegistryMu.Lock()
	currentJob = job
	jobRegistryMu.Unlock()

	// OPT-006 hook: analyze_start — path + request options only (no file
	// content); nil-safe appLog keeps direct-server tests untouched. The
	// three filter channels are logged as counts (OPT-001/002 echo fields
	// stay in the snapshot, the log carries magnitudes only).
	appLog.Event(logger.EventAnalyzeStart, map[string]any{
		"jobId":            job.id,
		"path":             absPath,
		"includeUnknown":   opts.includeUnknown,
		"languages":        opts.languages,
		"excludeDirs":      len(opts.excludeDirs),
		"fileTypes":        len(opts.fileTypes),
		"excludeFileTypes": len(opts.excludeFileTypes),
	})

	go func() {
		defer analyzeMu.Unlock()
		job.run(absPath, opts, extensions)
	}()
	return job
}

// run applies the DEC-005 flag reset, drives ProcessResult and moves the job
// to its terminal state exactly once. Called on the owning goroutine only.
func (j *analysisJob) run(absPath string, opts analysisOptions, extensions []string) {
	defaults := flagDefaults()
	started := time.Now()
	applyAnalysisFlags(absPath, opts, extensions, defaults)
	summaries, err := processor.ProcessResult()
	duration := time.Since(started)

	if err != nil {
		j.finish(jobStateError, nil, err.Error())
		// OPT-006 hook: analyze_error carries the engine failure text only.
		appLog.Event(logger.EventAnalyzeError, map[string]any{
			"jobId": j.id,
			"path":  absPath,
			"err":   err.Error(),
		})
		return
	}
	snapshot := buildAnalysisSnapshot(j.id, absPath, opts, summaries, duration)
	j.finish(jobStateDone, snapshot, "")
	// OPT-006 hook: analyze_done carries the totals seven-tuple summary.
	appLog.Event(logger.EventAnalyzeDone, map[string]any{
		"jobId":      j.id,
		"path":       absPath,
		"durationMs": duration.Milliseconds(),
		"totals": map[string]any{
			"files":      snapshot.Totals.Files,
			"lines":      snapshot.Totals.Lines,
			"code":       snapshot.Totals.Code,
			"comment":    snapshot.Totals.Comment,
			"blank":      snapshot.Totals.Blank,
			"complexity": snapshot.Totals.Complexity,
			"bytes":      snapshot.Totals.Bytes,
		},
	})
}

// finish publishes the terminal transition under the registry lock. The done
// snapshot is simultaneously the owning instance's latest-completed copy,
// which is what its export endpoints serve (DEC-012).
func (j *analysisJob) finish(state jobState, snapshot *analysisSnapshot, errText string) {
	jobRegistryMu.Lock()
	defer jobRegistryMu.Unlock()
	j.state = state
	j.snapshot = snapshot
	j.errText = errText
	if state == jobStateDone {
		latestByServer[j.owner] = snapshot
	}
}

// applyAnalysisFlags resets every processor global this pipeline reads before
// a run, from the frozen defaults plus the request options (DEC-005). The
// logging channels are re-pinned false so no job can ever speak on stdout
// beside the single SCCV_READY line (R3). Pure output-format globals (Format,
// Cocomo, SizeUnit, …) are out of scope: ProcessResult formats nothing.
// Runs hold analyzeMu, and health/config touch none of these, so no other
// code path can observe the reset window.
func applyAnalysisFlags(absPath string, opts analysisOptions, extensions []string, defaults processorDefaultsSnapshot) {
	processor.Verbose = false
	processor.Debug = false
	processor.Trace = false

	// Walk scope: the validated directory plus the CLI's exclusion defaults.
	// OPT-001 narrows it two ways: the exact multi-root complement (cover
	// mode) or — beyond maxCoverRoots — the single-root walk with the
	// wizard's exclusions merged into the suffix deny list (fallback mode).
	// Without exclusions the original single-root scope is byte-identical.
	switch {
	case len(opts.walkRoots) != 0:
		processor.DirFilePaths = opts.walkRoots
		processor.PathDenyList = slices.Clone(defaults.pathDenyList)
	case len(opts.denySuffixes) != 0:
		processor.DirFilePaths = []string{absPath}
		processor.PathDenyList = append(slices.Clone(defaults.pathDenyList), opts.denySuffixes...)
	default:
		processor.DirFilePaths = []string{absPath}
		processor.PathDenyList = slices.Clone(defaults.pathDenyList)
	}
	processor.ExcludeFilename = slices.Clone(defaults.excludeFilename)
	processor.Exclude = nil
	// OPT-002 dual-mode type filter: allow first, then exclude (the engine
	// gate checks them in that order, so an overlap is excluded — the UI
	// states this verbatim). Empty lists keep the filters off, exactly as
	// the frozen reset did.
	processor.ExcludeListExtensions = slices.Clone(opts.excludeFileTypes)
	processor.AllowListExtensions = extensions // K3 filter, off when nil
	if len(opts.fileTypes) != 0 {
		processor.AllowListExtensions = opts.fileTypes // mutually exclusive with languages (400 upstream)
	}
	processor.IgnoreFiles = nil
	processor.Ignore = false
	processor.GitIgnore = false
	processor.GitModuleIgnore = false
	processor.SccIgnore = false
	processor.CountIgnore = false
	processor.IncludeSymLinks = false
	processor.NoLarge = false
	processor.LargeLineCount = defaults.largeLineCount
	processor.LargeByteCount = defaults.largeByteCount

	// Detection and remapping: the GUI exposes no count-as/remap surface.
	processor.CountAs = ""
	processor.CountAsPattern = nil
	processor.CountRules = nil
	processor.RemapAll = ""
	processor.RemapUnknown = ""

	// Metrics: complexity counting is on (the CLI flag is a disable switch),
	// everything that adds columns the snapshot does not carry stays off —
	// ULOC and percentages explicitly so (R5).
	processor.Files = true // the snapshot carries per-file rows (§5)
	processor.Percent = false
	processor.UlocMode = false
	processor.Complexity = false
	processor.Cognitive = false
	processor.Duplicates = false
	processor.MaxMean = false
	processor.Dryness = false
	processor.DisableCheckBinary = false
	processor.SpecialisedCounters = false

	// Unknown extensions are only counted when asked for (DEC-016, mirrors
	// the CLI's --count-unsupported).
	processor.CountUnsupported = opts.includeUnknown

	// Minified/generated detection: off by default, markers pinned to the CLI
	// defaults so a later enabling cannot inherit stale markers.
	processor.Minified = false
	processor.Generated = false
	processor.MinifiedGenerated = false
	processor.IgnoreMinified = false
	processor.IgnoreGenerated = false
	processor.IgnoreMinifiedGenerate = false
	processor.GeneratedMarkers = slices.Clone(defaults.generatedMarkers)
	processor.MinifiedGeneratedLineByteLength = defaults.minifiedGeneratedLineByteLength

	// Feed-forward switches processFlags reads (processor.go:932-1000); all
	// off so the reset above survives the flag rewriting chain untouched.
	processor.More = false
	processor.CostComparison = false
	processor.Locomo = false

	// CLI default sort; the snapshot assembly re-orders rows for the schema.
	processor.SortBy = defaults.sortBy
}

// buildAnalysisSnapshot folds the engine summaries into the immutable §0.2-4
// shape: totals aggregated over languages, languages ordered by lines
// descending (name ascending on ties), files flattened and ordered by path.
func buildAnalysisSnapshot(jobID, rootPath string, opts analysisOptions, summaries []processor.LanguageSummary, duration time.Duration) *analysisSnapshot {
	snapshot := &analysisSnapshot{
		JobID:       jobID,
		Type:        snapshotTypeAnalyze,
		RootPath:    rootPath,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		DurationMs:  duration.Milliseconds(),
		Options: analysisOptionsPayload{
			IncludeUnknown:   opts.includeUnknown,
			Languages:        opts.languages, // echo verbatim; nil stays null
			ExcludeDirs:      opts.excludeAbs,
			FileTypes:        opts.fileTypes,
			ExcludeFileTypes: opts.excludeFileTypes,
		},
		Languages: make([]analysisLanguageRow, 0, len(summaries)),
		Files:     make([]analysisFileRow, 0),
	}

	for i := range summaries {
		summary := &summaries[i]
		snapshot.Totals.Files += summary.Count
		snapshot.Totals.Lines += summary.Lines
		snapshot.Totals.Code += summary.Code
		snapshot.Totals.Comment += summary.Comment
		snapshot.Totals.Blank += summary.Blank
		snapshot.Totals.Complexity += summary.Complexity
		snapshot.Totals.Bytes += summary.Bytes

		snapshot.Languages = append(snapshot.Languages, analysisLanguageRow{
			Name:       summary.Name,
			Files:      summary.Count,
			Lines:      summary.Lines,
			Code:       summary.Code,
			Comment:    summary.Comment,
			Blank:      summary.Blank,
			Complexity: summary.Complexity,
			Bytes:      summary.Bytes,
		})

		for _, file := range summary.Files {
			snapshot.Files = append(snapshot.Files, analysisFileRow{
				Path:       file.Location,
				Name:       file.Filename,
				Language:   file.Language,
				Lines:      file.Lines,
				Code:       file.Code,
				Comment:    file.Comment,
				Blank:      file.Blank,
				Complexity: file.Complexity,
				Bytes:      file.Bytes,
			})
		}
	}

	slices.SortFunc(snapshot.Languages, func(a, b analysisLanguageRow) int {
		if order := cmp.Compare(b.Lines, a.Lines); order != 0 {
			return order
		}
		return strings.Compare(a.Name, b.Name)
	})
	slices.SortFunc(snapshot.Files, func(a, b analysisFileRow) int {
		return strings.Compare(a.Path, b.Path)
	})
	return snapshot
}

// handleJob answers the poll for a job id. Only the current slot's id is
// known; anything else — unknown, empty, already superseded — is 404, which
// is the contract's way of saying "no such job" (§5 row 7).
func (s *server) handleJob(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, jobPathPrefix)
	// T-033 seam: report jobs answer on this shared status endpoint too; ids
	// come from one monotonic counter, so the two registries never overlap.
	if payload, ok := reportJobStatus(id); ok {
		writeJSON(w, http.StatusOK, payload)
		return
	}
	job, state, snapshot, errText := lookupJob(id)
	if job == nil {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	switch state {
	case jobStateDone:
		writeJSON(w, http.StatusOK, jobStatusPayload{Status: string(jobStateDone), Job: snapshot})
	case jobStateError:
		writeJSON(w, http.StatusOK, jobStatusPayload{Status: string(jobStateError), Error: errText})
	default:
		writeJSON(w, http.StatusOK, jobStatusPayload{Status: string(jobStateRunning)})
	}
}

// lookupJob reads the slot under the registry lock and returns the terminal
// payload pieces along with it, so callers never touch job fields directly.
func lookupJob(id string) (*analysisJob, jobState, *analysisSnapshot, string) {
	if id == "" {
		return nil, jobStateIdle, nil, ""
	}
	jobRegistryMu.Lock()
	defer jobRegistryMu.Unlock()
	job := currentJob
	if job == nil || job.id != id {
		return nil, jobStateIdle, nil, ""
	}
	return job, job.state, job.snapshot, job.errText
}

// latestSnapshotFor exposes the most recent successful snapshot accepted by
// this server instance, for its export endpoints (DEC-012: exports serve the
// latest completed analysis, unfiltered; DI-002/TC-064: per instance, so an
// instance that never analyzed has none). The snapshot is immutable once
// published.
func latestSnapshotFor(s *server) (*analysisSnapshot, bool) {
	jobRegistryMu.Lock()
	defer jobRegistryMu.Unlock()
	snapshot := latestByServer[s]
	return snapshot, snapshot != nil
}

// scrubWalkDedupState clears the engine's cross-run walk dedup state without
// touching processor code (K1/BG-002). The dedup map — visitedPaths, declared
// processor/file.go:17, written by newFileJobGuessed via LoadOrStore at
// file.go:203 — is only emptied by cleanVisitedPaths (file.go:47-48), which
// runs solely at the top of ProcessResult (result.go:21) and Process
// (processor.go:1037). CollectReportData never clears it, so any earlier walk
// of the same tree in this process — an analysis or a previous report — makes
// the report walk skip every file as already processed and the report come
// out all-zero.
//
// The isolation trick: ProcessResult against an empty directory. Its entry
// cleans the dedup state first and the empty walk repopulates nothing, so
// afterwards the state is pristine. Only whitelisted surfaces are used; the
// transient DirFilePaths swap is save/restored, and the run is serialized by
// analyzeMu like every other statistics entry.
func scrubWalkDedupState() error {
	scrubDir, err := os.MkdirTemp("", "sccv-scrub-")
	if err != nil {
		// Without the scrub the report would silently come out all-zero, so
		// a failing scrub aborts the job loudly instead.
		return fmt.Errorf("gui: create walk scrub dir: %w", err)
	}
	defer os.RemoveAll(scrubDir)

	saved := processor.DirFilePaths
	processor.DirFilePaths = []string{scrubDir}
	defer func() { processor.DirFilePaths = saved }()

	if _, err := processor.ProcessResult(); err != nil {
		return fmt.Errorf("gui: walk scrub over %s: %w", scrubDir, err)
	}
	return nil
}
