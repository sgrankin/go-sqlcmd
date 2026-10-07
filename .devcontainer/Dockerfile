# go-sqlcmd Development Container
# The Go major.minor version here must match the 'go' directive in go.mod.
# GOTOOLCHAIN=auto lets Go download the exact patch version (e.g. 1.25.9)
# when the base image is slightly behind. Downloads are verified against
# sum.golang.org.
FROM mcr.microsoft.com/devcontainers/go:2-1.27-bookworm
ENV GOTOOLCHAIN=auto

# The base image sets GOPATH=/go owned by root. Override to a user-writable
# location so GOTOOLCHAIN=auto can download patch releases without sudo.
ENV GOPATH=/home/vscode/go

# Install additional OS packages
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get install -y curl libkrb5-dev gnupg2 \
    && apt-get clean -y && rm -rf /var/lib/apt/lists/*

# Install Microsoft ODBC driver and mssql-tools18 (legacy ODBC sqlcmd/bcp for compatibility testing)
RUN curl -fsSL https://packages.microsoft.com/keys/microsoft.asc | gpg --dearmor -o /usr/share/keyrings/microsoft-prod.gpg \
    && curl -fsSL https://packages.microsoft.com/config/debian/12/prod.list | tee /etc/apt/sources.list.d/mssql-release.list \
    && apt-get update \
    && ACCEPT_EULA=Y apt-get install -y msodbcsql18 mssql-tools18 unixodbc-dev \
    && apt-get clean -y && rm -rf /var/lib/apt/lists/*
ENV PATH="/opt/mssql-tools18/bin:${PATH}"

# The post-create script builds golangci-lint from the workspace's go.mod
# so its analyzers use the same dependency versions as local development and CI.

# Install additional Go tools (pinned versions for reproducibility)
# These versions must be compatible with the Go version in the base image.
RUN go install golang.org/x/tools/gopls@v0.23.0 \
    && go install github.com/go-delve/delve/cmd/dlv@v1.27.2 \
    && go install honnef.co/go/tools/cmd/staticcheck@v0.8.1 \
    && go install golang.org/x/text/cmd/gotext@v0.40.0

# Fix ownership: go install ran as root and populated $GOPATH with root-owned
# files. chown the entire tree so vscode can write (GOTOOLCHAIN=auto downloads).
RUN mkdir -p /home/vscode/bin \
    && chown -R vscode:vscode /home/vscode/go /home/vscode/bin
ENV PATH="/home/vscode/bin:${GOPATH}/bin:${PATH}"
