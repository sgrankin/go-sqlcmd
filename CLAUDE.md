# go-sqlcmd

Fork of [microsoft/go-sqlcmd](https://github.com/microsoft/go-sqlcmd) — a CLI for Microsoft SQL Server and Azure SQL. See `TODO.md` for planned improvements.

## Build & Run

```bash
# Build
go build -o sqlcmd ./cmd/modern

# Build with version tag (matches CI)
go build -o sqlcmd -ldflags="-X main.version=$(git describe --tags --abbrev=0)" ./cmd/modern

# Run without building
go run ./cmd/modern [args...]
```

## Test

```bash
# Unit tests (no SQL Server needed — some tests will be skipped)
go test ./...

# With a SQL Server instance (used in CI)
export SQLCMDSERVER=localhost SQLCMDUSER=sa SQLCMDPASSWORD=<password>
go test -v ./...
```

CI spins up `mcr.microsoft.com/mssql/server:2022-latest` in Docker. See `.github/workflows/pr-validation.yml`.

## Lint

```bash
golangci-lint run
```

CI uses `golangci-lint` with `only-new-issues: true` — no config file, just defaults.

## Architecture

**Dual CLI mode**: `cmd/modern/main.go` dispatches based on the first argument:
- Modern subcommands (`create`, `query`, `config`, `start`, `stop`, etc.) → Cobra-based CLI
- Legacy ODBC-style flags (`-Q`, `-i`, `-o`, `-S`, etc.) → Kong-based CLI in `cmd/sqlcmd/`

**Key packages**:
- `cmd/modern/` — main entry point, Cobra command tree
- `cmd/sqlcmd/` — legacy CLI (backward compat)
- `pkg/sqlcmd/` — core library: connection, batch processing, variable substitution, REPL
- `pkg/console/` — interactive console using `liner`
- `internal/config/` — `~/.sqlcmd/sqlconfig` context management (Viper)
- `internal/output/` — output formatting (text, JSON, YAML, XML)
- `internal/container/` — Docker/Podman container lifecycle
- `internal/cmdparser/` — Cobra command scaffolding
- `internal/localizer/` — i18n (11 languages, `SQLCMD_LANG` env var)

**Authentication**: SQL auth, Windows integrated, Azure AD (via `azidentity` — `ActiveDirectoryDefault`, password, interactive, managed identity, service principal).

## Version Control

This is a **jj** (Jujutsu) repo colocated with git.

**Remotes**:
- `origin` — `github.com/sgrankin/go-sqlcmd` (our fork, push here)
- `upstream` — `github.com/microsoft/go-sqlcmd`

**Workflow**: Work in the current change at the tip of the tree. When done, `jj describe` to set the commit message, then `jj new` to create a clean change for the next edit. Do NOT use git commands directly.

## Code Conventions

- Go 1.24, module path `github.com/microsoft/go-sqlcmd` (upstream module path retained)
- Tests use `testify/assert` and `testify/require`
- Build version injected via `-ldflags="-X main.version=..."`
- Custom linter in `cmd/sqlcmd-linter/` (import and assertion checks)
