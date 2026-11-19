package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"

	"github.com/haiven-dev/haiven-cli/internal/demo"
	"github.com/haiven-dev/haiven-cli/internal/pin"
	"github.com/haiven-dev/haiven-cli/internal/starter"
	"github.com/haiven-dev/haiven-cli/internal/version"
)

// CacheDirEnv overrides the per-user cache directory pinned releases are
// stored in.
const CacheDirEnv = "HAIVEN_CACHE_DIR"

// DownloadBaseEnv overrides where pinned releases are downloaded from (a
// mirror, or a local server in tests).
const DownloadBaseEnv = "HAIVEN_DOWNLOAD_BASE"

// StarterRunnerDirEnv selects a release-owned directory containing optional,
// content-addressed starter runner artifacts.
const StarterRunnerDirEnv = "HAIVEN_STARTER_RUNNER_DIR"

// Production wires the real process, filesystem and network.
func Production(cwd string) Env {
	runner := starter.ExecutableRunner{Directory: os.Getenv(StarterRunnerDirEnv)}
	demoRunner := demo.Runner{}
	e := Env{
		Stdin:                  os.Stdin,
		Stdout:                 os.Stdout,
		Stderr:                 os.Stderr,
		Getenv:                 os.Getenv,
		Cwd:                    cwd,
		Version:                version.Version,
		GOOS:                   runtime.GOOS,
		GOARCH:                 runtime.GOARCH,
		Reexec:                 pin.Exec,
		StarterCatalog:         starter.EmbeddedSource{},
		StarterRunner:          runner,
		StarterLifecycleRunner: runner,
		GitDirty:               productionGitDirty,
		DemoOpener:             demoRunner,
		DemoBuilder:            demoRunner,
		DemoRegistrar:          demoRunner,
	}
	cacheDir := os.Getenv(CacheDirEnv)
	var cacheErr error
	if cacheDir == "" {
		cacheDir, cacheErr = pin.DefaultCacheDir()
	}
	if cacheDir != "" {
		e.Pin = &pin.Resolver{
			Cache:   pin.Cache{Dir: cacheDir},
			Fetcher: pin.ReleaseFetcher{Base: os.Getenv(DownloadBaseEnv)},
			GOOS:    e.GOOS,
			GOARCH:  e.GOARCH,
			Running: e.Version,
		}
	} else {
		e.pinCacheErr = cacheErr
	}
	return e
}

// errProjectNotVersionControlled means git status found no repository.
// Preview may continue. Apply must not: there is no clean worktree to
// recover from, and this release does not take a backup.
var errProjectNotVersionControlled = errors.New("project is not a git repository")

func productionGitDirty(ctx context.Context, path string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", path, "status", "--porcelain", "--untracked-files=normal")
	out, err := cmd.Output()
	if err != nil {
		// A missing worktree is not a clean tree. Other probe failures stay
		// errors so a broken git is not reported as clean.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && bytes.Contains(exitErr.Stderr, []byte("not a git repository")) {
			return false, errProjectNotVersionControlled
		}
		return false, err
	}
	return len(bytes.TrimSpace(out)) > 0, nil
}
