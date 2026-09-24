package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
	"time"
)

// Internal release-packaging command; read-only, no daemon action/host import.
func imageArchiveProof(args []string) error {
	flags := flag.NewFlagSet("image-archive-proof", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	archive := flags.String("archive", "", "")
	role := flags.String("role", "", "")
	sha := flags.String("source-sha", "", "")
	if flags.Parse(args) != nil || flags.NArg() != 0 || len(*archive) == 0 || len(*archive) > 4096 {
		return clusterbootstrap.ErrImageArchive
	}
	before, err := os.Lstat(*archive)
	if err != nil || !before.Mode().IsRegular() {
		return clusterbootstrap.ErrImageArchive
	}
	f, err := os.Open(*archive)
	if err != nil {
		return clusterbootstrap.ErrImageArchive
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) || before.Size() != opened.Size() || before.ModTime() != opened.ModTime() {
		return clusterbootstrap.ErrImageArchive
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	proof, err := clusterbootstrap.InspectImageArchive(ctx, f, opened.Size(), *role, *sha)
	if err != nil {
		return err
	}
	after, err := f.Stat()
	if err != nil || after.Size() != opened.Size() || after.ModTime() != opened.ModTime() {
		return clusterbootstrap.ErrImageArchive
	}
	current, err := os.Lstat(*archive)
	if err != nil || !os.SameFile(opened, current) || !current.Mode().IsRegular() {
		return clusterbootstrap.ErrImageArchive
	}
	return json.NewEncoder(os.Stdout).Encode(proof)
}
