package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestBootstrapNativeHostPublishedCustody(t *testing.T) {
	for _, mode := range []string{"valid", "content", "mode", "replace", "parent", "authority", "cancel", "caller alias", "unrelated"} {
		t.Run(mode, func(t *testing.T) {
			root, read := bootstrapDockerFixture(t)
			expected, key := bootstrapHostFilesFixture(), bootstrapDockerKeyFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			lost := false
			check := func(context.Context) error {
				if lost {
					return errors.New("private")
				}
				return nil
			}
			if installBootstrapDockerRepositoryWith(ctx, root, expected, check, read, func(context.Context) ([]byte, error) { return key, nil }) != nil {
				t.Fatal("publish fixture")
			}
			files, _ := bootstrapDockerRepositoryFiles(expected, "noble", key)
			pinned, err := pinBootstrapPublishedHostFiles(ctx, root, files, check)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "content":
				bootstrapDockerWrite(t, root, files[0].path, "changed")
			case "mode":
				if root.Chmod(files[0].path, 0600) != nil {
					t.Fatal("chmod")
				}
			case "replace":
				// Keep the original inode alive so the filesystem cannot reuse it.
				if root.Rename(files[0].path, files[0].path+".previous") != nil {
					t.Fatal("rename")
				}
				bootstrapDockerWrite(t, root, files[0].path, string(files[0].data))
			case "parent":
				parent := filepath.Dir(files[0].path)
				if root.Rename(parent, parent+".previous") != nil || os.Mkdir(filepath.Join(root.Name(), parent), 0755) != nil {
					t.Fatal("replace directory")
				}
				bootstrapDockerWrite(t, root, files[0].path, string(files[0].data))
			case "authority":
				lost = true
			case "cancel":
				cancel()
			case "caller alias":
				files[0].data[0] ^= 1
			case "unrelated":
				bootstrapDockerWrite(t, root, "etc/unrelated", "outside fixed files")
			}
			err = pinned(ctx)
			valid := mode == "valid" || mode == "caller alias" || mode == "unrelated"
			if (err == nil) != valid || err != nil && err != errBootstrapSource {
				t.Fatal("publication custody lost", err)
			}
		})
	}
}
