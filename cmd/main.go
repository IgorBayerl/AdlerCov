package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/IgorBayerl/AdlerCov/internal/analyzer"
	"github.com/IgorBayerl/AdlerCov/internal/filereader"
	"github.com/IgorBayerl/AdlerCov/internal/glob"
	"github.com/IgorBayerl/AdlerCov/internal/logging"
	"github.com/IgorBayerl/AdlerCov/internal/model"
	"github.com/IgorBayerl/AdlerCov/internal/reportconfig"
	"github.com/IgorBayerl/AdlerCov/internal/reporter"
	"github.com/IgorBayerl/AdlerCov/internal/settings"
	"github.com/IgorBayerl/AdlerCov/internal/utils"

	// reporters
	"github.com/IgorBayerl/AdlerCov/internal/reporter/htmlreport"
	"github.com/IgorBayerl/AdlerCov/internal/reporter/lcov"
	"github.com/IgorBayerl/AdlerCov/internal/reporter/textsummary"

	// language specific behaviours
	"github.com/IgorBayerl/AdlerCov/internal/language"
	"github.com/IgorBayerl/AdlerCov/internal/language/csharp"
	"github.com/IgorBayerl/AdlerCov/internal/language/defaultformatter"
	"github.com/IgorBayerl/AdlerCov/internal/language/golang"

	// parsers
	"github.com/IgorBayerl/AdlerCov/internal/parsers"
	"github.com/IgorBayerl/AdlerCov/internal/parsers/cobertura"
	"github.com/IgorBayerl/AdlerCov/internal/parsers/gocover"
)

var ErrMissingReportFlag = errors.New("missing required -report flag")

type cliFlags struct {
	// domain
	reportsPatterns   *string
	outputDir         *string
	reportTypes       *string
	sourceDirs        *string
	tag               *string
	title             *string
	assemblyFilters   *string
	classFilters      *string
	fileFilters       *string
	rhAssemblyFilters *string
	rhClassFilters    *string

	// logging
	verbose   *bool
	verbosity *string
	logFile   *string
	logFormat *string
}

func parseFlags() (*cliFlags, error) {
	f := &cliFlags{
		// domain flags
		reportsPatterns:   flag.String("report", "", "Coverage report file paths or patterns (semicolon-separated)"),
		outputDir:         flag.String("output", "coverage-report", "Output directory for generated reports"),
		reportTypes:       flag.String("reporttypes", "TextSummary,Html", "Report types (comma-separated)"),
		sourceDirs:        flag.String("sourcedirs", "", "Source directories (comma-separated)"),
		tag:               flag.String("tag", "", "Optional tag, e.g. build number"),
		title:             flag.String("title", "", "Optional report title (default: 'Coverage Report')"),
		assemblyFilters:   flag.String("assemblyfilters", "", "Assembly filters (+Include;-Exclude)"),
		classFilters:      flag.String("classfilters", "", "Class filters"),
		fileFilters:       flag.String("filefilters", "", "File filters"),
		rhAssemblyFilters: flag.String("riskhotspotassemblyfilters", "", "Risk-hotspot assembly filters"),
		rhClassFilters:    flag.String("riskhotspotclassfilters", "", "Risk-hotspot class filters"),

		// logging flags
		verbose:   flag.Bool("verbose", false, "Shortcut for Verbose logging (overridden by -verbosity)"),
		verbosity: flag.String("verbosity", "Error", "Logging level: Verbose, Info, Warning, Error, Off"),
		logFile:   flag.String("logfile", "", "Write logs to this file as well as the console"),
		logFormat: flag.String("logformat", "text", "Log output format: text (default) or json"),
	}

	flag.Parse()
	return f, nil
}

func buildLogger(f *cliFlags) (logging.VerbosityLevel, io.Closer, error) {
	verbosityStr := strings.TrimSpace(*f.verbosity)
	level, err := logging.ParseVerbosity(verbosityStr)
	if err != nil && verbosityStr != "" {
		return 0, nil, err
	}

	switch {
	case verbosityStr != "" && verbosityStr != "Error":
	case *f.verbose:
		level = logging.Verbose
	default:
		level = logging.Error
	}

	cfg := logging.Config{
		Verbosity: level,
		File:      *f.logFile,
		Format:    *f.logFormat,
	}
	closer, err := logging.Init(&cfg)
	return level, closer, err
}

// Helpers

