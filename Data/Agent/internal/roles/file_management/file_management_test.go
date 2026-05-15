package filemanagement

import (
	"archive/zip"
	"context"
	"io"
	"mime"
	"mime/multipart"
	"os"
	"path/filepath"
	"testing"
)

func TestChildrenMarksDirectorySymlinkNonExpandable(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "folder")
	if err := os.Mkdir(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "nested.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "plain.txt"), []byte("world"), 0o644); err != nil {
		t.Fatal(err)
	}
	symlinkPath := filepath.Join(root, "folder-link")
	symlinkCreated := os.Symlink(folder, symlinkPath) == nil

	response := request(t, map[string]any{"action": "children", "path": root})
	entries := entriesByName(t, response)

	if entries["folder"]["has_children"] != true {
		t.Fatalf("folder has_children = %v, want true", entries["folder"]["has_children"])
	}
	if entries["plain.txt"]["has_children"] != false {
		t.Fatalf("plain file has_children = %v, want false", entries["plain.txt"]["has_children"])
	}
	if symlinkCreated && entries["folder-link"]["has_children"] != false {
		t.Fatalf("directory symlink has_children = %v, want false", entries["folder-link"]["has_children"])
	}
}

func TestReadWriteTextRoundTrip(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "script.ps1")
	if err := os.WriteFile(filePath, []byte("Write-Host 'hello'\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	readResponse := request(t, map[string]any{"action": "read_text", "path": filePath})
	if readResponse["content"] != "Write-Host 'hello'\r\n" {
		t.Fatalf("content = %#v", readResponse["content"])
	}
	if readResponse["line_ending"] != "crlf" {
		t.Fatalf("line_ending = %v, want crlf", readResponse["line_ending"])
	}

	writeResponse := request(t, map[string]any{
		"action":      "write_text",
		"path":        filePath,
		"content":     "Write-Host 'updated'\nWrite-Host 'done'\n",
		"encoding":    readResponse["encoding"],
		"line_ending": "lf",
	})
	if writeResponse["line_ending"] != "lf" {
		t.Fatalf("written line_ending = %v, want lf", writeResponse["line_ending"])
	}
	raw, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "Write-Host 'updated'\nWrite-Host 'done'\n" {
		t.Fatalf("raw content = %#v", string(raw))
	}
}

func TestReadTextRejectsBinary(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "payload.bin")
	if err := os.WriteFile(filePath, []byte{0, 1, 2, 3, 4}, 0o644); err != nil {
		t.Fatal(err)
	}

	response := requestRaw(t, map[string]any{"action": "read_text", "path": filePath})
	if response["ok"] != false || response["error"] != "binary_not_supported" {
		t.Fatalf("response = %#v", response)
	}
}

func TestUploadConflictsSupportsNestedRelativePaths(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(nested, "payload.txt")
	if err := os.WriteFile(existing, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}

	response := request(t, map[string]any{
		"action":      "upload_conflicts",
		"target_path": root,
		"items": []map[string]any{
			{"client_key": "nested/payload.txt", "name": "payload.txt", "relative_path": "nested/payload.txt", "size_bytes": 7},
			{"client_key": "missing.txt", "name": "missing.txt", "relative_path": "missing.txt", "size_bytes": 7},
		},
	})
	conflicts, ok := response["conflicts"].([]map[string]any)
	if !ok {
		t.Fatalf("conflicts type = %T", response["conflicts"])
	}
	if len(conflicts) != 1 {
		t.Fatalf("conflicts len = %d, want 1", len(conflicts))
	}
	if conflicts[0]["relative_path"] != "nested/payload.txt" {
		t.Fatalf("conflict = %#v", conflicts[0])
	}
}

