package clusterbootstrap

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1" // Git SHA-1 object identity, not an authentication primitive.
	"crypto/sha256"
	"debug/buildinfo"
	"debug/elf"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Bundle is isolated scratch owned by this call, never an installed runtime.
// Caller must Close after SSH transfer. Paths remain private to trusted caller;
// mutation of scratch after verification invalidates its proof.
type Bundle struct {
	root     string
	identity identity
}

func (b *Bundle) ArchivePath() string { return filepath.Join(b.root, BundleName) }
func (b *Bundle) SourcePath() string  { return filepath.Join(b.root, "unpacked", "source") }
func (b *Bundle) ManagerPath() string { return filepath.Join(b.root, "unpacked", ManagerPath) }
func (b *Bundle) Close() error        { return os.RemoveAll(b.root) }

// ManagerProof exposes only public verified executable identity for fixed SSH
// transport. Receiver must recheck copied bytes immediately before execution.
func (b *Bundle) ManagerProof() (Expected, string, int64, error) {
	if b == nil || b.root == "" || !filepath.IsAbs(b.root) || !digestPattern.MatchString(b.identity.NodeManager.SHA256) {
		return Expected{}, "", 0, errors.New("verified node bootstrap bundle required")
	}
	i := b.identity
	return Expected{Repository: i.Repository, Release: i.Release, SourceSHA: i.SourceSHA, AllowQualification: strings.Contains(i.Release, "-rc.")}, i.NodeManager.SHA256, i.NodeManager.Size, nil
}

type sourceFile struct {
	mode int64
	sha  string
}

// Stage verifies the compressed hash before parsing, then scans every archive
// member before extraction. Only verified regular files/directories reach new
// mode0700 scratch. No source or node-manager code is executed. gitBin must be
// a trusted host Git executable; empty selects Git from the process PATH.
func Stage(ctx context.Context, parent string, m *Manifest, input io.Reader, gitBin string) (_ *Bundle, err error) {
	if m == nil || m.asset.Size < 1 || m.asset.Size > MaxBundleBytes || !digestPattern.MatchString(m.asset.SHA256) {
		return nil, errors.New("node bootstrap validated manifest required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := os.MkdirTemp(parent, "borealis-node-bootstrap-")
	if err != nil {
		return nil, errors.New("node bootstrap scratch unavailable")
	}
	b := &Bundle{root: root, identity: m.identity}
	defer func() {
		if err != nil {
			_ = b.Close()
		}
	}()
	archive, err := os.OpenFile(b.ArchivePath(), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return nil, errors.New("node bootstrap staging failed")
	}
	defer archive.Close()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(archive, hash), io.LimitReader(contextReader{ctx, input}, m.asset.Size+1))
	if err != nil || n != m.asset.Size || hex.EncodeToString(hash.Sum(nil)) != m.asset.SHA256 {
		return nil, errors.New("node bootstrap archive size or digest mismatch")
	}
	files, err := scanArchive(ctx, archive, m)
	if err != nil {
		return nil, err
	}
	if err := extractArchive(ctx, archive, filepath.Join(root, "unpacked")); err != nil {
		return nil, err
	}
	if err := verifySource(ctx, b.SourcePath(), m.identity, files, gitBin); err != nil {
		return nil, err
	}
	if err := verifyBinary(b.ManagerPath(), m.identity); err != nil {
		return nil, err
	}
	return b, ctx.Err()
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

// Walk checks gzip checksum, rejects concatenated streams/trailing data and
// bounds expansion including tar headers. tar.Reader alone stops before CRC.
func walkArchive(ctx context.Context, file *os.File, visit func(*tar.Header, io.Reader) error) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return errors.New("node bootstrap archive rewind failed")
	}
	compressed := bufio.NewReader(contextReader{ctx, file})
	z, err := gzip.NewReader(compressed)
	if err != nil {
		return errors.New("node bootstrap gzip invalid")
	}
	defer z.Close()
	z.Multistream(false)
	expanded := &io.LimitedReader{R: contextReader{ctx, z}, N: MaxExpandedBytes + (64 << 20) + 1}
	t := tar.NewReader(expanded)
	for count := 0; ; count++ {
		h, err := t.Next()
		if err == io.EOF {
			break
		}
		if err != nil || count >= MaxEntries {
			return errors.New("node bootstrap tar invalid or entry limit exceeded")
		}
		if err := visit(h, t); err != nil {
			return err
		}
	}
	// Python tarfile pads its final record with at most 10240 zero bytes.
	padding, err := io.ReadAll(io.LimitReader(expanded, 10241))
	if err != nil || len(padding) > 10240 || len(bytes.Trim(padding, "\x00")) != 0 || expanded.N <= 0 {
		return errors.New("node bootstrap gzip checksum or tar padding invalid")
	}
	if _, err := compressed.Peek(1); err != io.EOF {
		return errors.New("node bootstrap compressed trailing data invalid")
	}
	return nil
}

