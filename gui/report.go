package gui

// The report job worker (T-033): POST /api/report runs
// processor.CollectReportData in a goroutine behind the same R2 slot the
// analyzer uses, and GET /api/download/{jobId} streams the finished report
// straight out of processor.RenderReportTo — fully in-memory, zero temp files
// (DEC-025/DEC-306).
//
// Consumption contract (DEC-002): the engine entry points used here are
// CollectReportData (report.go:309) and RenderReportTo (report_render.go:161,
// the additive T-024 wrapper). Process is off limits (it calls os.Exit eleven
// times). Every processor global the report chain reads is set explicitly
// before each job (DEC-005), so no run depends on state a previous job left
// behind (R2).

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/boyter/scc/v4/gui/logger"
	"github.com/boyter/scc/v4/processor"
)

// downloadPathPrefix is the route prefix handleDownload strips to recover the
// report job id.
const downloadPathPrefix = "/api/download/"

// reportAttachmentHeader is the exact Content-Disposition value the frozen
// download contract asserts (TC-056).
const reportAttachmentHeader = "attachment; filename=sccv-report.html"

// reportRequest is the POST /api/report body (§5 row 9). includeHistory
// defaults to false: the git-history observers are skipped unless asked for
// (DEC-013); a tree without git degrades naturally either way
// (report.go:332/:337/:361).
type reportRequest struct {
	Path           string   `json:"path"`
	Languages      []string `json:"languages"`
	IncludeUnknown bool     `json:"includeUnknown"`
	IncludeHistory bool     `json:"includeHistory"`
}

// reportOptions is the validated option set a report job runs with. The same
// values are echoed verbatim into the done payload; path carries the
// normalized form so the echo matches what was actually analyzed.
type reportOptions struct {
	path           string
	languages      []string
	includeUnknown bool
	includeHistory bool
}

func (r reportRequest) toOptions(absPath string) reportOptions {
	return reportOptions{
		path:           absPath,
		languages:      r.Languages,
		includeUnknown: r.IncludeUnknown,
		includeHistory: r.IncludeHistory,
	}
}

// reportOptionsPayload is the echoed option block of a report job.
type reportOptionsPayload struct {
	Path           string   `json:"path"`
	Languages      []string `json:"languages"`
	IncludeUnknown bool     `json:"includeUnknown"`
	IncludeHistory bool     `json:"includeHistory"`
}

func (o reportOptions) toPayload() reportOptionsPayload {
	return reportOptionsPayload{
		Path:           o.path,
		Languages:      o.languages, // echo verbatim; nil stays null
		IncludeUnknown: o.includeUnknown,
		IncludeHistory: o.includeHistory,
	}
}

// reportJobStatusPayload is the GET /api/job/{id} body for report jobs: the
// same envelope as the analyze status, but a done job carries the download
// link plus the echoed options (§5 row 9) instead of a snapshot.
type reportJobStatusPayload struct {
	Status string            `json:"status"`
	Job    *reportJobPayload `json:"job,omitempty"`
	Error  string            `json:"error,omitempty"`
}

// reportJobPayload is the done-state body of a report job: the relative
// download URL and the options the report was rendered with.
type reportJobPayload struct {
	DownloadURL string               `json:"downloadUrl"`
	Options     reportOptionsPayload `json:"options"`
}

// reportJob is the report side of the single R2 slot. Its mutable fields are
// written exactly once by the owning goroutine under jobRegistryMu and read
// under the same lock; the published ReportData is never mutated afterwards.
type reportJob struct {
	id      string
	state   jobState
	options reportOptionsPayload
	data    *processor.ReportData
	errText string
}

// currentReportJob is the only report job the process keeps: superseded ids
// answer 404, exactly like the analyze slot. Retaining one ReportData bounds
// memory and keeps the server side of report history at zero persistence.
var currentReportJob *reportJob

