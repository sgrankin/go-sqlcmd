// Copyright (c) Microsoft Corporation.
// Licensed under the MIT license.

// Package sqlservertest provides a shared SQL Server container for integration tests.
// It uses testcontainers-go with a named, reusable container so that all test packages
// running in parallel share a single SQL Server instance.
package sqlservertest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/mssql"
)

const (
	containerName = "go-sqlcmd-test"
	saPassword    = "SqlcmdTest1!"
	mssqlImage    = "mcr.microsoft.com/mssql/server:2022-latest"
)

// SetupForTestMain ensures a shared MSSQL container is running and sets the
// SQLCMDSERVER, SQLCMDUSER, and SQLCMDPASSWORD environment variables.
//
// If SQLCMDSERVER is already set, it does nothing (respects existing server).
// If Docker is unavailable, it prints a message and returns.
//
// Call this from TestMain before m.Run(). The returned cleanup function is a
// no-op — the container persists for other packages and testcontainers' Ryuk
// reaper cleans it up after all test processes exit.
func SetupForTestMain() (cleanup func()) {
	cleanup = func() {}

	if os.Getenv("SQLCMDSERVER") != "" {
		return cleanup
	}

	configureDockerHost()

	if err := startContainer(); err != nil {
		fmt.Fprintf(os.Stderr, "sqlservertest: %v\n", err)
		fmt.Fprintf(os.Stderr, "sqlservertest: tests requiring SQL Server will fail\n")
	}
	return cleanup
}

// configureDockerHost sets DOCKER_HOST and TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE
// if not already set, by reading the active Docker CLI context. This handles
// Colima and other non-standard Docker setups where the socket isn't at
// /var/run/docker.sock.
func configureDockerHost() {
	if os.Getenv("DOCKER_HOST") != "" {
		return
	}
	host := detectDockerHost()
	if host == "" {
		return
	}
	os.Setenv("DOCKER_HOST", host)
	// The socket path inside the Docker VM is always /var/run/docker.sock,
	// even when the host-side socket is elsewhere (e.g. Colima).
	// This tells Ryuk where to mount the socket inside its container.
	if os.Getenv("TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE") == "" {
		os.Setenv("TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE", "/var/run/docker.sock")
	}
}

// detectDockerHost reads the current Docker context to find the socket endpoint.
func detectDockerHost() string {
	out, err := exec.Command("docker", "context", "inspect").Output()
	if err != nil {
		return ""
	}
	var contexts []struct {
		Endpoints map[string]struct {
			Host string `json:"Host"`
		} `json:"Endpoints"`
	}
	if err := json.Unmarshal(out, &contexts); err != nil || len(contexts) == 0 {
		return ""
	}
	ep, ok := contexts[0].Endpoints["docker"]
	if !ok || !strings.HasPrefix(ep.Host, "unix://") {
		return ""
	}
	return ep.Host
}

func startContainer() (retErr error) {
	// testcontainers-go panics when Docker is not available (e.g. rootless Docker not found).
	defer func() {
		if r := recover(); r != nil {
			retErr = fmt.Errorf("could not start MSSQL container (Docker unavailable?): %v", r)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	ctr, err := mssql.Run(ctx, mssqlImage,
		mssql.WithAcceptEULA(),
		mssql.WithPassword(saPassword),
		testcontainers.WithReuseByName(containerName),
	)
	if err != nil {
		return fmt.Errorf("could not start MSSQL container: %w", err)
	}

	host, err := ctr.Host(ctx)
	if err != nil {
		return fmt.Errorf("could not get container host: %w", err)
	}

	port, err := ctr.MappedPort(ctx, "1433/tcp")
	if err != nil {
		return fmt.Errorf("could not get container port: %w", err)
	}

	server := fmt.Sprintf("%s,%s", host, port.Port())
	os.Setenv("SQLCMDSERVER", server)
	os.Setenv("SQLCMDUSER", "sa")
	os.Setenv("SQLCMDPASSWORD", saPassword)

	fmt.Fprintf(os.Stderr, "sqlservertest: MSSQL container ready at %s\n", server)
	return nil
}
