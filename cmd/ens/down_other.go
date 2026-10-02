//go:build !unix

package main

import "fmt"

func runDown() error {
	return fmt.Errorf("ens down is not supported on this OS")
}
