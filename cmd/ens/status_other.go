//go:build !unix

package main

import (
	"fmt"
	"io"
)

func runStatus(io.Writer) error {
	return fmt.Errorf("ens status is not supported on this OS")
}
