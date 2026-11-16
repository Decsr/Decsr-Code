//go:build windows

package main

func execProcess(string, []string) error { return errTmuxWindows }
