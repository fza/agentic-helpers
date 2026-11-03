//go:build !darwin

package home

func excludedFromBackups(string) bool { return true }

func excludeFromBackups(string) error { return nil }
