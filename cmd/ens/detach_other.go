//go:build !unix

package main

func isWorker() bool { return false }

func maybeSupervise(run func() error) error { return run() }

func notifyConnected() {}

func redirectLogs() {}
