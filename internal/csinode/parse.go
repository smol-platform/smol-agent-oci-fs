package csinode

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func parseOptionalDuration(input string) (time.Duration, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return 0, nil
	}
	return time.ParseDuration(input)
}

func parseByteSize(input string) (int64, error) {
	input = strings.TrimSpace(input)
	if input == "" || input == "0" {
		return 0, nil
	}
	multiplier := int64(1)
	for suffix, value := range map[string]int64{
		"KiB": 1024,
		"MiB": 1024 * 1024,
		"GiB": 1024 * 1024 * 1024,
		"KB":  1000,
		"MB":  1000 * 1000,
		"GB":  1000 * 1000 * 1000,
	} {
		if strings.HasSuffix(input, suffix) {
			multiplier = value
			input = strings.TrimSuffix(input, suffix)
			break
		}
	}
	value, err := strconv.ParseInt(input, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid byte size %q", input)
	}
	return value * multiplier, nil
}
