// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/boyter/scc/v4/gui"
	"github.com/boyter/scc/v4/i18n"
	"github.com/boyter/scc/v4/processor"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func printShellCompletion(cmd *cobra.Command, command string) error {
	switch command {
	case "bash":
		return cmd.GenBashCompletionV2(os.Stdout, true)
	case "zsh":
		return cmd.GenZshCompletion(os.Stdout)
	case "fish":
		return cmd.GenFishCompletion(os.Stdout, true)
	case "powershell":
		return cmd.GenPowerShellCompletion(os.Stdout)
	default:
		return errors.New("Unknown shell: " + command)
	}
}

func printFlagSuggestion(flagSet *pflag.FlagSet, unknownFlag string) {
	flags := processor.GetMostSimilarFlags(flagSet, unknownFlag)
	if len(flags) == 0 {
		return
	}

	// The suggestion lines stay in the scc 4.1.0 English wording on purpose
	// (phase-1 localization surface excludes them, DEC-201 conservative
	// clause): TestFlagSuggestion asserts these exact strings under the zh
	// default, so translating them would break an existing test.
	if len(flags) > 1 {
		_, _ = fmt.Fprintf(os.Stderr, "The most similar flags of --%s are:\n", unknownFlag)
	} else {
		_, _ = fmt.Fprintf(os.Stderr, "The most similar flag of --%s is:\n", unknownFlag)
	}

	for _, flag := range flags {
		_, _ = fmt.Fprintf(os.Stderr, "\t--%s\n", flag)
	}
}

// Help text keys are the Chinese copy itself (i18n convention): zh shows the
// key verbatim while en resolves it in dict_en_cli.go back to the scc 4.1.0
// original, keeping `--lang en --help` byte-identical to the baseline binary.
const (
	cliLongHead = "Sloc, Cloc and Code. 统计目录中的代码行数并估算复杂度。\n版本 "
	cliLongTail = "\nBen Boyter <ben@boyter.org> 及贡献者\nhttps://github.com/boyter/scc"
	cliExample  = `  统计当前目录：
    scc

  统计指定文件夹或文件：
    scc myproject/
    scc main.go

  一次统计多个路径：
    scc src/ docs/ README.md

  逐文件明细输出（默认按语言汇总）：
    scc --by-file

  以 CSV 或 JSON 输出（例如供进一步处理使用）：
    scc --format csv
    scc --format json -o counts.json

  把无法识别的扩展名按已知语言统计：
    scc --count-as jsp:html

  把匹配路径模式的文件按基础语言统计为新类别（默认 glob，可用 re: 前缀改为正则）：
    scc --count-as-pattern '*_spec.rb:Ruby Spec:Ruby'

  生成自包含的 HTML 信息图报告：
    scc --report
    scc --report=out.html --report-title "myrepo" --report-skip cocomo

  使用项目配置文件（./.sccconfig）或全局配置文件（优先级：全局 < 项目 < 命令行）：
    export SCC_CONFIG_PATH=~/.sccconfig
    scc --config team.sccconfig

  调整 COCOMO 成本估算参数，或关闭估算（见 https://en.wikipedia.org/wiki/COCOMO）：
    scc --avg-wage 75000 --cocomo-project-type semi-detached
    scc --no-cocomo`
)

// GUI launch options staged from argv and the environment before cobra runs.
// Zero values mean "unset": a zero port lets the GUI pick a random free port,
// which also keeps an explicit port unrepresentable as unset.
var (
	guiPort      int
	guiNoBrowser bool
)

const (
	guiFlag  = "--gui"
	langFlag = "--lang"
	portFlag = "--port"
)

// guiRequested reports whether argv explicitly asks for the GUI via --gui,
// matching the bare boolean form and the --gui=true spelling pflag accepts.
func guiRequested(args []string) bool {
	return slices.ContainsFunc(args, func(a string) bool {
		return a == guiFlag || a == guiFlag+"=true"
	})
}

