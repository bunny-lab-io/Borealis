package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"io"
	"os"
	"slices"
	"sync"
	"syscall"
)

// Private synchronous source capability, not a generic node-manager action.
// Caller must prove the selected original source/controller and live claim;
// this function adds exact root-file custody. It cannot choose another path,
// rotate a token, create a credential or leave material alive after consumption.
func withSourceK3sJoinMaterial(ctx context.Context, expected clusterbootstrap.PreparationExpected, source engineidentity.Binding,
	proveSource func(context.Context) error, consume func(context.Context, *clusterbootstrap.K3sJoinMaterial, func(context.Context) error) error) error {
	if os.Geteuid() != 0 || os.Getegid() != 0 {
		return clusterbootstrap.ErrK3sJoinMaterial
	}
	root, err := os.OpenRoot("/")
	if err != nil {
		return clusterbootstrap.ErrK3sJoinMaterial
	}
	defer root.Close()
	return withSourceK3sJoinMaterialAt(ctx, root, expected, source, proveSource, consume)
}

func withSourceK3sJoinMaterialAt(ctx context.Context, root *os.Root, expected clusterbootstrap.PreparationExpected, source engineidentity.Binding,
	proveSource func(context.Context) error, consume func(context.Context, *clusterbootstrap.K3sJoinMaterial, func(context.Context) error) error) error {
	fail := clusterbootstrap.ErrK3sJoinMaterial
	if root == nil || clusterbootstrap.ValidateK3sJoinBinding(expected, source) != nil || proveSource == nil || consume == nil {
		return fail
	}
	expected.PeerAddresses = slices.Clone(expected.PeerAddresses)
	boundary := func(ctx context.Context) error {
		if ctx.Err() != nil || proveSource(ctx) != nil || ctx.Err() != nil {
			return fail
		}
		return nil
	}
	if boundary(ctx) != nil {
		return fail
	}
	files := []bootstrapHostFile{
		{path: "var/lib/rancher/k3s/server/token", mode: 0600},
		{path: "var/lib/rancher/k3s/server/tls/server-ca.crt", mode: 0644},
	}
	ancestors, err := bootstrapHostFileAncestors(root, files)
	if err != nil {
		return fail
	}
	var scope sync.Mutex
	closed := false
	originals := make([]os.FileInfo, len(files))
	defer func() {
		scope.Lock()
		defer scope.Unlock()
		closed = true
		for _, file := range files {
			clear(file.data)
		}
	}()
	for i := range files {
		maximum := int64(clusterbootstrap.MaxK3sJoinTokenBytes + 1)
		if i == 1 {
			maximum = clusterbootstrap.MaxK3sJoinCABytes
		}
		file, err := root.OpenFile(files[i].path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return fail
		}
		info, statErr := file.Stat()
		if statErr != nil || !info.Mode().IsRegular() || info.Mode() != files[i].mode || info.Size() < 1 || info.Size() > maximum {
			file.Close()
			return fail
		}
		originals[i] = info
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || int(owner.Uid) != os.Geteuid() || int(owner.Gid) != os.Getegid() || owner.Nlink != 1 {
			file.Close()
			return fail
		}
		raw, readErr := io.ReadAll(io.LimitReader(file, maximum+1))
		closeErr := file.Close()
		files[i].data = raw
		if readErr != nil || closeErr != nil || int64(len(raw)) != info.Size() || proveBootstrapHostFile(root, files[i]) != nil || boundary(ctx) != nil {
			return fail
		}
	}
	refresh := func(ctx context.Context) error {
		scope.Lock()
		defer scope.Unlock()
		if closed || boundary(ctx) != nil {
			return fail
		}
		current, err := bootstrapHostFileAncestors(root, files)
		if err != nil || len(current) != len(ancestors) {
			return fail
		}
		for path, identity := range ancestors {
			if current[path] != identity {
				return fail
			}
		}
		for i, file := range files {
			current, err := root.Lstat(file.path)
			if err != nil || !os.SameFile(originals[i], current) || !originals[i].ModTime().Equal(current.ModTime()) || proveBootstrapHostFile(root, file) != nil || boundary(ctx) != nil {
				return fail
			}
		}
		return nil
	}
	if refresh(ctx) != nil {
		return fail
	}
	token := bytes.TrimSuffix(files[0].data, []byte{'\n'})
	material, err := clusterbootstrap.NewK3sJoinMaterial(expected, source, token, files[1].data)
	if err != nil {
		return fail
	}
	defer material.Close()
	if refresh(ctx) != nil || consume(ctx, material, refresh) != nil || refresh(ctx) != nil {
		return fail
	}
	return nil
}
