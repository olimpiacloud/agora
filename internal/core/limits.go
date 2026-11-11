package core

import "fmt"

const (
	maxNameBytes    = 200
	maxDetailRunes  = 2000
	maxGoalBytes    = 64 << 10
	maxResultBytes  = 256 << 10
	maxMessageBytes = 64 << 10
	maxLabels       = 32
	maxLabelKey     = 64
	maxLabelValue   = 256
	eventGoalRunes  = 500
)

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	i := 0
	for j := range s {
		if i == n {
			return s[:j]
		}
		i++
	}
	return s
}

func checkLabels(l map[string]string) error {
	if len(l) > maxLabels {
		return fmt.Errorf("labels are limited to %d entries", maxLabels)
	}
	for k, v := range l {
		if len(k) > maxLabelKey || len(v) > maxLabelValue {
			return fmt.Errorf("label keys are limited to %d bytes and values to %d", maxLabelKey, maxLabelValue)
		}
	}
	return nil
}