func TestPasteCopyAndCut(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "payload.txt")
	if err := os.WriteFile(source, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	copyResponse := request(t, map[string]any{
		"action":           "paste",
		"operation":        "copy",
		"paths":            []map[string]any{{"path": source}},
		"destination_path": root,
	})
	pasted := copyResponse["pasted"].([]map[string]any)
	copiedPath := pasted[0]["path"].(string)
	if filepath.Base(copiedPath) != "payload - Copy.txt" {
		t.Fatalf("copied path = %s", copiedPath)
	}
	copiedRaw, err := os.ReadFile(copiedPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(copiedRaw) != "hello" {
		t.Fatalf("copied content = %#v", string(copiedRaw))
	}

	destination := filepath.Join(root, "destination")
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	cutResponse := request(t, map[string]any{
		"action":           "paste",
		"operation":        "cut",
		"paths":            []map[string]any{{"path": source}},
		"destination_path": destination,
	})
	moved := cutResponse["pasted"].([]map[string]any)
	movedPath := moved[0]["path"].(string)
	if movedPath != filepath.Join(destination, "payload.txt") {
		t.Fatalf("moved path = %s", movedPath)
	}
	if pathExists(source) {
		t.Fatalf("source still exists after cut")
	}
}

func TestZipSelectionArchivesDirectory(t *testing.T) {
	root := t.TempDir()
	sourceDir := filepath.Join(root, "bundle")
	if err := os.Mkdir(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "payload.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(root, "download.zip")

	size, err := zipSelection([]map[string]any{{"path": sourceDir, "name": "bundle"}}, archivePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if size <= 0 {
		t.Fatalf("zip size = %d, want positive", size)
	}
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for _, file := range reader.File {
		if file.Name == "bundle/payload.txt" {
			return
		}
	}
	t.Fatalf("bundle/payload.txt not found in archive")
}

func TestBuildMultipartArtifactBodyIncludesArtifactAndContentLength(t *testing.T) {
	root := t.TempDir()
	artifactPath := filepath.Join(root, "download.zip")
	if err := os.WriteFile(artifactPath, []byte("zip-payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := New(nil, "test-host")
	manager.tempRoot = root

	body, contentType, contentLength, cleanup, err := manager.buildMultipartArtifactBody(artifactPath, "download.zip", "application/zip")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if contentLength <= int64(len("zip-payload")) {
		t.Fatalf("content length = %d, want multipart length", contentLength)
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatal(err)
	}
	if mediaType != "multipart/form-data" {
		t.Fatalf("media type = %s", mediaType)
	}
	reader := multipart.NewReader(body, params["boundary"])
	form, err := reader.ReadForm(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	if form.Value["archive_name"][0] != "download.zip" {
		t.Fatalf("archive_name = %#v", form.Value["archive_name"])
	}
	if form.Value["mime_type"][0] != "application/zip" {
		t.Fatalf("mime_type = %#v", form.Value["mime_type"])
	}
	files := form.File["artifact"]
	if len(files) != 1 {
		t.Fatalf("artifact files = %d, want 1", len(files))
	}
	file, err := files[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(file)
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "zip-payload" {
		t.Fatalf("artifact content = %#v", string(raw))
	}
}

func request(t *testing.T, payload map[string]any) map[string]any {
	t.Helper()
	response := requestRaw(t, payload)
	if response["ok"] != true {
		t.Fatalf("request failed: %#v", response)
	}
	return response
}

func requestRaw(t *testing.T, payload map[string]any) map[string]any {
	t.Helper()
	manager := New(nil, "test-host")
	response, err := manager.HandleRequest(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	responseMap, ok := response.(map[string]any)
	if !ok {
		t.Fatalf("response type = %T", response)
	}
	return responseMap
}

func entriesByName(t *testing.T, response map[string]any) map[string]map[string]any {
	t.Helper()
	entries, ok := response["entries"].([]map[string]any)
	if !ok {
		t.Fatalf("entries type = %T", response["entries"])
	}
	out := map[string]map[string]any{}
	for _, entry := range entries {
		out[cleanText(entry["name"])] = entry
	}
	return out
}
