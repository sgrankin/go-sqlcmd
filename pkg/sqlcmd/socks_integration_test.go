// Copyright (c) Microsoft Corporation.
// Licensed under the MIT license.

package sqlcmd

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/binary"
	"fmt"
	"io"
	"math/big"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	mssql "github.com/microsoft/go-mssqldb"
	"github.com/microsoft/go-mssqldb/azuread"
	"github.com/microsoft/go-mssqldb/msdsn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSQLConnectorUsesSOCKSForAuthenticationMethods(t *testing.T) {
	for _, method := range []string{SqlPassword, azuread.ActiveDirectoryAzCli} {
		t.Run(method, func(t *testing.T) {
			proxyURL, results := startSQLSOCKSPeer(t, 1, nil, true, "")
			setSQLSOCKSEnvironment(t, proxyURL)
			settings := ConnectSettings{
				ServerName: "tcp:sql-proxy-test.invalid,1433", UserName: "test", Password: "test",
				AuthenticationMethod: method, LoginTimeoutSeconds: 2,
			}
			connector, err := settings.connector()
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, err = connector.Connect(ctx)
			require.Error(t, err)
			result := receiveSQLSOCKSResult(t, ctx, results)
			require.NoError(t, result.err)
			assert.Equal(t, "sql-proxy-test.invalid:1433", result.destination)
		})
	}
}

func TestSOCKSCertificateValidatedQueryAndReconnect(t *testing.T) {
	for _, encrypt := range []string{"true", "strict"} {
		t.Run(encrypt, func(t *testing.T) {
			serverTLS, roots := sqlSOCKSTestCertificate(t, "sql-proxy-test.invalid")
			proxyURL, results := startSQLSOCKSPeer(t, 2, serverTLS, false, "")
			setSQLSOCKSEnvironment(t, proxyURL)
			settings := ConnectSettings{
				ServerName: "tcp:sql-proxy-test.invalid,1433", UserName: "test", Password: "test", Encrypt: encrypt,
			}
			connector := sqlSOCKSTestConnector(t, settings, roots)
			db := sql.OpenDB(connector)
			defer db.Close()
			// Returning each connection closes it, so the second query must dial again.
			db.SetMaxIdleConns(0)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for range 2 {
				var value int
				require.NoError(t, db.QueryRowContext(ctx, "SELECT 1").Scan(&value))
				assert.Equal(t, 1, value)
				result := receiveSQLSOCKSResult(t, ctx, results)
				require.NoError(t, result.err)
				assert.Equal(t, "sql-proxy-test.invalid:1433", result.destination)
				assert.Equal(t, "sql-proxy-test.invalid", result.serverName)
				assert.Equal(t, "sql-proxy-test.invalid", result.loginServer)
			}
		})
	}
}

func TestSOCKSPreservesServerNameOverride(t *testing.T) {
	serverTLS, roots := sqlSOCKSTestCertificate(t, "login-proxy-test.invalid")
	proxyURL, results := startSQLSOCKSPeer(t, 1, serverTLS, false, "")
	setSQLSOCKSEnvironment(t, proxyURL)
	settings := ConnectSettings{
		ServerName: "tcp:dial-proxy-test.invalid,1444", ServerNameOverride: "login-proxy-test.invalid",
		UserName: "test", Password: "test", Encrypt: "strict",
	}
	db := sql.OpenDB(sqlSOCKSTestConnector(t, settings, roots))
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var value int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT 1").Scan(&value))
	result := receiveSQLSOCKSResult(t, ctx, results)
	require.NoError(t, result.err)
	assert.Equal(t, "dial-proxy-test.invalid:1444", result.destination)
	assert.Equal(t, "login-proxy-test.invalid", result.serverName)
	assert.Equal(t, "login-proxy-test.invalid", result.loginServer)
}