// discoverConfigPresent reports whether the config discovery chain would hand
// this launch any CLI tokens (SCC_CONFIG_PATH global or project .sccconfig).
// It is the bare-launch tiebreaker (DI-003): with nothing on the command line
// the binary goes to the GUI only when no config speaks for a CLI run, so a
// bare sccv inside a configured project keeps the original scan semantics.
// A discovery failure counts as present: the CLI path then surfaces the same
// config error it always did instead of silently swallowing it behind a GUI.
//
// Only reached with zero surviving arguments, so neither the completion
// invocation guard nor @file expansion can change the outcome; the CLI path
// below re-runs discovery on the fully expanded argv exactly as before.
func discoverConfigPresent(osArgs []string) bool {
	noConfig, findRoot, explicitPath := preScanConfig(osArgs)
	globalTokens, projectTokens, err := discoverConfigArgs(noConfig, findRoot, explicitPath)
	if err != nil {
		return true
	}
	return len(globalTokens) != 0 || len(projectTokens) != 0
}

// stripGUIFlags removes the GUI-only --lang/--port flags from args and
// returns the surviving arguments plus the language and port found. Both the
// separated (--lang en) and the '=' (--lang=en) spellings are accepted, and
// the last occurrence wins. Neither flag exists in scc, so any occurrence
// left behind would make cobra exit(1) on an unknown flag; a flag missing its
// value is a usage error and fails loudly here instead.
func stripGUIFlags(args []string) (clean []string, lang string, port int, err error) {
	clean = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case strings.HasPrefix(arg, langFlag+"="):
			lang = strings.TrimPrefix(arg, langFlag+"=")
		case arg == langFlag:
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return nil, "", 0, fmt.Errorf("%s requires a language code", langFlag)
			}
			lang = args[i+1]
			i++
		case strings.HasPrefix(arg, portFlag+"="):
			port, err = parseGUIPort(strings.TrimPrefix(arg, portFlag+"="))
			if err != nil {
				return nil, "", 0, err
			}
		case arg == portFlag:
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return nil, "", 0, fmt.Errorf("%s requires a port number", portFlag)
			}
			port, err = parseGUIPort(args[i+1])
			if err != nil {
				return nil, "", 0, err
			}
			i++
		default:
			clean = append(clean, arg)
		}
	}
	return clean, lang, port, nil
}

// parseGUIPort validates a port taken from --port or SCCV_PORT so an
// impossible binding fails at startup instead of inside the GUI.
func parseGUIPort(value string) (int, error) {
	port, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid port %q: must be a number between 1 and 65535", value)
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid port %d: must be between 1 and 65535", port)
	}
	return port, nil
}

// resolveLanguage picks the UI language: an explicit --lang beats the
// SCCV_LANG environment variable, and Chinese is the product default. The
// choice is applied before any flag registration so help text is built in the
// right language.
func resolveLanguage(cliLang string) string {
	if cliLang != "" {
		return cliLang
	}
	if envLang := os.Getenv("SCCV_LANG"); envLang != "" {
		return envLang
	}
	return "zh"
}

