package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/haiven-dev/haiven-cli/internal/result"
	"github.com/haiven-dev/haiven-cli/internal/version"
)

func TestProductionGitDirtyOutsideWorktreeIsNotVersionControlled(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", root)
	project := filepath.Join(root, "game")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	dirty, err := productionGitDirty(context.Background(), project)
	if !errors.Is(err, errProjectNotVersionControlled) || dirty {
		t.Fatalf("non-repo dirty=%v err=%v", dirty, err)
	}
	if _, err := productionGitDirty(context.Background(), filepath.Join(root, "missing")); err == nil {
		t.Fatal("missing path was treated as clean")
	}
}

func TestProductionGitDirtySeesWorktreeStatus(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", root)
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_CEILING_DIRECTORIES="+root,
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init")
	if err := os.WriteFile(filepath.Join(repo, "keep.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "keep.txt")
	git("commit", "-m", "init")
	dirty, err := productionGitDirty(context.Background(), repo)
	if err != nil || dirty {
		t.Fatalf("clean repo dirty=%v err=%v", dirty, err)
	}
	if err := os.WriteFile(filepath.Join(repo, "extra.txt"), []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, err = productionGitDirty(context.Background(), repo)
	if err != nil || !dirty {
		t.Fatalf("dirty repo dirty=%v err=%v", dirty, err)
	}
}

func TestStarterIntegratePreviewAllowsProjectOutsideGit(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", root)
	project := filepath.Join(root, "game")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "project.txt"), []byte("owned\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	exit := Run(context.Background(), Env{
		Stdin:         bytes.NewReader(nil),
		Stdout:        &stdout,
		Stderr:        &stderr,
		Getenv:        func(string) string { return "" },
		Cwd:           root,
		Version:       testVersion,
		GOOS:          "linux",
		GOARCH:        "amd64",
		StarterRunner: &fakeStarterRunner{},
		GitDirty:      productionGitDirty,
	}, []string{
		"sdk", "starter", "integrate", "unity-existing",
		"--project", project,
		"--param", "sdk_revision=28193c29b4115fdf37bd431e027400f56e340629",
		"--preview", "--json",
	})
	got := ran{exit: exit, stdout: stdout.String(), stderr: stderr.String()}
	envelope := decodeStarterEnvelope(t, got)
	if got.exit != int(result.ExitEnvironment) || len(envelope.Diagnostics) == 0 || envelope.Diagnostics[0].Code != result.DiagStarterManualRequired {
		t.Fatalf("exit=%d stderr=%s stdout=%s", got.exit, got.stderr, got.stdout)
	}
}

func TestStarterIntegrateApplyRefusesProjectOutsideGit(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", root)
	project := filepath.Join(root, "game")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	const owned = "owned\n"
	if err := os.WriteFile(filepath.Join(project, "project.txt"), []byte(owned), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &fakeStarterRunner{automated: true}
	var stdout, stderr bytes.Buffer
	exit := Run(context.Background(), Env{
		Stdout:        &stdout,
		Stderr:        &stderr,
		Getenv:        func(string) string { return "" },
		Cwd:           root,
		Version:       testVersion,
		GOOS:          "linux",
		GOARCH:        "amd64",
		StarterRunner: runner,
		GitDirty:      productionGitDirty,
	}, []string{
		"sdk", "starter", "integrate", "unity-existing",
		"--project", project,
		"--param", "sdk_revision=28193c29b4115fdf37bd431e027400f56e340629",
		"--yes", "--json",
	})
	got := ran{exit: exit, stdout: stdout.String(), stderr: stderr.String()}
	envelope := decodeStarterEnvelope(t, got)
	if got.exit != int(result.ExitUsage) || len(envelope.Diagnostics) == 0 || envelope.Diagnostics[0].Code != result.DiagStarterUnversionedProject {
		t.Fatalf("exit=%d stderr=%s stdout=%s", got.exit, got.stderr, got.stdout)
	}
	if runner.calls != 0 {
		t.Fatalf("unversioned apply called the runner %d times", runner.calls)
	}
	body, err := os.ReadFile(filepath.Join(project, "project.txt"))
	if err != nil || string(body) != owned {
		t.Fatalf("project changed: %q %v", body, err)
	}
}

func TestReleasePinWithoutUserCacheFailsClosed(t *testing.T) {
	t.Setenv("HAIVEN_CACHE_DIR", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("LOCALAPPDATA", "")
	root := t.TempDir()
	writePin := func(version string) {
		t.Helper()
		body := "[haiven]\nversion = \"" + version + "\"\n"
		if err := os.WriteFile(filepath.Join(root, "haiven.toml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(binary string) ran {
		t.Helper()
		previous := version.Version
		version.Version = binary
		t.Cleanup(func() { version.Version = previous })
		e := Production(root)
		var stdout, stderr bytes.Buffer
		e.Stdout = &stdout
		e.Stderr = &stderr
		exit := Run(context.Background(), e, []string{"capabilities", "--json"})
		return ran{exit: exit, stdout: stdout.String(), stderr: stderr.String()}
	}
	writePin("9.9.9")
	previous := version.Version
	version.Version = "1.2.3"
	t.Cleanup(func() { version.Version = previous })
	if wired := Production(root); wired.Pin != nil || wired.pinCacheErr == nil {
		t.Fatalf("user cache resolved: pin set=%v err=%v", wired.Pin != nil, wired.pinCacheErr)
	}
	release := run("1.2.3")
	if release.exit != int(result.ExitEnvironment) || !strings.Contains(release.stdout, string(result.DiagPinnedVersionUnavailable)) || strings.Contains(release.stdout, `"pin": "dev-build"`) {
		t.Fatalf("release without cache: exit %d stdout %s stderr %s", release.exit, release.stdout, release.stderr)
	}
	dev := run(version.Dev)
	if dev.exit != 0 || !strings.Contains(dev.stdout, `"pin": "dev-build"`) {
		t.Fatalf("dev build should keep running: exit %d stdout %s", dev.exit, dev.stdout)
	}
	writePin("1.2.3")
	matched := run("1.2.3")
	if matched.exit != 0 || !strings.Contains(matched.stdout, `"pin": "matches"`) {
		t.Fatalf("matching pin should not need the cache: exit %d stdout %s", matched.exit, matched.stdout)
	}
}
