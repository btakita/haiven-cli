# Contribute to the Haiven CLI

Use the Go version pinned in `go.mod` and run `go vet ./...` and
`go test -count=1 ./...` from the checkout root. If you use mise, `mise install`
installs the tools for `task check`, which also runs lint and cross-compiles
the CLI. Update the published schemas and golden results with `task golden`
when intentionally changing their output.

Keep command names, flags, schemas, and machine-readable outcome codes
backward compatible. Use conventional commit messages in the form
`type(scope): subject`. Contributions are distributed under the
[Apache 2.0 license](LICENSE).
