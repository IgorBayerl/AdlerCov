package reporter

import "github.com/IgorBayerl/AdlerCov/internal/model"

// ReportBuilder interface defines methods that all report generators must implement
type ReportBuilder interface {
	// ReportType returns the type of report this builder generates
	ReportType() string

	// CreateReport generates the report from the coverage data
	CreateReport(report *model.SummaryResult) error
}
