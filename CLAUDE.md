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
# Full test suite — Docker is the only prerequisite
# testcontainers-go auto-provisions a shared SQL Server container
go test ./...

# With coverage summary per package
go test -cover ./...

# Generate coverage profile and open HTML report
go test -coverprofile=cover.out ./...
go tool cover -html=cover.out              # opens in browser
go tool cover -func=cover.out | tail -1    # total line coverage

# Use an existing SQL Server instead of testcontainers
export SQLCMDSERVER=localhost SQLCMDUSER=sa SQLCMDPASSWORD='YourPassword1!'
go test ./...
```

**Test infrastructure**: `internal/sqlservertest` uses testcontainers-go with a named, reusable MSSQL container. All packages that need SQL Server call `sqlservertest.SetupForTestMain()` in their `TestMain`. If `SQLCMDSERVER` is already set, the container is not started (respects existing server). If Docker is unavailable, tests that need SQL Server will fail individually.

**Colima users**: The helper auto-detects Docker socket via `docker context inspect`. No manual `DOCKER_HOST` setup needed.

**Packages that still skip/fail**: `cmd/modern/root` (query tests have upstream config-layer issues), `cmd/modern/root/install`, `internal/container` (need Docker daemon for container lifecycle tests), `internal/net`, `internal/tools/tool` (env-specific).

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

**Workflow**:
1. Work in the current change at the tip of the tree
2. After every meaningful win: `jj describe -m "..."` then `jj new`
3. Before committing: run `go test ./...` and confirm tests pass (at least the packages that don't need SQL Server/Docker)
4. New code must have tests. Use `go test -cover ./path/to/pkg` to check coverage of packages you changed
5. Update `TODO.md` when completing a planned item (mark it done, record design decisions)
6. Do NOT use git commands directly

## Code Conventions

- Go 1.24, module path `github.com/microsoft/go-sqlcmd` (upstream module path retained)
- Tests use `testify/assert` and `testify/require`
- Build version injected via `-ldflags="-X main.version=..."`
- Custom linter in `cmd/sqlcmd-linter/` (import and assertion checks)