func TestSOCKSRejectsMismatchedSQLCertificate(t *testing.T) {
	serverTLS, roots := sqlSOCKSTestCertificate(t, "other-proxy-test.invalid")
	proxyURL, results := startSQLSOCKSPeer(t, 1, serverTLS, false, "")
	setSQLSOCKSEnvironment(t, proxyURL)
	settings := ConnectSettings{
		ServerName: "tcp:sql-proxy-test.invalid,1433", UserName: "test", Password: "test", Encrypt: "strict",
	}
	db := sql.OpenDB(sqlSOCKSTestConnector(t, settings, roots))
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := db.PingContext(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sql-proxy-test.invalid")
	assert.Contains(t, err.Error(), "certificate")
	result := receiveSQLSOCKSResult(t, ctx, results)
	assert.Error(t, result.err)
	assert.Empty(t, result.loginServer, "TLS rejection must precede SQL login")
}

func TestSOCKSRedirectUsesRoutedHostname(t *testing.T) {
	for _, tc := range []struct {
		name     string
		dialHost string
		override string
		route    string
	}{
		{"hostname", "sql-proxy-test.invalid", "", "routed-proxy-test.invalid"},
		{"hostname_with_override", "dial-proxy-test.invalid", "sql-proxy-test.invalid", "routed-proxy-test.invalid"},
		{"port_with_override", "dial-proxy-test.invalid", "sql-proxy-test.invalid", "sql-proxy-test.invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			serverTLS, roots := sqlSOCKSTestCertificate(t, "sql-proxy-test.invalid", tc.route)
			proxyURL, results := startSQLSOCKSPeer(t, 2, serverTLS, false, tc.route+":1444")
			setSQLSOCKSEnvironment(t, proxyURL)
			settings := ConnectSettings{
				ServerName: "tcp:" + tc.dialHost + ",1433", ServerNameOverride: tc.override,
				UserName: "test", Password: "test", Encrypt: "strict",
			}
			db := sql.OpenDB(sqlSOCKSTestConnector(t, settings, roots))
			defer db.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var value int
			require.NoError(t, db.QueryRowContext(ctx, "SELECT 1").Scan(&value))
			assert.Equal(t, 1, value)
			for _, expected := range []struct{ destination, server string }{
				{tc.dialHost + ":1433", "sql-proxy-test.invalid"},
				{tc.route + ":1444", tc.route},
			} {
				result := receiveSQLSOCKSResult(t, ctx, results)
				require.NoError(t, result.err)
				assert.Equal(t, expected.destination, result.destination)
				assert.Equal(t, expected.server, result.serverName)
				assert.Equal(t, expected.server, result.loginServer)
			}
		})
	}
}

func sqlSOCKSTestConnector(t *testing.T, settings ConnectSettings, roots *x509.CertPool) *mssql.Connector {
	t.Helper()
	dsn, err := settings.ConnectionString()
	require.NoError(t, err)
	config, err := msdsn.Parse(dsn)
	require.NoError(t, err)
	config.TLSConfig.RootCAs = roots
	connector := mssql.NewConnectorConfig(config)
	require.NoError(t, configureConnectionDialer(connector, &settings))
	return connector
}

func setSQLSOCKSEnvironment(t *testing.T, proxyURL string) {
	t.Helper()
	t.Setenv("ALL_PROXY", proxyURL)
	t.Setenv("all_proxy", "")
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
}

func sqlSOCKSTestCertificate(t *testing.T, hosts ...string) (*tls.Config, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: hosts[0]}, DNSNames: hosts,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	require.NoError(t, err)
	parsed, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	return &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		MinVersion:   tls.VersionTLS12, MaxVersion: tls.VersionTLS12,
		NextProtos: []string{"tds/8.0"},
	}, roots
}

type sqlSOCKSResult struct {
	destination string
	serverName  string
	loginServer string
	err         error
}

func receiveSQLSOCKSResult(t *testing.T, ctx context.Context, results <-chan sqlSOCKSResult) sqlSOCKSResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		return sqlSOCKSResult{}
	}
}

