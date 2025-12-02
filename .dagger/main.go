package main

import (
	"context"
	"strings"

	"github.com/frantjc/go-ingress/.dagger/internal/dagger"
	xslices "github.com/frantjc/x/slices"
)

type GoIngressDev struct {
	Source *dagger.Directory
}

func New(
	ctx context.Context,
	// +optional
	// +defaultPath="."
	src *dagger.Directory,
) (*GoIngressDev, error) {
	return &GoIngressDev{
		Source: src,
	}, nil
}

func (m *GoIngressDev) Fmt(ctx context.Context) *dagger.Changeset {
	goModules := []string{
		".dagger/",
	}

	root := dag.Go(dagger.GoOpts{
		Module: m.Source.Filter(dagger.DirectoryFilterOpts{
			Exclude: goModules,
		}),
	}).
		Container().
		WithExec([]string{"go", "fmt", "./..."}).
		Directory(".")

	for _, module := range goModules {
		root = root.WithDirectory(
			module,
			dag.Go(dagger.GoOpts{
				Module: m.Source.Directory(module).Filter(dagger.DirectoryFilterOpts{
					Exclude: xslices.Filter(goModules, func(m string, _ int) bool {
						return strings.HasPrefix(m, module)
					}),
				}),
			}).
				Container().
				WithExec([]string{"go", "fmt", "./..."}).
				Directory("."),
		)
	}

	return root.Changes(m.Source)
}

func (m *GoIngressDev) Generate(ctx context.Context) *dagger.Changeset {
	return dag.Go(dagger.GoOpts{
		Module: m.Source,
	}).
		Container().
		WithExec([]string{
			"go", "install", "sigs.k8s.io/controller-tools/cmd/controller-gen@v0.19.0",
		}).
		WithExec([]string{
			// Order of the arguments doesn't seem to matter here. Can break this up into multiple execs if needed.
			"controller-gen",
			// generate CustomResourceDefinitions for types in api/** and put them in config/crd.
			"crd", "paths=./api/...", "output:crd:artifacts:config=config/crd",
			// generate [Validating|Mutating]WebhookConfigurations (none as of writing).
			"webhook",
			// generate api/**/zz_generated.deepcopy.go for types in api/**.
			"object",
			// generate ClusterRole for controllers in internal/controller/** and put it in config/rbac (default location).
			"rbac:roleName=go-ingress", "paths=./internal/controller/...",
		}).
		Directory(".").
		Changes(m.Source)
}

const (
	gid   = "1001"
	uid   = gid
	group = "manager"
	user  = group
	owner = user + ":" + group
	home  = "/home/" + user
)

func (m *GoIngressDev) Container(ctx context.Context) *dagger.Container {
	return dag.Wolfi().
		Container().
		WithExec([]string{"addgroup", "-S", "-g", gid, group}).
		WithExec([]string{"adduser", "-S", "-G", group, "-u", uid, user}).
		WithEnvVariable("PATH", home+"/.local/bin:$PATH", dagger.ContainerWithEnvVariableOpts{Expand: true}).
		WithFile(
			home+"/.local/bin/manager", m.Binary(ctx),
			dagger.ContainerWithFileOpts{Expand: true, Owner: owner, Permissions: 0700}).
		WithExec([]string{"chown", "-R", owner, home}).
		WithEntrypoint([]string{"manager"})
}

func (m *GoIngressDev) Version(ctx context.Context) string {
	version := "v0.0.0-unknown"

	ref, err := m.Source.AsGit().LatestVersion().Ref(ctx)
	if err == nil {
		version = strings.TrimPrefix(ref, "refs/tags/")
	}

	if empty, _ := m.Source.AsGit().Uncommitted().IsEmpty(ctx); !empty {
		version += "*"
	}

	return version
}

func (m *GoIngressDev) Tag(ctx context.Context) string {
	return strings.TrimSuffix(strings.TrimPrefix(m.Version(ctx), "v"), "*")
}

func (m *GoIngressDev) Binary(ctx context.Context) *dagger.File {
	return dag.Go(dagger.GoOpts{
		Module: m.Source.Filter(dagger.DirectoryFilterOpts{
			Exclude: []string{".github/"},
		}),
	}).
		Build(dagger.GoBuildOpts{
			Pkg:     "./cmd/manager",
			Ldflags: "-s -w -X main.version=" + m.Version(ctx),
		})
}
