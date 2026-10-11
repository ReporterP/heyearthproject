//go:build !linux

package main

import "errors"

func diskUsagePercent(path string) (float64, error) {
	return 0, errors.New("disk usage check is supported on linux only")
}