// The peer implements SOCKS CONNECT followed by just the TDS exchanges needed
// for SQL login, redirection, and SELECT 1. All SQL names use the reserved
// .invalid suffix, so successful queries require proxy-side name handling.
func startSQLSOCKSPeer(t *testing.T, connections int, serverTLS *tls.Config, reject bool, redirect string) (string, <-chan sqlSOCKSResult) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	results := make(chan sqlSOCKSResult, connections)
	done := make(chan struct{})
	var mu sync.Mutex
	var active net.Conn
	go func() {
		defer close(done)
		for i := range connections {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			active = conn
			mu.Unlock()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			result := sqlSOCKSResult{}
			result.destination, result.err = sqlSOCKSHandshake(conn, reject)
			if result.err == nil && !reject {
				route := ""
				if i == 0 {
					route = redirect
				}
				result.serverName, result.loginServer, result.err = sqlSOCKSTDSExchange(conn, serverTLS, route)
			}
			_ = conn.Close()
			results <- result
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		mu.Lock()
		if active != nil {
			_ = active.Close()
		}
		mu.Unlock()
		<-done
	})
	return "socks5h://" + listener.Addr().String(), results
}

func sqlSOCKSHandshake(conn net.Conn, reject bool) (string, error) {
	var greeting [2]byte
	if _, err := io.ReadFull(conn, greeting[:]); err != nil {
		return "", err
	}
	if greeting[0] != 5 {
		return "", fmt.Errorf("unexpected SOCKS version %d", greeting[0])
	}
	if _, err := io.CopyN(io.Discard, conn, int64(greeting[1])); err != nil {
		return "", err
	}
	if _, err := conn.Write([]byte{5, 0}); err != nil {
		return "", err
	}
	var request [5]byte
	if _, err := io.ReadFull(conn, request[:]); err != nil {
		return "", err
	}
	if request[0] != 5 || request[1] != 1 || request[3] != 3 {
		return "", fmt.Errorf("expected SOCKS hostname CONNECT, got %v", request)
	}
	address := make([]byte, int(request[4])+2)
	if _, err := io.ReadFull(conn, address); err != nil {
		return "", err
	}
	host := string(address[:len(address)-2])
	port := binary.BigEndian.Uint16(address[len(address)-2:])
	reply := []byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}
	if reject {
		reply[1] = 5
	}
	_, err := conn.Write(reply)
	return net.JoinHostPort(host, strconv.Itoa(int(port))), err
}

