// Copyright (c) Microsoft Corporation.
// Licensed under the MIT license.

package sqlcmd

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProxyDialerHostName(t *testing.T) {
	d := &proxyDialer{serverName: "myserver.database.windows.net"}
	assert.Equal(t, "myserver.database.windows.net", d.HostName())
}

func TestProxyDialerHostNameEmpty(t *testing.T) {
	d := &proxyDialer{}
	assert.Equal(t, "", d.HostName())
}

func TestProxyDialerDialAddressOverridesHostAndPortForTCP(t *testing.T) {
	d := &proxyDialer{
		serverName: "server.example.com",
		serverPort: "1433",
		targetHost: "proxy.local",
		targetPort: "1444",
	}

	dialAddr := d.dialAddress("tcp", "server.example.com:1433")
	assert.Equal(t, "proxy.local:1444", dialAddr)
}

func TestProxyDialerDialAddressKeepsPortForUDP(t *testing.T) {
	d := &proxyDialer{
		serverName: "server.example.com",
		serverPort: "1433",
		targetHost: "proxy.local",
		targetPort: "1444",
	}

	dialAddr := d.dialAddress("udp", "server.example.com:1434")
	assert.Equal(t, "proxy.local:1434", dialAddr)
}

func TestProxyDialerPreservesRedirects(t *testing.T) {
	d := &proxyDialer{
		serverName: "server.example.com", serverPort: "1433",
		targetHost: "localhost", targetPort: "1444",
	}
	assert.Equal(t, "routed.example.com:11001", d.dialAddress("tcp", "routed.example.com:11001"))
	assert.Equal(t, "server.example.com:11001", d.dialAddress("tcp", "server.example.com:11001"))
}

func clearProxyEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(name, "")
	}
}

func TestSocksProxyConfiguration(t *testing.T) {
	clearProxyEnvironment(t)
	for _, value := range []string{
		"not-a-url", "http://localhost:1080", "socks5h://", "socks5://:1080",
		"socks5://localhost:1080:1081", "socks5://localhost:0", "socks5://localhost:65536", "socks5://localhost:",
		"socks5://localhost/path", "socks5://localhost?query", "socks5://localhost#fragment",
		"socks5://user:secret%zz@localhost", "socks5://:secret@localhost",
		"socks5://" + strings.Repeat("u", 256) + ":secret@localhost",
	} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("ALL_PROXY", value)
			d, err := socksDialerFromEnvironment()
			require.Error(t, err)
			assert.Nil(t, d)
			assert.NotContains(t, err.Error(), "secret")
		})
	}
	for _, value := range []string{
		"socks5://localhost", "socks5h://localhost:1080/", "socks5h://[::1]:1080",
		"socks5://user:secret@localhost:1080",
	} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("ALL_PROXY", value)
			d, err := socksDialerFromEnvironment()
			require.NoError(t, err)
			assert.NotNil(t, d)
		})
	}
}

func TestSocksProxyEnvironmentPrecedence(t *testing.T) {
	clearProxyEnvironment(t)
	d, err := socksDialerFromEnvironment()
	require.NoError(t, err)
	assert.Nil(t, d)
	t.Setenv("all_proxy", "socks5h://localhost:1080")
	t.Setenv("no_proxy", "lower.example")
	d, err = socksDialerFromEnvironment()
	require.NoError(t, err)
	assert.True(t, d.(*socksDialer).noProxy[0].matches("lower.example", "1433"))
	t.Setenv("ALL_PROXY", "invalid")
	_, err = socksDialerFromEnvironment()
	require.Error(t, err)
	t.Setenv("ALL_PROXY", "socks5://localhost:1081")
	t.Setenv("NO_PROXY", "upper.example")
	d, err = socksDialerFromEnvironment()
	require.NoError(t, err)
	assert.True(t, d.(*socksDialer).noProxy[0].matches("upper.example", "1433"))
	assert.False(t, d.(*socksDialer).noProxy[0].matches("lower.example", "1433"))
}

func TestNoProxy(t *testing.T) {
	for _, test := range []struct {
		rules, addr string
		match       bool
	}{
		{"", "localhost:1433", false},
		{"", "127.0.0.1:1433", false},
		{"*", "anything.invalid:1433", true},
		{"example.com", "example.com:1433", true},
		{"example.com", "sql.example.com:1433", true},
		{"example.com", "badexample.com:1433", false},
		{".example.com", "example.com:1433", false},
		{".example.com", "sql.example.com:1433", true},
		{"*.example.com", "example.com:1433", false},
		{"*.example.com", "sql.example.com:1433", true},
		{"EXAMPLE.COM:1433", "SQL.Example.com:1433", true},
		{"example.com:1433", "sql.example.com:11000", false},
		{"*.example.com:1433", "sql.example.com:1433", true},
		{"127.0.0.1", "127.0.0.1:1433", true},
		{"127.0.0.1:1433", "127.0.0.1:11000", false},
		{"10.0.0.0/8", "10.1.2.3:1433", true},
		{"10.0.0.0/8", "sql.example.com:1433", false},
		{"10.0.0.0/8", "11.1.2.3:1433", false},
		{"::1", "[::1]:1433", true},
		{"[::1]:1433", "[::1]:1433", true},
		{"2001:db8::/32", "[2001:db8::1]:1433", true},
		{"2001:db8::1", "[2001:0db8:0:0:0:0:0:1]:1433", true},
		{" ,other.example, example.com ,", "sql.example.com:1433", true},
	} {
		t.Run(test.rules+"/"+test.addr, func(t *testing.T) {
			host, port, err := net.SplitHostPort(test.addr)
			require.NoError(t, err)
			matched := false
			for _, rule := range parseNoProxy(test.rules) {
				matched = matched || rule.matches(host, port)
			}
			assert.Equal(t, test.match, matched)
		})
	}
}

