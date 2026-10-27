package clusterbootstrap

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func artifactTargetsFixture(t *testing.T) (prejoinArtifactTargets, prejoinArtifactPlan, string) {
	t.Helper()
	a, _, _ := preparationArtifactsFixture(t)
	a.mu.Lock()
	plan, err := a.publicationPlan(context.Background(), func(context.Context) error { return nil })
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	system, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { system.Close() })
	source, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { source.Close() })
	for _, path := range []string{"var/lib", "usr/local/bin"} {
		if system.MkdirAll(path, 0700) != nil {
			t.Fatal("system fixture")
		}
	}
	if source.MkdirAll("Engine/Deploy", 0700) != nil {
		t.Fatal("source fixture")
	}
	return prejoinArtifactTargets{system, source}, plan, mutationDigest("complete fixture intent")
}

func TestPreparationArtifactTargets(t *testing.T) {
	ctx := context.Background()
	check := func(context.Context) error { return nil }
	t.Run("complete", func(t *testing.T) {
		targets, plan, input := artifactTargetsFixture(t)
		if targets.system.Mkdir("var/lib/rancher", 0750) != nil {
			t.Fatal("existing rancher")
		}
		before, _ := targets.system.Stat("var/lib/rancher")
		if err := targets.publish(ctx, plan, input, check); err != nil {
			t.Fatal(err)
		}
		after, _ := targets.system.Stat("var/lib/rancher")
		if !os.SameFile(before, after) || before.Mode() != after.Mode() {
			t.Fatal("existing parent changed")
		}
		if targets.prove(ctx, plan, input, check) != nil {
			t.Fatal("complete proof failed")
		}
		if targets.fresh(ctx, plan, input, check) == nil || targets.publish(ctx, plan, input, check) == nil {
			t.Fatal("existing effects admitted fresh")
		}
		for _, file := range plan.files {
			root, path := targets.destination(file)
			if provePrejoinArtifactFile(ctx, root, path, file) != nil {
				t.Fatal("missing complete artifact", path)
			}
		}
	})
	for _, mode := range []string{"existing tree", "existing binary", "source collision", "foreign scratch", "source scratch", "unsafe parent", "lost authority"} {
		t.Run("fresh/"+mode, func(t *testing.T) {
			targets, plan, input := artifactTargetsFixture(t)
			current := check
			switch mode {
			case "existing tree":
				targets.system.MkdirAll("var/lib/rancher/k3s", 0700)
			case "existing binary":
				targets.system.WriteFile("usr/local/bin/k3s", []byte("foreign"), 0755)
			case "source collision":
				targets.source.WriteFile("Engine/Deploy/k3s-install.sh", []byte("foreign"), 0600)
			case "foreign scratch":
				targets.system.WriteFile("usr/local/bin/.borealis-artifact-foreign", nil, 0600)
			case "source scratch":
				targets.source.WriteFile("Engine/Deploy/.borealis-artifact-foreign", nil, 0600)
			case "unsafe parent":
				targets.system.Chmod("var/lib", 0777)
			case "lost authority":
				current = func(context.Context) error { return ErrSessionAuthority }
			}
			if targets.fresh(ctx, plan, input, current) == nil || targets.publish(ctx, plan, input, current) == nil {
				t.Fatal("unsafe fresh state accepted")
			}
			if mode != "existing tree" && !artifactPathAbsent(targets.system, "var/lib/rancher/k3s") {
				t.Fatal("rejected admission created runtime paths")
			}
		})
	}
	for _, mode := range []string{"extra archive", "extra token", "changed archive", "unsafe binary", "extra scratch", "missing installer", "lost authority"} {
		t.Run("proof/"+mode, func(t *testing.T) {
			targets, plan, input := artifactTargetsFixture(t)
			if err := targets.publish(ctx, plan, input, check); err != nil {
				t.Fatal(err)
			}
			current := check
			switch mode {
			case "extra archive":
				targets.system.WriteFile("var/lib/rancher/k3s/agent/images/foreign.tar", nil, 0600)
			case "extra token":
				targets.system.WriteFile("var/lib/rancher/k3s/token", nil, 0600)
			case "changed archive":
				targets.system.WriteFile(plan.files[0].path, []byte("changed"), 0600)
			case "unsafe binary":
				targets.system.Chmod("usr/local/bin/k3s", 0777)
			case "extra scratch":
				targets.source.WriteFile("Engine/Deploy/.borealis-artifact-foreign", nil, 0600)
			case "missing installer":
				targets.source.Remove("Engine/Deploy/k3s-install.sh")
			case "lost authority":
				current = func(context.Context) error { return ErrSessionAuthority }
			}
			if targets.prove(ctx, plan, input, current) == nil {
				t.Fatal("incomplete or unsafe state proved complete")
			}
		})
	}
	t.Run("interrupted bytes retained fenced", func(t *testing.T) {
		targets, plan, input := artifactTargetsFixture(t)
		file := &plan.files[0]
		file.write = func(_ context.Context, out io.Writer, _ func(context.Context) error) error {
			out.Write([]byte("partial"))
			return ErrSessionAuthority
		}
		if targets.publish(ctx, plan, input, check) == nil {
			t.Fatal("interruption accepted")
		}
		pending := filepath.Join(filepath.Dir(file.path), prejoinArtifactPending(input, file.path))
		if _, err := targets.system.Lstat(pending); err != nil {
			t.Fatal("interruption evidence removed", err)
		}
		if !artifactPathAbsent(targets.system, file.path) || targets.prove(ctx, plan, input, check) == nil || targets.fresh(ctx, plan, input, check) == nil {
			t.Fatal("partial effects admitted")
		}
	})
}
