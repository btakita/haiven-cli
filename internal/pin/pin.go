// Package pin replaces uvx's pinned-run behavior: when a project's
// haiven.toml pins a haiven release other than the running one, that exact
// release is run from a local cache, downloading it once if needed.
package pin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/haiven-dev/haiven-cli/internal/version"
)

// ActiveEnv is set on a re-executed child to the version it was started for.
// A process that sees it never re-executes again, so a mis-built cached
// binary cannot loop.
const ActiveEnv = "HAIVEN_PIN_ACTIVE"

// Fetcher downloads one haiven release binary for a platform to dest.
// Implementations must verify integrity before writing dest.
type Fetcher interface {
	Fetch(ctx context.Context, version, goos, goarch, dest string) error
}

// ErrChecksumMismatch means a download did not match its published checksum.
var ErrChecksumMismatch = errors.New("checksum mismatch")

// Cache stores downloaded releases under Dir.
type Cache struct {
	Dir string
}

// DefaultCacheDir is the per-user cache root: the OS user cache directory
// plus "haiven".
func DefaultCacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "haiven"), nil
}

// BinaryName is the executable name on goos.
func BinaryName(goos string) string {
	if goos == "windows" {
		return "haiven.exe"
	}
	return "haiven"
}

// Path is where the binary for v on goos/goarch lives in the cache.
func (c Cache) Path(v, goos, goarch string) (string, error) {
	if !version.Valid(v) {
		return "", fmt.Errorf("invalid version %q", v)
	}
	return filepath.Join(c.Dir, "versions", v, goos+"-"+goarch, BinaryName(goos)), nil
}

// Decision is what the running binary should do about a project pin.
type Decision struct {
	// Reexec is true when Binary must run instead of this process.
	Reexec bool
	// Binary is the cached pinned release.
	Binary string
	// Pinned is the project's pinned version ("" when unpinned).
	Pinned string
	// Reason explains a decision not to re-execute, for capabilities.
	Reason Reason
}

// Reason is why the running binary serves the command itself.
type Reason string

// The closed reason vocabulary.
const (
	ReasonUnpinned Reason = "unpinned"
	ReasonMatches  Reason = "matches"
	ReasonDevBuild Reason = "dev-build"
	ReasonActive   Reason = "already-reexecuted"
	ReasonPinned   Reason = "pinned-release"
)

// Resolver decides and, when needed, materializes the pinned release.
type Resolver struct {
	Cache   Cache
	Fetcher Fetcher
	GOOS    string
	GOARCH  string
	// Running is this binary's version.
	Running string
}

// Resolve returns the decision for a project pinned to pinned. active is the
// value of ActiveEnv in this process's environment.
func (r Resolver) Resolve(ctx context.Context, pinned, active string) (Decision, error) {
	d := Decision{Pinned: pinned}
	switch {
	case pinned == "":
		d.Reason = ReasonUnpinned
		return d, nil
	case pinned == r.Running:
		d.Reason = ReasonMatches
		return d, nil
	case active != "":
		d.Reason = ReasonActive
		return d, nil
	case version.IsDev(r.Running):
		d.Reason = ReasonDevBuild
		return d, nil
	}
	path, err := r.Cache.Path(pinned, r.GOOS, r.GOARCH)
	if err != nil {
		return d, err
	}
	if _, err := os.Stat(path); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return d, err
		}
		if r.Fetcher == nil {
			return d, fmt.Errorf("haiven %s is not cached and no fetcher is configured", pinned)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return d, err
		}
		if err := r.Fetcher.Fetch(ctx, pinned, r.GOOS, r.GOARCH, path); err != nil {
			return d, err
		}
	}
	d.Reexec, d.Binary, d.Reason = true, path, ReasonPinned
	return d, nil
}