func sqlSOCKSTDSExchange(conn net.Conn, config *tls.Config, redirect string) (serverName, loginServer string, err error) {
	// A strict connection starts with raw TLS; encrypt=true starts with PRELOGIN
	// and transports the TLS handshake inside TDS packets.
	var first [1]byte
	if _, err = io.ReadFull(conn, first[:]); err != nil {
		return
	}
	transport := &sqlSOCKSPrefixConn{Conn: conn, prefix: first[:]}
	var tlsConn *tls.Conn
	if first[0] == 0x16 {
		tlsConn = tls.Server(transport, config)
	} else {
		if _, _, err = sqlSOCKSReadPacket(transport); err != nil {
			return
		}
		if err = sqlSOCKSWritePacket(conn, 4, []byte{1, 0, 6, 0, 1, 0xff, 1}); err != nil {
			return
		}
		handshake := &sqlSOCKSHandshakeConn{Conn: conn}
		tlsConn = tls.Server(handshake, config)
		if err = tlsConn.Handshake(); err != nil {
			return
		}
		handshake.raw = true
	}
	if err = tlsConn.Handshake(); err != nil {
		return
	}
	serverName = tlsConn.ConnectionState().ServerName
	if first[0] == 0x16 {
		if _, _, err = sqlSOCKSReadPacket(tlsConn); err != nil {
			return
		}
		if err = sqlSOCKSWritePacket(tlsConn, 4, []byte{1, 0, 6, 0, 1, 0xff, 1}); err != nil {
			return
		}
	}
	var packetType byte
	var login []byte
	packetType, login, err = sqlSOCKSReadPacket(tlsConn)
	if err != nil {
		return
	}
	if packetType != 0x10 || len(login) < 56 {
		err = fmt.Errorf("expected LOGIN7, got type %x, size %d", packetType, len(login))
		return
	}
	offset, length := int(binary.LittleEndian.Uint16(login[52:])), 2*int(binary.LittleEndian.Uint16(login[54:]))
	if offset+length > len(login) {
		err = fmt.Errorf("LOGIN7 server name exceeds packet")
		return
	}
	loginServer = sqlSOCKSDecodeUTF16(login[offset : offset+length])
	// LOGINACK declares TDS 7.4 and an empty server program name.
	response := []byte{0xad, 10, 0, 1, 0x74, 0, 0, 4, 0, 0, 0, 0, 0}
	if redirect != "" {
		var host, port string
		host, port, err = net.SplitHostPort(redirect)
		if err != nil {
			return
		}
		var number int
		number, err = strconv.Atoi(port)
		if err != nil {
			return
		}
		name := sqlSOCKSEncodeUTF16(host)
		routing := []byte{20, byte(5 + len(name)), byte((5 + len(name)) >> 8), 0, byte(number), byte(number >> 8), byte(len(name) / 2), 0}
		routing = append(routing, name...)
		routing = append(routing, 0, 0)
		response = append(response, 0xe3, byte(len(routing)), byte(len(routing)>>8))
		response = append(response, routing...)
	}
	response = append(response, 0xfd, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	if err = sqlSOCKSWritePacket(tlsConn, 4, response); err != nil || redirect != "" {
		return
	}
	packetType, _, err = sqlSOCKSReadPacket(tlsConn)
	if err != nil {
		return
	}
	if packetType != 1 {
		err = fmt.Errorf("expected SQL batch, got type %x", packetType)
		return
	}
	// One non-null INT column, one row with value 1, then DONE with one row.
	err = sqlSOCKSWritePacket(tlsConn, 4, []byte{
		0x81, 1, 0, 0, 0, 0, 0, 0, 0, 0x38, 0,
		0xd1, 1, 0, 0, 0,
		0xfd, 0x10, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0,
	})
	return
}

type sqlSOCKSPrefixConn struct {
	net.Conn
	prefix []byte
}

func (c *sqlSOCKSPrefixConn) Read(p []byte) (int, error) {
	if len(c.prefix) != 0 {
		n := copy(p, c.prefix)
		c.prefix = c.prefix[n:]
		return n, nil
	}
	return c.Conn.Read(p)
}

type sqlSOCKSHandshakeConn struct {
	net.Conn
	pending []byte
	raw     bool
}

func (c *sqlSOCKSHandshakeConn) Read(p []byte) (int, error) {
	if c.raw {
		return c.Conn.Read(p)
	}
	if len(c.pending) == 0 {
		_, payload, err := sqlSOCKSReadPacket(c.Conn)
		if err != nil {
			return 0, err
		}
		c.pending = payload
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

func (c *sqlSOCKSHandshakeConn) Write(p []byte) (int, error) {
	if c.raw {
		return c.Conn.Write(p)
	}
	if err := sqlSOCKSWritePacket(c.Conn, 0x12, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func sqlSOCKSReadPacket(r io.Reader) (byte, []byte, error) {
	var payload []byte
	for {
		var header [8]byte
		if _, err := io.ReadFull(r, header[:]); err != nil {
			return 0, nil, err
		}
		length := int(binary.BigEndian.Uint16(header[2:])) - len(header)
		if length < 0 {
			return 0, nil, fmt.Errorf("invalid TDS packet length")
		}
		data := make([]byte, length)
		if _, err := io.ReadFull(r, data); err != nil {
			return 0, nil, err
		}
		payload = append(payload, data...)
		if header[1]&1 != 0 {
			return header[0], payload, nil
		}
	}
}

func sqlSOCKSWritePacket(w io.Writer, packetType byte, payload []byte) error {
	header := []byte{packetType, 1, 0, 0, 0, 0, 1, 0}
	binary.BigEndian.PutUint16(header[2:], uint16(len(header)+len(payload)))
	_, err := w.Write(append(header, payload...))
	return err
}

func sqlSOCKSDecodeUTF16(b []byte) string {
	words := make([]uint16, len(b)/2)
	for i := range words {
		words[i] = binary.LittleEndian.Uint16(b[2*i:])
	}
	return string(utf16.Decode(words))
}

func sqlSOCKSEncodeUTF16(s string) []byte {
	words := utf16.Encode([]rune(s))
	b := make([]byte, len(words)*2)
	for i, word := range words {
		binary.LittleEndian.PutUint16(b[2*i:], word)
	}
	return b
}
