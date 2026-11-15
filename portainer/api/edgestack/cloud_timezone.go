package edgestack

import (
	"strings"
	"time"
	_ "time/tzdata" // Keep IANA validation available in minimal runtime images.
)

const DefaultSummaryTimeZone = "Asia/Shanghai"

func summaryTimeZone(name string) (string, *time.Location, error) {
	if name == "" {
		name = DefaultSummaryTimeZone
	}
	if name == "Local" || len(name) > 128 || strings.TrimSpace(name) != name {
		return "", nil, fail(400, "invalid_summary_timezone")
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return "", nil, fail(400, "invalid_summary_timezone")
	}
	return name, location, nil
}