func resolveAndValidateInputs(logger *slog.Logger, flags *cliFlags) ([]string, []string, error) {
	if *flags.reportsPatterns == "" {
		return nil, nil, ErrMissingReportFlag
	}

	reportFilePatterns := strings.Split(*flags.reportsPatterns, ";")
	var actualReportFiles []string
	var invalidPatterns []string
	seenFiles := make(map[string]struct{})

	for _, pattern := range reportFilePatterns {
		trimmedPattern := strings.TrimSpace(pattern)
		if trimmedPattern == "" {
			continue
		}
		expandedFiles, err := glob.GetFiles(trimmedPattern)
		if err != nil {
			logger.Warn("Error expanding report file pattern", "pattern", trimmedPattern, "error", err)
			invalidPatterns = append(invalidPatterns, trimmedPattern)
			continue
		}
		if len(expandedFiles) == 0 {
			logger.Warn("No files found for report pattern", "pattern", trimmedPattern)
			invalidPatterns = append(invalidPatterns, trimmedPattern)
		}
		for _, file := range expandedFiles {
			absFile, _ := filepath.Abs(file)
			if _, exists := seenFiles[absFile]; !exists {
				if stat, err := os.Stat(absFile); err == nil && !stat.IsDir() {
					actualReportFiles = append(actualReportFiles, absFile)
					seenFiles[absFile] = struct{}{}
				} else if err != nil {
					logger.Warn("Could not stat file from pattern", "pattern", trimmedPattern, "file", absFile, "error", err)
					invalidPatterns = append(invalidPatterns, file)
				}
			}
		}
	}

	if len(actualReportFiles) == 0 {
		return nil, invalidPatterns, fmt.Errorf("no valid report files found after expanding patterns")
	}

	logger.Info("Found report files", "count", len(actualReportFiles))
	logger.Debug("Report file list", "files", strings.Join(actualReportFiles, ", "))
	return actualReportFiles, invalidPatterns, nil
}

func createReportConfiguration(flags *cliFlags, verbosity logging.VerbosityLevel, actualReportFiles, invalidPatterns []string, langFactory *language.ProcessorFactory, logger *slog.Logger) (*reportconfig.ReportConfiguration, error) {
	reportTypes := strings.Split(*flags.reportTypes, ",")
	sourceDirsList := strings.Split(*flags.sourceDirs, ",")
	assemblyFilterStrings := strings.Split(*flags.assemblyFilters, ";")
	classFilterStrings := strings.Split(*flags.classFilters, ";")
	fileFilterStrings := strings.Split(*flags.fileFilters, ";")
	rhAssemblyFilterStrings := strings.Split(*flags.rhAssemblyFilters, ";")
	rhClassFilterStrings := strings.Split(*flags.rhClassFilters, ";")

	opts := []reportconfig.Option{
		reportconfig.WithLogger(logger),
		reportconfig.WithVerbosity(verbosity),
		reportconfig.WithInvalidPatterns(invalidPatterns),
		reportconfig.WithTitle(*flags.title),
		reportconfig.WithTag(*flags.tag),
		reportconfig.WithSourceDirectories(sourceDirsList),
		reportconfig.WithReportTypes(reportTypes),
		reportconfig.WithFilters(
			assemblyFilterStrings,
			classFilterStrings,
			fileFilterStrings,
			rhAssemblyFilterStrings,
			rhClassFilterStrings,
		),
		reportconfig.WithLanguageProcessorFactory(langFactory),
	}

	return reportconfig.NewReportConfiguration(
		actualReportFiles,
		*flags.outputDir,
		opts...,
	)
}

// parseReportFiles iterates through the report file patterns, parses each valid file,
// and returns the collected results, any unresolved source file paths, and any parsing errors.
func parseReportFiles(logger *slog.Logger, reportConfig *reportconfig.ReportConfiguration, parserFactory *parsers.ParserFactory) ([]*parsers.ParserResult, []string, []string) {
	var parserResults []*parsers.ParserResult
	var parserErrors []string
	var allUnresolvedFiles []string

	for _, reportFile := range reportConfig.ReportFiles() {
		logger.Info("Attempting to parse report file", "file", reportFile)
		parserInstance, err := parserFactory.FindParserForFile(reportFile)
		if err != nil {
			msg := fmt.Sprintf("no suitable parser found for file %s: %v", reportFile, err)
			parserErrors = append(parserErrors, msg)
			logger.Warn(msg)
			continue
		}

		logger.Info("Using parser for file", "parser", parserInstance.Name(), "file", reportFile)

		result, err := parserInstance.Parse(reportFile, reportConfig)
		if err != nil {
			msg := fmt.Sprintf("error parsing file %s with %s: %v", reportFile, parserInstance.Name(), err)
			parserErrors = append(parserErrors, msg)
			logger.Error(msg)
			continue
		}

		if len(result.UnresolvedSourceFiles) > 0 {
			allUnresolvedFiles = append(allUnresolvedFiles, result.UnresolvedSourceFiles...)
		}

		parserResults = append(parserResults, result)
		logger.Info("Successfully parsed file", "file", reportFile)

		if len(reportConfig.SourceDirectories()) == 0 && len(result.SourceDirectories) > 0 {
			logger.Info("Report specified source directories, updating configuration", "file", reportFile, "dirs", result.SourceDirectories)
			if err := reportconfig.WithSourceDirectories(result.SourceDirectories)(reportConfig); err != nil {
				logger.Warn("Failed to apply source directories", "error", err)
			}
		}
	}

	return parserResults, allUnresolvedFiles, parserErrors
}

