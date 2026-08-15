//go:build windows

package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func downloadFileLogged(ctx context.Context, rawURL string, destination string, timeout time.Duration, logger *BootstrapLogger) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	startedAt := time.Now()
	if logger != nil {
		logger.Tracef("Download start: url=%s destination=%s timeout=%s", rawURL, destination, timeout)
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download %s returned HTTP %d", rawURL, resp.StatusCode)
	}
	tmp := destination + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, resp.Body)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	_ = os.Remove(destination)
	if err := os.Rename(tmp, destination); err != nil {
		return err
	}
	if logger != nil {
		info, _ := os.Stat(destination)
		size := int64(-1)
		if info != nil {
			size = info.Size()
		}
		logger.Tracef("Download complete: destination=%s bytes=%d duration=%s", destination, size, time.Since(startedAt).Round(time.Millisecond))
	}
	return nil
}

func unzipFileLogged(archivePath string, destination string, logger *BootstrapLogger) error {
	startedAt := time.Now()
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer reader.Close()
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	files := 0
	dirs := 0
	cleanDestination := filepath.Clean(destination)
	for _, item := range reader.File {
		target := filepath.Clean(filepath.Join(destination, item.Name))
		if !strings.HasPrefix(strings.ToLower(target), strings.ToLower(cleanDestination)+strings.ToLower(string(os.PathSeparator))) && !strings.EqualFold(target, cleanDestination) {
			return fmt.Errorf("zip entry escapes destination: %s", item.Name)
		}
		if item.FileInfo().IsDir() {
			dirs++
			if err := os.MkdirAll(target, item.Mode()); err != nil {
				return err
			}
			continue
		}
		files++
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := item.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, item.Mode())
		if err != nil {
			_ = in.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeInErr := in.Close()
		closeOutErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeInErr != nil {
			return closeInErr
		}
		if closeOutErr != nil {
			return closeOutErr
		}
	}
	if logger != nil {
		logger.Tracef("Unzip complete: archive=%s files=%d dirs=%d duration=%s", archivePath, files, dirs, time.Since(startedAt).Round(time.Millisecond))
	}
	return nil
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func copyFile(source string, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := destination + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	_ = os.Remove(destination)
	if err := os.Rename(tmp, destination); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
