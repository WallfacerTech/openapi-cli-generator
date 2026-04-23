# OpenAPI CLI Generator

A fork of [danielgtaylor/openapi-cli-generator](https://github.com/danielgtaylor/openapi-cli-generator) that generates Go-based command-line clients from OpenAPI 3.0 specs. The upstream project is no longer maintained.

### What this fork adds

- **Resource grouping** -- commands are organized into subcommand groups based on URL path structure instead of a flat list (e.g. `my-cli items list` instead of `my-cli list-items`)
- **`x-cli-group` / `x-cli-name` extensions** -- override the auto-derived group or action name per-operation
- **Action name derivation** from HTTP method + path pattern (`GET /resources` → `list`, `POST /resources/{id}/action` → `action`)

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

## License

MIT -- see [LICENSE](LICENSE).

## Attribution

Originally created by [Daniel G. Taylor](https://github.com/danielgtaylor/openapi-cli-generator).
