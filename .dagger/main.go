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
			// generate ClusterRole for controllers in internal/** and cmd/** and put it in config/rbac (default location).
			"rbac:roleName=go-ingress", "paths=./cmd/...", "paths=./internal/...",
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

	gitRef := m.Source.AsGit().LatestVersion()

	if ref, err := gitRef.Ref(ctx); err == nil {
		version = strings.TrimPrefix(ref, "refs/tags/")
	}

	if latestVersionCommit, err := gitRef.Commit(ctx); err == nil {
		if headCommit, err := m.Source.AsGit().Head().Commit(ctx); err == nil {
			if headCommit != latestVersionCommit {
				if len(headCommit) > 7 {
					headCommit = headCommit[:7]
				}
				version += "-" + headCommit
			}
		}
	}

	if empty, _ := m.Source.AsGit().Uncommitted().IsEmpty(ctx); !empty {
		version += "+dirty"
	}

	return version
}

func (m *GoIngressDev) Tag(ctx context.Context) string {
	before, _, _ := strings.Cut(strings.TrimPrefix(m.Version(ctx), "v"), "+")
	return before
}

func (m *GoIngressDev) Binary(ctx context.Context) *dagger.File {
	return dag.Go(dagger.GoOpts{
		Module: m.Source.Filter(dagger.DirectoryFilterOpts{
			Exclude: []string{
				".dagger/",
				".githooks/",
				".github/",
			},
		}),
	}).
		Build(dagger.GoBuildOpts{
			Pkg:     "./cmd/manager",
			Ldflags: "-s -w -X main.version=" + m.Version(ctx),
		})
}

func (m *GoIngressDev) Test(ctx context.Context) (string, error) {
	return dag.Go(dagger.GoOpts{
		Module: m.Source.Filter(dagger.DirectoryFilterOpts{
			Exclude: []string{
				".dagger/",
				".githooks/",
				".github/",
			},
		}),
	}).
		Container().
		WithExec([]string{"go", "test", "-cover", "-race", "./..."}).
		CombinedOutput(ctx)
}

func (m *GoIngressDev) Vulncheck(ctx context.Context) (string, error) {
	return dag.Go(dagger.GoOpts{
		Module: m.Source.Filter(dagger.DirectoryFilterOpts{
			Exclude: []string{
				".dagger/",
				".githooks/",
				".github/",
			},
		}),
	}).
		Container().
		WithExec([]string{"go", "install", "golang.org/x/vuln/cmd/govulncheck@v1.1.4"}).
		WithExec([]string{"govulncheck", "./..."}).
		CombinedOutput(ctx)
}

func (m *GoIngressDev) Vet(ctx context.Context) (string, error) {
	return dag.Go(dagger.GoOpts{
		Module: m.Source.Filter(dagger.DirectoryFilterOpts{
			Exclude: []string{
				".dagger/",
				".githooks/",
				".github/",
			},
		}),
	}).
		Container().
		WithExec([]string{"go", "vet", "./..."}).
		CombinedOutput(ctx)
}

func (m *GoIngressDev) Staticcheck(ctx context.Context) (string, error) {
	return dag.Go(dagger.GoOpts{
		Module: m.Source.Filter(dagger.DirectoryFilterOpts{
			Exclude: []string{
				".dagger/",
				".githooks/",
				".github/",
			},
		}),
	}).
		Container().
		WithExec([]string{"go", "install", "honnef.co/go/tools/cmd/staticcheck@v0.6.1"}).
		WithExec([]string{"staticcheck", "./..."}).
		CombinedOutput(ctx)
}

func (m *GoIngressDev) Coder(ctx context.Context) (*dagger.LLM, error) {
	gopls := dag.Go(dagger.GoOpts{
		Module: m.Source.Filter(dagger.DirectoryFilterOpts{
			Exclude: []string{
				".dagger/",
				".githooks/",
				".github/",
			},
		}),
	}).
		Container().
		WithExec([]string{"go", "install", "golang.org/x/tools/gopls@v0.20.0"})

	instructions, err := gopls.WithExec([]string{"gopls", "mcp", "-instructions"}).Stdout(ctx)
	if err != nil {
		return nil, err
	}

	return dag.Doug().
		Agent(
			dag.LLM().
				WithEnv(
					dag.Env().
						// WithCurrentModule().
						WithWorkspace(
							m.Source.Filter(dagger.DirectoryFilterOpts{
								Exclude: []string{
									".dagger/",
									".githooks/",
									".github/",
								},
							}),
						),
				).
				// WithBlockedFunction("GoIngressDev", "container").
				// WithBlockedFunction("GoIngressDev", "tag").
				// WithBlockedFunction("GoIngressDev", "version").
				WithSystemPrompt(instructions).
				WithMCPServer(
					"gopls",
					gopls.AsService(dagger.ContainerAsServiceOpts{
						Args: []string{"gopls", "mcp"},
					}),
				),
		), nil
}
