package executor

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// parseTimestamp attempts to parse a string as a timestamp in various formats.
func parseTimestamp(s string) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
		"01/02/2006 15:04:05",
		"01/02/2006",
	}
	for _, layout := range formats {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse timestamp %q", s)
}

// sqlToGoLayout converts SQL date format to Go layout.
func sqlToGoLayout(layout string) string {
	sqlTokens := []struct{ sql, goLayout string }{
		{"YYYY", "2006"},
		{"YY", "06"},
		{"HH24", "15"},
		{"MM", "01"},
		{"DD", "02"},
		{"MI", "04"},
		{"SS", "05"},
		{"HH", "03"},
		{"AM", "PM"},
		{"PM", "PM"},
	}
	result := layout
	for _, t := range sqlTokens {
		result = strings.ReplaceAll(result, t.sql, t.goLayout)
	}
	return result
}

// isIntervalString checks if a string is a SQL interval.
func isIntervalString(s string) bool {
	s = strings.TrimSpace(s)
	s = strings.ToUpper(s)
	return strings.Contains(s, "INTERVAL") || strings.HasSuffix(s, "DAYS") ||
		strings.HasSuffix(s, "HOURS") || strings.HasSuffix(s, "MINUTES") ||
		strings.HasSuffix(s, "SECONDS") || strings.HasSuffix(s, "MONTHS") ||
		strings.HasSuffix(s, "YEARS")
}

// evalDateInterval computes date interval arithmetic.
func evalDateInterval(dateStr, intervalStr, op string) (interface{}, error) {
	t, err := parseTimestamp(dateStr)
	if err != nil {
		return nil, fmt.Errorf("date interval: %w", err)
	}
	intervalStr = strings.TrimSpace(intervalStr)
	intervalStr = strings.TrimPrefix(strings.ToUpper(intervalStr), "INTERVAL")
	intervalStr = strings.TrimSpace(intervalStr)
	intervalStr = strings.Trim(intervalStr, "'\"")

	amount := 1
	parts := strings.Fields(intervalStr)
	if len(parts) >= 2 {
		if n, err := strconv.Atoi(parts[0]); err == nil {
			amount = n
		}
	}
	unit := strings.TrimSuffix(strings.ToUpper(parts[len(parts)-1]), "S")

	switch op {
	case "+":
		switch unit {
		case "DAY":
			t = t.AddDate(0, 0, amount)
		case "HOUR":
			t = t.Add(time.Duration(amount) * time.Hour)
		case "MINUTE":
			t = t.Add(time.Duration(amount) * time.Minute)
		case "SECOND":
			t = t.Add(time.Duration(amount) * time.Second)
		case "MONTH":
			t = t.AddDate(0, amount, 0)
		case "YEAR":
			t = t.AddDate(amount, 0, 0)
		case "WEEK":
			t = t.AddDate(0, 0, amount*7)
		default:
			return nil, fmt.Errorf("unknown interval unit: %s", unit)
		}
	case "-":
		switch unit {
		case "DAY":
			t = t.AddDate(0, 0, -amount)
		case "HOUR":
			t = t.Add(-time.Duration(amount) * time.Hour)
		case "MINUTE":
			t = t.Add(-time.Duration(amount) * time.Minute)
		case "SECOND":
			t = t.Add(-time.Duration(amount) * time.Second)
		case "MONTH":
			t = t.AddDate(0, -amount, 0)
		case "YEAR":
			t = t.AddDate(-amount, 0, 0)
		case "WEEK":
			t = t.AddDate(0, 0, -amount*7)
		default:
			return nil, fmt.Errorf("unknown interval unit: %s", unit)
		}
	}
	return t.Format(time.RFC3339), nil
}

// fnNow returns the current time.
func fnNow(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	return time.Now().UTC().Format(time.RFC3339), nil
}

// fnCurrentDate returns the current date.
func fnCurrentDate(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	return time.Now().UTC().Format("2006-01-02"), nil
}

// fnCurrentTime returns the current time.
func fnCurrentTime(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	return time.Now().UTC().Format("15:04:05"), nil
}

// fnCurrentTimestamp returns the current timestamp.
func fnCurrentTimestamp(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	return time.Now().UTC().Format(time.RFC3339), nil
}