// startReportJob registers the job in the running state and hands it to a
// goroutine. Ownership of analyzeMu transfers with it: the slot stays closed
// from the 202 until the job reaches a terminal state (R2). Ids come from the
// shared monotonic jobCounter, so analyze and report ids can never collide.
func startReportJob(absPath string, opts reportOptions, extensions []string) *reportJob {
	job := &reportJob{
		id:      strconv.FormatInt(jobCounter.Add(1), 10),
		state:   jobStateRunning,
		options: opts.toPayload(),
	}
	jobRegistryMu.Lock()
	currentReportJob = job
	jobRegistryMu.Unlock()

	go func() {
		defer analyzeMu.Unlock()
		job.run(absPath, opts, extensions)
	}()
	return job
}

// run applies the DEC-005 flag set, drives CollectReportData and moves the job
// to its terminal state exactly once. Called on the owning goroutine only.
func (j *reportJob) run(absPath string, opts reportOptions, extensions []string) {
	// BG-002: CollectReportData re-walks the tree but, unlike ProcessResult,
	// never clears the engine's cross-run walk dedup state (visitedPaths,
	// cleaned only at result.go:21/processor.go:1037). Without the scrub, any
	// earlier walk of the same tree in this process — an analysis or a
	// previous report — makes this walk skip every file and the report come
	// out all-zero. Gui-side isolation only (K1); see scrubWalkDedupState.
	if err := scrubWalkDedupState(); err != nil {
		j.finish(jobStateError, nil, err.Error())
		// OPT-006 hook: report_error (scrub failure text only).
		appLog.Event(logger.EventReportError, map[string]any{
			"jobId": j.id,
			"path":  absPath,
			"err":   err.Error(),
		})
		return
	}
	applyReportFlags(opts, extensions)
	data, err := processor.CollectReportData(absPath)
	if err != nil {
		j.finish(jobStateError, nil, err.Error())
		// OPT-006 hook: report_error (engine failure text only).
		appLog.Event(logger.EventReportError, map[string]any{
			"jobId": j.id,
			"path":  absPath,
			"err":   err.Error(),
		})
		return
	}
	j.finish(jobStateDone, &data, "")
	// OPT-006 hook: report_done — path + options + rollup summary.
	appLog.Event(logger.EventReportDone, map[string]any{
		"jobId":      j.id,
		"path":       absPath,
		"languages":  opts.languages,
		"durationMs": data.Duration.Milliseconds(),
		"totals": map[string]any{
			"files":      data.Totals.Files,
			"lines":      data.Totals.Lines,
			"code":       data.Totals.Code,
			"comment":    data.Totals.Comment,
			"blank":      data.Totals.Blank,
			"complexity": data.Totals.Complexity,
			"bytes":      data.Totals.Bytes,
		},
	})
}

// finish publishes the terminal transition under the registry lock.
func (j *reportJob) finish(state jobState, data *processor.ReportData, errText string) {
	jobRegistryMu.Lock()
	defer jobRegistryMu.Unlock()
	j.state = state
	j.data = data
	j.errText = errText
}

// reportJobStatus returns the status body for a report job id, reporting
// whether the id belongs to the report registry. This is the T-033 seam the
// shared /api/job/{id} handler consults when the analyze slot misses.
func reportJobStatus(id string) (any, bool) {
	if id == "" {
		return nil, false
	}
	jobRegistryMu.Lock()
	defer jobRegistryMu.Unlock()
	job := currentReportJob
	if job == nil || job.id != id {
		return nil, false
	}
	switch job.state {
	case jobStateDone:
		return reportJobStatusPayload{
			Status: string(jobStateDone),
			Job:    &reportJobPayload{DownloadURL: downloadPathPrefix + job.id, Options: job.options},
		}, true
	case jobStateError:
		return reportJobStatusPayload{Status: string(jobStateError), Error: job.errText}, true
	default:
		return reportJobStatusPayload{Status: string(jobStateRunning)}, true
	}
}

