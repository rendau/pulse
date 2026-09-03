// Package window — разбор параметра window (Go duration) с дефолтом и потолком.
package window

import (
	"fmt"
	"strings"
	"time"
)

const (
	Default = time.Hour
	Max     = 7 * 24 * time.Hour
)

// Parse разбирает «15m», «2h», «24h», «7d». Пустая строка — дефолт; больше потолка — ошибка.
func Parse(s string, def, maxWindow time.Duration) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return def, nil
	}

	// time.ParseDuration не знает суток
	if days, ok := strings.CutSuffix(s, "d"); ok && !strings.ContainsAny(days, "hms") {
		var n float64
		if _, err := fmt.Sscanf(days, "%g", &n); err != nil || n <= 0 {
			return 0, fmt.Errorf("window %q: expected Go duration like 15m, 2h, 24h, 7d", s)
		}
		d := time.Duration(n * float64(24*time.Hour))
		if d > maxWindow {
			return 0, fmt.Errorf("window %q exceeds maximum %s", s, maxWindow)
		}
		return d, nil
	}

	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("window %q: expected Go duration like 15m, 2h, 24h, 7d", s)
	}
	if d > maxWindow {
		return 0, fmt.Errorf("window %q exceeds maximum %s", s, maxWindow)
	}
	return d, nil
}

// Format печатает окно компактно: 15m, 2h, 24h, 7d (вместо 24h0m0s).
func Format(d time.Duration) string {
	switch {
	case d >= 48*time.Hour && d%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	case d >= time.Hour && d%time.Hour == 0:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	case d >= time.Minute && d%time.Minute == 0:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	default:
		return d.String()
	}
}