// fnDateTrunc truncates timestamp to the specified part.
func fnDateTrunc(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("DATE_TRUNC requires 2 arguments")
	}
	part := strings.ToUpper(valueToString(args[0]))
	ts, err := parseTimestamp(valueToString(args[1]))
	if err != nil {
		return nil, fmt.Errorf("DATE_TRUNC: %w", err)
	}
	switch part {
	case "YEAR":
		return time.Date(ts.Year(), 1, 1, 0, 0, 0, 0, ts.Location()).Format(time.RFC3339), nil
	case "MONTH":
		return time.Date(ts.Year(), ts.Month(), 1, 0, 0, 0, 0, ts.Location()).Format(time.RFC3339), nil
	case "DAY":
		return time.Date(ts.Year(), ts.Month(), ts.Day(), 0, 0, 0, 0, ts.Location()).Format(time.RFC3339), nil
	case "HOUR":
		return time.Date(ts.Year(), ts.Month(), ts.Day(), ts.Hour(), 0, 0, 0, ts.Location()).Format(time.RFC3339), nil
	case "MINUTE":
		return time.Date(ts.Year(), ts.Month(), ts.Day(), ts.Hour(), ts.Minute(), 0, 0, ts.Location()).Format(time.RFC3339), nil
	case "SECOND":
		return time.Date(ts.Year(), ts.Month(), ts.Day(), ts.Hour(), ts.Minute(), ts.Second(), 0, ts.Location()).Format(time.RFC3339), nil
	default:
		return nil, fmt.Errorf("DATE_TRUNC: unknown part %q", part)
	}
}

// fnExtract extracts a part from a timestamp.
func fnExtract(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) < 1 || len(args) > 2 {
		return nil, fmt.Errorf("EXTRACT requires 1 or 2 arguments")
	}
	if len(args) == 1 {
		s := valueToString(args[0])
		ts, err := parseTimestamp(s)
		if err != nil {
			return nil, fmt.Errorf("EXTRACT: %w", err)
		}
		return int64(ts.Unix()), nil
	}
	field := strings.ToUpper(valueToString(args[0]))
	t, err := parseTimestamp(valueToString(args[1]))
	if err != nil {
		return nil, fmt.Errorf("EXTRACT: %w", err)
	}
	switch field {
	case "YEAR":
		return int64(t.Year()), nil
	case "MONTH":
		return int64(t.Month()), nil
	case "DAY":
		return int64(t.Day()), nil
	case "HOUR":
		return int64(t.Hour()), nil
	case "MINUTE":
		return int64(t.Minute()), nil
	case "SECOND":
		return int64(t.Second()), nil
	case "DOW":
		return int64(t.Weekday()), nil
	case "DOY":
		return int64(t.YearDay()), nil
	default:
		return nil, fmt.Errorf("EXTRACT: unknown field %q", field)
	}
}

// fnAge computes time difference.
func fnAge(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) < 1 || len(args) > 2 {
		return nil, fmt.Errorf("AGE requires 1 or 2 arguments")
	}
	ts, err := parseTimestamp(valueToString(args[0]))
	if err != nil {
		return nil, fmt.Errorf("AGE: %w", err)
	}
	var diff time.Duration
	if len(args) == 2 {
		ts2, err := parseTimestamp(valueToString(args[1]))
		if err != nil {
			return nil, fmt.Errorf("AGE: %w", err)
		}
		diff = ts.Sub(ts2)
	} else {
		diff = time.Since(ts)
	}
	days := int(diff.Hours() / 24)
	hours := int(math.Mod(diff.Hours(), 24))
	minutes := int(math.Mod(diff.Minutes(), 60))
	seconds := int(math.Mod(diff.Seconds(), 60))
	return fmt.Sprintf("%d days %d hours %d mins %d secs", days, hours, minutes, seconds), nil
}

// fnToDate converts string to date.
func fnToDate(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("TO_DATE requires 2 arguments")
	}
	s := valueToString(args[0])
	layout := valueToString(args[1])
	goLayout := sqlToGoLayout(layout)
	t, err := time.Parse(goLayout, s)
	if err != nil {
		return nil, fmt.Errorf("TO_DATE: %w", err)
	}
	return t.Format("2006-01-02"), nil
}

// fnToChar formats timestamp to string.
func fnToChar(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("TO_CHAR requires 2 arguments")
	}
	ts, err := parseTimestamp(valueToString(args[0]))
	if err != nil {
		return nil, fmt.Errorf("TO_CHAR: %w", err)
	}
	layout := valueToString(args[1])
	return ts.Format(layout), nil
}