//go:generate go run scripts/include.go
func main() {
	// f, _ := os.Create("scc.pprof")
	// pprof.StartCPUProfile(f)
	// defer pprof.StopCPUProfile()

	// Parse and strip the GUI-only --lang/--port flags before cobra sees
	// argv, and stage their values plus the SCCV_PORT/SCCV_NO_BROWSER
	// environment settings. scc registers neither flag, so anything left
	// behind would exit(1) as an unknown flag. The language is resolved here
	// too: CLI --lang wins over SCCV_LANG, and Chinese is the product default.
	cleanArgs, cliLang, cliPort, parseErr := stripGUIFlags(os.Args[1:])
	if parseErr != nil {
		fmt.Fprintf(os.Stderr, "sccv: %s\n", parseErr)
		os.Exit(1)
	}
	os.Args = append([]string{os.Args[0]}, cleanArgs...)

	guiPort = cliPort
	if guiPort == 0 {
		if envPort := os.Getenv("SCCV_PORT"); envPort != "" {
			port, envErr := parseGUIPort(envPort)
			if envErr != nil {
				fmt.Fprintf(os.Stderr, "sccv: SCCV_PORT: %s\n", envErr)
				os.Exit(1)
			}
			guiPort = port
		}
	}
	guiNoBrowser = os.Getenv("SCCV_NO_BROWSER") == "1"
	cliLanguage := resolveLanguage(cliLang)
	i18n.SetLang(cliLanguage)

	// DEC-001 dual-form dispatch as refined by DI-003, judged on what survives
	// the stripping above: a bare launch (no argument left at all — the
	// double-click case) starts the GUI only when no discoverable config
	// (SCC_CONFIG_PATH or project .sccconfig) would have driven a CLI scan,
	// an explicit --gui forces the GUI no matter what else is on the line,
	// and everything else keeps the original cobra semantics. The decision
	// runs before the --mcp interception, config discovery and @file
	// expansion, so neither a ./.sccconfig nor a flags file can turn a
	// double-click into a CLI scan — while a bare launch inside a configured
	// project still scans, exactly as scc always did.
	if guiRequested(cleanArgs) || (len(cleanArgs) == 0 && !discoverConfigPresent(os.Args)) {
		if startErr := gui.Start(gui.ServerOptions{
			Port:      guiPort,
			Lang:      cliLanguage,
			NoBrowser: guiNoBrowser,
		}); startErr != nil {
			fmt.Fprintf(os.Stderr, "sccv: %s\n", startErr)
			os.Exit(1)
		}
		// Start blocks until the server is shut down; the process ends here.
		return
	}

	// Handle --mcp flag before cobra to avoid interfering with stdio. Match both
	// the bare boolean form and the explicit --mcp=true form pflag accepts, so the
	// server starts consistently however the flag is spelled.
	if slices.ContainsFunc(os.Args[1:], func(a string) bool {
		return a == "--mcp" || a == "--mcp=true"
	}) {
		startMCPServer()
		return
	}

	// CLI branch from here on: reattach to the parent console before anything
	// prints (a no-op off Windows). The windowsgui release build starts without
	// usable std handles when run from a terminal, while a redirected pipe or
	// file keeps working untouched.
	setupConsole()

	// handle "scc @flags.txt" syntax. The sole-argument trigger is preserved;
	// only the splitter is swapped for the shared tokenizer, which adds comment
	// stripping, quote-aware tokenization and drops the old blank-line empty-arg
	// bug.
	if len(os.Args) == 2 && strings.HasPrefix(os.Args[1], "@") {
		filename := strings.TrimPrefix(os.Args[1], "@")
		b, err := os.ReadFile(filename)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading flags from a file: %s\n", err)
			os.Exit(1)
		}
		os.Args = append([]string{os.Args[0]}, parseConfigArgs(string(b), true)...)
	}

	// What the user actually specified
	genuineCLI := slices.Clone(os.Args[1:])

	// Cobra's completion machinery must see the genuine argv. The shell invokes
	// the hidden __complete / __completeNoDesc commands on every TAB, and the
	// user-facing `completion` command generates the scripts; both key on the
	// subcommand sitting in args[0]. Prepending discovered config tokens would
	// shift it and break dynamic completion in any directory containing a ./.sccconfig.
	// Config never influences completion output anyway, so skip discovery for it.
	var globalTokens, projectTokens []string
	if !isCompletionInvocation(os.Args) {
		noConfig, findRoot, explicitPath := preScanConfig(os.Args)
		var discoverErr error
		globalTokens, projectTokens, discoverErr = discoverConfigArgs(noConfig, findRoot, explicitPath)
		if discoverErr != nil {
			processor.PrintError(discoverErr.Error())
			os.Exit(1)
		}
	}

	// Fast path when no config was discovered: the merged list *is* the genuine
	// CLI, so bind write flags directly to the real vars and parse once, exactly
	// as scc does today. The discard / CLI-only split engages only when config is
	// actually present.
	// Ensures we follow old scc logic without config
	configPresent := len(globalTokens) != 0 || len(projectTokens) != 0

	// Build the merged argument list N.B. ORDER MATTERS HERE! CLI MUST COME LAST!
	var merged []string
	merged = append(merged, os.Args[0])
	merged = append(merged, globalTokens...)
	merged = append(merged, projectTokens...)
	merged = append(merged, genuineCLI...)

	// Write-flag bindings for the merged parse
	var discardOutput, discardReport, discardFormatMulti string
	bindings := &flagBindings{
		output:      &processor.FileOutput,
		report:      &processor.ReportOut,
		formatMulti: &processor.FormatMulti,
	}

	// if we use config, we NEVER allow writing to disk because someone could use that as
	// an attack vector, so we reset these options to ensure this is not a risk
	if configPresent {
		bindings.output = &discardOutput
		bindings.report = &discardReport
		bindings.formatMulti = &discardFormatMulti
	}

	rootCmd := &cobra.Command{
		Use:     "scc [flags] [files or directories]",
		Short:   "scc [files or directories]",
		Long:    i18n.T(cliLongHead) + processor.Version + i18n.T(cliLongTail),
		Example: i18n.T(cliExample),
		Version: processor.Version,
		Run: func(cmd *cobra.Command, args []string) {
			processor.DirFilePaths = args
			processor.ConfigureGc()
			processor.ConfigureLazy(true)

			// Detect if LOCOMO price/tps flags were explicitly set. Their default
			// is 0, which is ambiguous (unset vs. an explicit 0), so the processor
			// needs the "was it set?" bit to decide between the preset value and the
			// user override. Only pflag's Changed() knows this, hence here not there.
			processor.LocomoInputPriceSet = cmd.PersistentFlags().Changed("locomo-input-price")
			processor.LocomoOutputPriceSet = cmd.PersistentFlags().Changed("locomo-output-price")
			processor.LocomoTPSSet = cmd.PersistentFlags().Changed("locomo-tps")
			processor.LocomoCyclesSet = cmd.PersistentFlags().Changed("locomo-cycles")

			if v, err := cmd.PersistentFlags().GetBool("no-fold-authors"); err == nil && v {
				processor.FoldAuthors = false
			}

			// Source the write vars from the genuine CLI alone (file output is a
			// CLI-only capability), then warn if config tried to set one.
			if configPresent {
				cliSet := resolveWriteFlags(genuineCLI)
				warnIfConfigWrote(cmd.PersistentFlags(), cliSet)
			}

			// Merge the built-in defaults back into the empty-defaulted slice
			// flags, then flush any buffered config trace/debug.
			applySliceDefaults()
			flushConfigTrace()

			processor.Process()
		},
	}

	flags := rootCmd.PersistentFlags()
	registerFlags(flags, bindings)
	registerConfigControlFlags(flags)

	// If invoked in the format of "scc completion --shell [name of shell]", generate command line completions instead.
	// With the --shell option, unintentionally triggering shell completions should be highly unlikely. This reads the
	// genuine os.Args (config tokens are handed to cobra via SetArgs below, never prepended into os.Args), so a ./.sccconfig
	// in the working directory cannot shift args[1] and break completion.
	args := os.Args
	if len(args) == 4 && args[1] == "completion" && args[2] == "--shell" {
		err := printShellCompletion(rootCmd, args[3])
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Error printing shell completion: %s\n", err)
		}
		return
	}

	// Hand the merged list to cobra without mutating os.Args.
	rootCmd.SetArgs(merged[1:])

	if err := rootCmd.Execute(); err != nil {
		// If a flag does not exist and is not a shorthand, it may be a spelling error. Search for and print possible options.
		if notExistError, ok := err.(*pflag.NotExistError); ok && len(notExistError.GetSpecifiedName()) > 1 {
			name := notExistError.GetSpecifiedName()
			// Best-effort: attribute the unknown flag to the config file it came.
			if src := attributeConfigFlag(name); src != "" {
				_, _ = fmt.Fprintf(os.Stderr, "in %s: unknown flag --%s\n", src, name)
			}
			printFlagSuggestion(flags, name)
		}
		os.Exit(1)
	}
}
