//go:build !unix

package main

import "io"

type nopLock struct{}

func (nopLock) Close() error { return nil }

func acquireInstanceLock() (io.Closer, error) {
	return nopLock{}, nil
}