func TestSocksProxyBypass(t *testing.T) {
	clearProxyEnvironment(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { listener.Close() })
	t.Setenv("ALL_PROXY", "socks5h://127.0.0.1:1")
	t.Setenv("NO_PROXY", listener.Addr().String())
	d, err := socksDialerFromEnvironment()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := d.DialContext(ctx, "tcp", listener.Addr().String())
	require.NoError(t, err)
	conn.Close()
	// A different port must still use the unavailable proxy.
	conn, err = d.DialContext(ctx, "tcp", "127.0.0.1:1433")
	require.Error(t, err)
	assert.Nil(t, conn)
}

func TestSocksProxyFailureDoesNotDialDirect(t *testing.T) {
	clearProxyEnvironment(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { listener.Close() })
	t.Setenv("ALL_PROXY", "socks5h://127.0.0.1:1")
	d, err := socksDialerFromEnvironment()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := d.DialContext(ctx, "tcp", listener.Addr().String())
	require.Error(t, err)
	assert.Nil(t, conn)
}

func TestSocksProxyNegotiationContext(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "deadline"}[deadline], func(t *testing.T) {
			clearProxyEnvironment(t)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			defer listener.Close()
			require.NoError(t, listener.(*net.TCPListener).SetDeadline(time.Now().Add(3*time.Second)))
			t.Setenv("ALL_PROXY", "socks5h://"+listener.Addr().String())
			d, err := socksDialerFromEnvironment()
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if deadline {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 200*time.Millisecond)
			}
			defer cancel()
			result := make(chan error, 1)
			go func() {
				conn, err := d.DialContext(ctx, "tcp", "unresolved.invalid:1433")
				if conn != nil {
					conn.Close()
				}
				result <- err
			}()
			peer, err := listener.Accept()
			require.NoError(t, err)
			defer peer.Close()
			require.NoError(t, peer.SetDeadline(time.Now().Add(3*time.Second)))
			greeting := make([]byte, 3)
			_, err = io.ReadFull(peer, greeting)
			require.NoError(t, err)
			assert.Equal(t, []byte{5, 1, 0}, greeting)
			if !deadline {
				cancel()
			}
			select {
			case err := <-result:
				require.Error(t, err)
			case <-time.After(3 * time.Second):
				t.Fatal("SOCKS negotiation ignored its context")
			}
		})
	}
}

func TestSocksProxyRejectsUDP(t *testing.T) {
	clearProxyEnvironment(t)
	t.Setenv("ALL_PROXY", "socks5h://127.0.0.1:1080")
	d, err := socksDialerFromEnvironment()
	require.NoError(t, err)
	_, err = d.DialContext(context.Background(), "udp", "sql.invalid:1434")
	require.ErrorContains(t, err, "explicit port")
}

func TestSocksProxyRejectsNonTCPProtocol(t *testing.T) {
	clearProxyEnvironment(t)
	t.Setenv("ALL_PROXY", "socks5h://127.0.0.1:1080")
	for _, server := range []string{"np:sql.invalid", `\\sql.invalid\pipe\sql\query`, "admin:sql.invalid"} {
		c := ConnectSettings{ServerName: server, UserName: "sa"}
		_, err := c.connector()
		require.Error(t, err)
	}
}

func TestServerNameOverrideMatchesDriverDestination(t *testing.T) {
	clearProxyEnvironment(t)
	for _, test := range []struct {
		server, override, addr, want string
	}{
		{`dial.example.com\instance`, "login.example.com", "login.example.com:5000", "dial.example.com:5000"},
		{"dial.example.com", `login.example.com\instance`, "login.example.com:5000", "dial.example.com:5000"},
		{"dial.example.com,1444", ".", "localhost:1444", "dial.example.com:1444"},
		{"dial.example.com,1444", "(local)", "localhost:1444", "dial.example.com:1444"},
		{"admin:dial.example.com", "login.example.com", "login.example.com:1434", "dial.example.com:1434"},
	} {
		t.Run(test.server+"/"+test.override, func(t *testing.T) {
			settings := ConnectSettings{ServerName: test.server, ServerNameOverride: test.override, UserName: "sa"}
			connector, err := settings.connector()
			require.NoError(t, err)
			d := connector.(*mssql.Connector).Dialer.(*proxyDialer)
			assert.Equal(t, test.want, d.dialAddress("tcp", test.addr))
			assert.Equal(t, "dial.example.com:1434", d.dialAddress("udp", d.serverName+":1434"))
		})
	}
}
