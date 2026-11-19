// Development tools, kept out of the haiven module so the CLI's own
// dependency graph stays small. Run with: go tool -modfile=tools/commitlint/go.mod commitlint
module github.com/haiven-dev/haiven-cli/tools/commitlint

go 1.27.1

tool github.com/conventionalcommit/commitlint

require (
	github.com/conventionalcommit/commitlint v0.12.0 // indirect
	github.com/conventionalcommit/parser v0.8.0 // indirect
	github.com/cpuguy83/go-md2man/v2 v2.0.7 // indirect
	github.com/russross/blackfriday/v2 v2.1.0 // indirect
	github.com/urfave/cli/v2 v2.27.7 // indirect
	github.com/xrash/smetrics v0.0.0-20250705151800-55b8f293f342 // indirect
	golang.org/x/mod v0.33.0 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
)

replace github.com/conventionalcommit/parser => github.com/conventionalcommit/parser v0.6.0
