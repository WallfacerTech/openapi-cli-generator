# OpenAPI CLI Generator (with Resource Grouping)

A fork of [danielgtaylor/openapi-cli-generator](https://github.com/danielgtaylor/openapi-cli-generator) that generates Go-based command-line clients from OpenAPI 3.0 specs.

The upstream project is no longer maintained. This fork adds **resource grouping** -- automatically organizing CLI commands into subcommand groups based on REST URL path structure, instead of generating a flat list of top-level commands.

## Resource Grouping

The upstream generator produces flat commands derived from `operationId`:

```
my-cli list-items
my-cli create-an-item
my-cli get-item
```

This fork analyzes the API's URL paths and groups operations under resource subcommands:

```
my-cli items list
my-cli items create
my-cli items get
```

Groups are auto-derived from the path structure. For example, `GET /v1/accounts/{id}/tasks` produces a `tasks` group with a `list` action. Action names are derived from the HTTP method and path pattern:

| Method + Path Pattern | Derived Action |
|----------------------|----------------|
| `GET /resources` | `list` |
| `GET /resources/{id}` | `get` |
| `POST /resources` | `create` |
| `PATCH /resources/{id}` | `update` |
| `DELETE /resources/{id}` | `delete` |
| `POST /resources/{id}/action-name` | `action-name` |

Both the group and action name can be overridden per-operation using the `x-cli-group` and `x-cli-name` OpenAPI extensions.

## Usage

### Install the generator

```sh
go install github.com/WallfacerTech/openapi-cli-generator@latest
```

### Generate a CLI

Create a project directory and generate commands from your OpenAPI spec:

```sh
mkdir my-cli && cd my-cli
go mod init my-cli
openapi-cli-generator generate openapi.yaml
```

This produces an `openapi.go` file with all the CLI commands. Create a `main.go` entrypoint:

```go
package main

import "github.com/WallfacerTech/openapi-cli-generator/cli"

func main() {
	cli.Init(&cli.Config{
		AppName:   "my-cli",
		EnvPrefix: "MY_CLI",
		Version:   "1.0.0",
	})

	openapiRegister(false)
	cli.Root.Execute()
}
```

Build and run:

```sh
go build -o my-cli .
./my-cli --help
```

## Features

- Resource-grouped subcommands from URL path analysis
- Syntax-highlighted JSON output
- JMESPath response filtering via `--filter`
- Table output format
- OAuth 2.0, API key, and Auth0 authentication support
- Input from files, stdin, or shorthand syntax
- Caching and profile support

## Modified Files (vs. Upstream)

| File | Change |
|------|--------|
| `main.go` | Added `OperationGroup` type, path analysis (`precomputeResources`, `deriveGroupAndAction`), `x-cli-group` extension support |
| `templates/commands.tmpl` | Wraps operations in group parent commands instead of flat root commands |
| `bindata.go` | Regenerated from updated template |

## License

MIT -- see [LICENSE](LICENSE).

## Attribution

Originally created by [Daniel G. Taylor](https://github.com/danielgtaylor/openapi-cli-generator).