// doneReportData returns the published report of a done job id, reporting
// whether a downloadable report exists right now.
func doneReportData(id string) (*processor.ReportData, bool) {
	if id == "" {
		return nil, false
	}
	jobRegistryMu.Lock()
	defer jobRegistryMu.Unlock()
	job := currentReportJob
	if job == nil || job.id != id || job.state != jobStateDone || job.data == nil {
		return nil, false
	}
	return job.data, true
}

// historyObserverSections are the git-history driven sections
// CollectReportData only runs when detectGit succeeds (report.go:332/:361).
// includeHistory=false (DEC-013, the default) skips exactly these; every other
// section keeps the CLI behaviour.
var historyObserverSections = [4]string{"hotspots", "coupling", "authors", "timeline"}

// historySkipSet builds the ReportSkipNames view of the includeHistory option:
// an empty map means "run every section" (ReportSkipped is false for an empty
// map), the default pins the four history observers off.
func historySkipSet(includeHistory bool) map[string]bool {
	set := make(map[string]bool, len(historyObserverSections))
	if includeHistory {
		return set
	}
	for _, section := range historyObserverSections {
		set[section] = true
	}
	return set
}

// applyReportFlags sets every processor global the report chain reads before a
// run (DEC-005): the walk scope mirrors the CLI defaults, the filter and
// unknown handling come from the request, and the report-only globals pin the
// K3 title annotation and the DEC-013 history switch. Runs hold analyzeMu, and
// health/config touch none of these, so no other code path can observe the
// reset window. CollectReportData manages UlocMode/MaxMean/Files itself
// (snapshot, flip, restore — report.go:314-341), so they are deliberately not
// touched here.
func applyReportFlags(opts reportOptions, extensions []string) {
	defaults := flagDefaults()
	processor.Verbose = false
	processor.Debug = false
	processor.Trace = false

	// Walk scope: the walker inside walkAndAggregate takes the path as a
	// parameter, so only the exclusion and ignore defaults are pinned here.
	processor.PathDenyList = slices.Clone(defaults.pathDenyList)
	processor.ExcludeFilename = slices.Clone(defaults.excludeFilename)
	processor.Exclude = nil
	processor.ExcludeListExtensions = nil
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

	// Metrics: complexity counting is on (the CLI flag is a disable switch and
	// the report needs complexity totals), everything that adds output the
	// report does not consume stays off.
	processor.Duplicates = false
	processor.SpecialisedCounters = false
	processor.DisableCheckBinary = false
	processor.Percent = false
	processor.Complexity = false
	processor.Cognitive = false
	processor.Dryness = false

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

	// Feed-forward switches processFlags reads (processor.go:932-1000); this
	// chain never calls processFlags, and they stay off on principle.
	processor.More = false
	processor.CostComparison = false
	processor.Locomo = false
	processor.Cocomo = false // CLI default: the report carries the COCOMO block

	processor.SortBy = defaults.sortBy

	// Request-scoped engine switches, always set explicitly so nothing from a
	// previous job leaks in (DEC-005/DEC-016).
	processor.CountUnsupported = opts.includeUnknown
	processor.AllowListExtensions = extensions // K3 filter, off when nil

	// Report-only globals: the K3/Q7 title annotation and the DEC-013 history
	// switch. An empty title leaves detectRepoName's auto chain intact, which
	// is where the remote.origin.url derived repository name comes from
	// (DEC-307).
	processor.ReportTitle = filteredReportTitle(opts.path, opts.languages)
	processor.ReportSkipNames = historySkipSet(opts.includeHistory)
}

// filteredReportTitle injects the K3/Q7 scope annotation into the report
// banner title: "pure-tree（筛选：Go、Java）". An unfiltered report leaves the
// title empty so the auto-detected repository name stays (TC-057 pins the
// filtered form).
func filteredReportTitle(absPath string, languages []string) string {
	if len(languages) == 0 {
		return ""
	}
	return filepath.Base(absPath) + "（筛选：" + strings.Join(languages, "、") + "）"
}
