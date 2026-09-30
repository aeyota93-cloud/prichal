//go:build !linux

package main

func diskUsage(string) (uint64, uint64) { return 0, 0 }
