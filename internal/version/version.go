// Package version holds the build identity of the haiven binary.
package version

import "regexp"

// Version is the CLI's semantic version. Release builds set it with
// -ldflags "-X github.com/haiven-dev/haiven-cli/internal/version.Version=1.2.3".
// An unset value means a development build, which never re-executes a
// project's pinned release (see internal/pin).
var Version = Dev

// Dev is the version string of a build that did not come from a release.
const Dev = "0.0.0-dev"

// semver accepts MAJOR.MINOR.PATCH with an optional pre-release and build
// suffix. It is deliberately strict: a pinned version becomes a cache
// directory name and part of a download URL, so it must never carry a path
// separator or URL syntax.
var semver = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

// Valid reports whether v is a well-formed semantic version (without a
// leading "v").
func Valid(v string) bool { return semver.MatchString(v) }

// IsDev reports whether v is the development-build sentinel.
func IsDev(v string) bool { return v == Dev }
