//go:build !linux

package clusterbootstrap

import "os"

// Acquisition is an Engine Linux operation; no sparse fallback elsewhere.
func reservePostgresFile(*os.File, int64) error { return ErrImageArchive }
