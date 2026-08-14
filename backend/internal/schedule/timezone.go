// Package schedule contains shared validation for application-level schedules.
package schedule

import (
	"errors"
	"strings"
	"time"
	_ "time/tzdata"
)

const DefaultTimezone = "Asia/Shanghai"

var ErrInvalidTimezone = errors.New("timezone must be a valid IANA timezone name")

// LoadTimezone validates an explicit IANA timezone and returns its normalized
// spelling together with the resolved location. "Local" is deliberately
// rejected because it would make an application schedule depend on the host's
// system timezone again.
func LoadTimezone(value string) (string, *time.Location, error) {
	normalized := strings.TrimSpace(value)
	if normalized == "" || normalized == "Local" {
		return "", nil, ErrInvalidTimezone
	}
	location, err := time.LoadLocation(normalized)
	if err != nil {
		return "", nil, ErrInvalidTimezone
	}
	return normalized, location, nil
}