func scanArchive(ctx context.Context, file *os.File, m *Manifest) (map[string]sourceFile, error) {
	seen := map[string]bool{}
	files := map[string]sourceFile{}
	var total int64
	var inner []byte
	var managerSHA string
	err := walkArchive(ctx, file, func(h *tar.Header, r io.Reader) error {
		name, err := archiveName(h)
		if err != nil {
			return err
		}
		if _, exists := seen[name]; exists || (path.Dir(name) != "." && !seen[path.Dir(name)]) {
			return errors.New("node bootstrap duplicate member or missing parent")
		}
		seen[name] = h.Typeflag == tar.TypeDir
		total += h.Size
		if total > MaxExpandedBytes {
			return errors.New("node bootstrap expanded size limit exceeded")
		}
		if h.Typeflag == tar.TypeDir {
			return nil
		}
		switch name {
		case "identity.json":
			if h.Size > MaxManifestBytes || h.Mode != 0o644 {
				return errors.New("node bootstrap inner identity size or mode invalid")
			}
			inner, err = io.ReadAll(r)
		case ManagerPath:
			if h.Size != m.identity.NodeManager.Size || h.Mode != 0o755 {
				return errors.New("node bootstrap binary size or mode mismatch")
			}
			hash := sha256.New()
			_, err = io.Copy(hash, r)
			managerSHA = hex.EncodeToString(hash.Sum(nil))
		default:
			if !strings.HasPrefix(name, "source/.git/") {
				// Compute Git's blob ID directly: no content filters, hooks or
				// executable source. Compare every file to the exact commit tree.
				hash := sha1.New()
				_, _ = fmt.Fprintf(hash, "blob %d\x00", h.Size)
				_, err = io.Copy(hash, r)
				files[strings.TrimPrefix(name, "source/")] = sourceFile{h.Mode, hex.EncodeToString(hash.Sum(nil))}
			}
		}
		if err != nil {
			return errors.New("node bootstrap archive member truncated")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	i, err := parseIdentity(inner, Expected{Repository: m.identity.Repository, Release: m.identity.Release, SourceSHA: m.identity.SourceSHA, AllowQualification: true})
	if err != nil || i != m.identity || managerSHA != m.identity.NodeManager.SHA256 || len(files) == 0 {
		return nil, errors.New("node bootstrap inner identity or binary digest mismatch")
	}
	return files, nil
}

var packFilePattern = regexp.MustCompile(`^objects/pack/pack-[0-9a-f]{40}\.(?:pack|idx|rev)$`)

func archiveName(h *tar.Header) (string, error) {
	invalid := errors.New("node bootstrap archive path, type or metadata invalid")
	name := h.Name
	if h.Typeflag == tar.TypeDir {
		name = strings.TrimSuffix(name, "/")
	}
	if len(name) == 0 || len(name) > 1024 || !utf8.ValidString(name) || path.Clean(name) != name || path.IsAbs(name) || name == "." || name == ".." || strings.HasPrefix(name, "../") || strings.ContainsAny(name, "\\\x00\r\n\t") {
		return "", invalid
	}
	for _, c := range name {
		if c < 0x20 || c == 0x7f {
			return "", invalid
		}
	}
	if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir || h.Linkname != "" || h.Uid != 0 || h.Gid != 0 || h.Uname != "" || h.Gname != "" || h.Devmajor != 0 || h.Devminor != 0 || h.ModTime.Unix() != 0 || !h.AccessTime.IsZero() || !h.ChangeTime.IsZero() || len(h.Xattrs) != 0 || h.Size < 0 || h.Size > MaxBundleBytes {
		return "", invalid
	}
	if h.Typeflag == tar.TypeDir && (h.Mode != 0o755 || h.Size != 0) || h.Typeflag == tar.TypeReg && h.Mode != 0o644 && h.Mode != 0o755 {
		return "", invalid
	}
	for key, value := range h.PAXRecords {
		if key != "path" || value != h.Name {
			return "", invalid
		}
	}
	switch {
	case name == "bin" || name == "source":
		if h.Typeflag != tar.TypeDir {
			return "", invalid
		}
	case name == "identity.json" || name == ManagerPath:
		if h.Typeflag != tar.TypeReg {
			return "", invalid
		}
	case strings.HasPrefix(name, "source/"):
		relative := strings.TrimPrefix(name, "source/")
		if relative == ".git" || strings.HasPrefix(relative, ".git/") {
			if !allowedGitPath(strings.TrimPrefix(relative, ".git"), h.Typeflag == tar.TypeDir) {
				return "", invalid
			}
		} else {
			for _, part := range strings.Split(relative, "/") {
				if strings.EqualFold(part, ".git") {
					return "", invalid
				}
			}
		}
	default:
		return "", invalid
	}
	return name, nil
}

func allowedGitPath(name string, directory bool) bool {
	name = strings.TrimPrefix(name, "/")
	if directory {
		switch name {
		case "", "info", "objects", "objects/info", "objects/pack", "refs", "refs/heads", "refs/tags":
			return true
		}
		return false
	}
	switch name {
	case "HEAD", "config", "index", "shallow", "packed-refs", "info/refs", "objects/info/packs":
		return true
	}
	return packFilePattern.MatchString(name) || strings.HasPrefix(name, "refs/tags/") && releasePattern.MatchString(strings.TrimPrefix(name, "refs/tags/"))
}

func extractArchive(ctx context.Context, archive *os.File, destination string) error {
	if err := os.Mkdir(destination, 0o700); err != nil {
		return errors.New("node bootstrap extraction scratch unavailable")
	}
	return walkArchive(ctx, archive, func(h *tar.Header, r io.Reader) error {
		// Recheck names even though the private archive has already been scanned.
		name, err := archiveName(h)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, filepath.FromSlash(name))
		if h.Typeflag == tar.TypeDir {
			if err := os.Mkdir(target, 0o755); err != nil {
				return errors.New("node bootstrap directory extraction failed")
			}
			return nil
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return errors.New("node bootstrap file extraction failed")
		}
		_, copyErr := io.Copy(file, r)
		modeErr := file.Chmod(os.FileMode(h.Mode))
		closeErr := file.Close()
		if copyErr != nil || modeErr != nil || closeErr != nil {
			return errors.New("node bootstrap file extraction incomplete")
		}
		return nil
	})
}

func verifyBinary(name string, i identity) error {
	invalid := errors.New("node bootstrap executable platform or build identity invalid")
	f, err := elf.Open(name)
	if err != nil {
		return invalid
	}
	ok := f.Machine == elf.EM_X86_64 && f.Class == elf.ELFCLASS64 && f.Type == elf.ET_EXEC
	_ = f.Close()
	if !ok {
		return invalid
	}
	info, err := buildinfo.ReadFile(name)
	if err != nil || info.GoVersion != i.GoVersion || info.Path != "borealis/api-backend/cmd/borealis-node-manager" || info.Main.Path != "borealis/api-backend" {
		return invalid
	}
	settings := map[string]string{}
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	if settings["GOOS"] != "linux" || settings["GOARCH"] != "amd64" || settings["CGO_ENABLED"] != "0" {
		return invalid
	}
	return nil
}