// fnToTimestamp converts a string to a timestamp.
func fnToTimestamp(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("TO_TIMESTAMP requires 2 arguments")
	}
	s := valueToString(args[0])
	layout := valueToString(args[1])
	t, err := time.Parse(layout, s)
	if err != nil {
		return nil, fmt.Errorf("TO_TIMESTAMP: %w", err)
	}
	return t.Format(time.RFC3339), nil
}

// fnDateAdd adds an interval to a date.
func fnDateAdd(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("DATE_ADD requires 3 arguments: date, amount, unit")
	}
	dateStr := valueToString(args[0])
	t, err := parseTimestamp(dateStr)
	if err != nil {
		return nil, fmt.Errorf("DATE_ADD: %w", err)
	}
	amount, ok := toFloat(args[1])
	if !ok {
		return nil, fmt.Errorf("DATE_ADD: amount must be numeric")
	}
	unit := strings.ToUpper(valueToString(args[2]))
	switch unit {
	case "YEAR":
		t = t.AddDate(int(amount), 0, 0)
	case "MONTH":
		t = t.AddDate(0, int(amount), 0)
	case "DAY":
		t = t.AddDate(0, 0, int(amount))
	case "HOUR":
		t = t.Add(time.Duration(amount) * time.Hour)
	case "MINUTE":
		t = t.Add(time.Duration(amount) * time.Minute)
	case "SECOND":
		t = t.Add(time.Duration(amount) * time.Second)
	default:
		return nil, fmt.Errorf("DATE_ADD: unknown unit %q", unit)
	}
	return t.Format(time.RFC3339), nil
}

// fnDateSub subtracts an interval from a date.
func fnDateSub(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("DATE_SUB requires 3 arguments: date, amount, unit")
	}
	dateStr := valueToString(args[0])
	t, err := parseTimestamp(dateStr)
	if err != nil {
		return nil, fmt.Errorf("DATE_SUB: %w", err)
	}
	amount, ok := toFloat(args[1])
	if !ok {
		return nil, fmt.Errorf("DATE_SUB: amount must be numeric")
	}
	unit := strings.ToUpper(valueToString(args[2]))
	switch unit {
	case "YEAR":
		t = t.AddDate(-int(amount), 0, 0)
	case "MONTH":
		t = t.AddDate(0, -int(amount), 0)
	case "DAY":
		t = t.AddDate(0, 0, -int(amount))
	case "HOUR":
		t = t.Add(-time.Duration(amount) * time.Hour)
	case "MINUTE":
		t = t.Add(-time.Duration(amount) * time.Minute)
	case "SECOND":
		t = t.Add(-time.Duration(amount) * time.Second)
	default:
		return nil, fmt.Errorf("DATE_SUB: unknown unit %q", unit)
	}
	return t.Format(time.RFC3339), nil
}

// fnDateDiff computes difference between two dates.
func fnDateDiff(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("DATE_DIFF requires 3 arguments: unit, date1, date2")
	}
	unit := strings.ToUpper(valueToString(args[0]))
	t1, err := parseTimestamp(valueToString(args[1]))
	if err != nil {
		return nil, fmt.Errorf("DATE_DIFF: %w", err)
	}
	t2, err := parseTimestamp(valueToString(args[2]))
	if err != nil {
		return nil, fmt.Errorf("DATE_DIFF: %w", err)
	}
	diff := t2.Sub(t1)
	switch unit {
	case "DAY":
		return int64(diff.Hours() / 24), nil
	case "HOUR":
		return int64(diff.Hours()), nil
	case "MINUTE":
		return int64(diff.Minutes()), nil
	case "SECOND":
		return int64(diff.Seconds()), nil
	case "MONTH":
		months := int64((t2.Year()-t1.Year())*12 + int(t2.Month()) - int(t1.Month()))
		if t2.Day() < t1.Day() {
			months--
		}
		return months, nil
	case "YEAR":
		years := int64(t2.Year() - t1.Year())
		if t2.Month() < t1.Month() || (t2.Month() == t1.Month() && t2.Day() < t1.Day()) {
			years--
		}
		return years, nil
	default:
		return nil, fmt.Errorf("DATE_DIFF: unknown unit %q", unit)
	}
}