func parseAndMergeReports(logger *slog.Logger, reportConfig *reportconfig.ReportConfiguration, parserFactory *parsers.ParserFactory) (*model.SummaryResult, error) {
	parserResults, allUnresolvedFiles, parserErrors := parseReportFiles(logger, reportConfig, parserFactory)

	// any source files were not found.
	if len(allUnresolvedFiles) > 0 {
		uniqueUnresolvedFiles := utils.DistinctBy(allUnresolvedFiles, func(s string) string { return s })

		logger.Error("Failed to find source files referenced in coverage report",
			"count", len(uniqueUnresolvedFiles))
		logger.Error("This is a fatal error because it would result in an incorrect or empty report")
		logger.Error("Please provide the root directory of your source code using the '-sourcedirs' flag")
		logger.Error("Examples of missing files:")

		limit := 5
		if len(uniqueUnresolvedFiles) < limit {
			limit = len(uniqueUnresolvedFiles)
		}
		for i := 0; i < limit; i++ {
			logger.Error("Missing file", "file", uniqueUnresolvedFiles[i])
		}

		return nil, errors.New("failed to find source files referenced in coverage report")
	}

	// no reports could be parsed at all
	if len(parserResults) == 0 {
		errMsg := "no coverage reports could be parsed successfully"
		if len(parserErrors) > 0 {
			errMsg = fmt.Sprintf("%s. Errors:\r\n- %s", errMsg, strings.Join(parserErrors, "\r\n- "))
		}
		return nil, errors.New(errMsg)
	}

	logger.Info("Merging parsed reports", "count", len(parserResults))
	summaryResult, err := analyzer.MergeParserResults(parserResults, reportConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to merge parser results: %w", err)
	}
	logger.Info("Coverage data merged and analyzed")
	return summaryResult, nil
}

func generateReports(reportCtx reporter.IBuilderContext, summaryResult *model.SummaryResult) error {
	logger := reportCtx.Logger()
	reportConfig := reportCtx.ReportConfiguration()
	outputDir := reportConfig.TargetDirectory()

	logger.Info("Generating reports", "directory", outputDir)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	for _, reportType := range reportConfig.ReportTypes() {
		trimmedType := strings.TrimSpace(reportType)
		logger.Info("Generating report", "type", trimmedType)

		switch trimmedType {
		case "TextSummary":
			if err := textsummary.NewTextReportBuilder(outputDir, logger).CreateReport(summaryResult); err != nil {
				return fmt.Errorf("failed to generate text report: %w", err)
			}
		case "Html":
			if err := htmlreport.NewHtmlReportBuilder(outputDir, reportCtx).CreateReport(summaryResult); err != nil {
				return fmt.Errorf("failed to generate HTML report: %w", err)
			}
		case "Lcov":
			if err := lcov.NewLcovReportBuilder(outputDir).CreateReport(summaryResult); err != nil {
				return fmt.Errorf("failed to generate lcov report: %w", err)
			}
		}
	}
	return nil
}

func run(flags *cliFlags) error {
	logger := slog.Default()

	// Re-get the verbosity level from the flags, as it's needed for ReportConfiguration.
	verbosityStr := strings.TrimSpace(*flags.verbosity)
	verbosity, _ := logging.ParseVerbosity(verbosityStr)
	if *flags.verbose {
		verbosity = logging.Verbose
	}

	// Create all desired language processors and the factory that holds them.
	langFactory := language.NewProcessorFactory(
		defaultformatter.NewDefaultProcessor(),
		csharp.NewCSharpProcessor(),
		golang.NewGoProcessor(),
	)

	// The fileReader dependency is created here once from the central package.
	prodFileReader := filereader.NewDefaultReader()
	parserFactory := parsers.NewParserFactory(
		cobertura.NewCoberturaParser(prodFileReader),
		gocover.NewGoCoverParser(prodFileReader),
	)

	actualReportFiles, invalidPatterns, err := resolveAndValidateInputs(logger, flags)
	if err != nil {
		if len(invalidPatterns) > 0 {
			return fmt.Errorf("%w; invalid patterns: %s", err, strings.Join(invalidPatterns, ", "))
		}
		return err
	}

	// Pass the language factory to create the configuration
	reportConfig, err := createReportConfiguration(flags, verbosity, actualReportFiles, invalidPatterns, langFactory, logger)
	if err != nil {
		return err
	}

	// Pass the parser factory to the parsing logic
	summaryResult, err := parseAndMergeReports(logger, reportConfig, parserFactory)
	if err != nil {
		return err
	}

	reportCtx := reporter.NewBuilderContext(reportConfig, settings.NewSettings(), logger)
	return generateReports(reportCtx, summaryResult)
}

func main() {
	start := time.Now()

	flags, err := parseFlags()
	if err != nil {
		fmt.Fprintln(os.Stderr, "flag error:", err)
		os.Exit(1)
	}

	_, closer, err := buildLogger(flags)
	if err != nil {
		fmt.Fprintln(os.Stderr, "logger init error:", err)
		os.Exit(1)
	}
	if closer != nil {
		defer closer.Close()
	}

	if err := run(flags); err != nil {
		slog.Error("An error occurred during report generation", "error", err)

		if errors.Is(err, ErrMissingReportFlag) {
			fmt.Fprintln(os.Stderr, "")
			flag.Usage()
		}

		os.Exit(1)
	}

	slog.Info("Report generation completed successfully", "duration", time.Since(start).Round(time.Millisecond))
}
