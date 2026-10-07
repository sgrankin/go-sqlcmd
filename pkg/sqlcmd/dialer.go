// Copyright (c) Microsoft Corporation.
// Licensed under the MIT license.

package sqlcmd

import (
	"context"
	"database/sql/driver"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"

	mssql "github.com/microsoft/go-mssqldb"
	"github.com/microsoft/go-mssqldb/msdsn"
	"golang.org/x/net/proxy"
)

// proxyDialer marks the connection as hostname-aware so the driver leaves DNS
// to the transport. The DSN retains the hostname used for TLS and SQL login.
type proxyDialer struct {
	serverName string
	serverPort string
	targetHost string
	targetPort string
	dialer     mssql.Dialer
}

func (d *proxyDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return d.dialer.DialContext(ctx, network, d.dialAddress(network, addr))
}

func (d *proxyDialer) HostName() string {
	return d.serverName
}

func (d *proxyDialer) dialAddress(network, addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || !strings.EqualFold(host, d.serverName) {
		return addr
	}
	// Redirects must use their routed destination, even when the hostname is
	// unchanged and only the port differs from the initial connection.
	if isTCPNetwork(network) && d.serverPort != "" && port != d.serverPort {
		return addr
	}
	if d.targetHost != "" {
		host = d.targetHost
	}
	if d.targetPort != "" && isTCPNetwork(network) {
		port = d.targetPort
	}
	return net.JoinHostPort(host, port)
}

func isTCPNetwork(network string) bool {
	return network == "tcp" || network == "tcp4" || network == "tcp6"
}

func proxyEnvironment(upper, lower string) string {
	if value := os.Getenv(upper); value != "" {
		return value
	}
	return os.Getenv(lower)
}

func configureConnectionDialer(connector driver.Connector, connect *ConnectSettings) error {
	transport, err := socksDialerFromEnvironment()
	if err != nil {
		return err
	}
	server, _, port, protocol, err := splitServer(connect.ServerName)
	if err != nil {
		return err
	}
	if server == "" {
		server = "."
	}
	override := connect.useServerNameOverride(protocol, connect.ServerName)
	if transport == nil && !override {
		return nil
	}
	if transport == nil {
		transport = &net.Dialer{}
	}
	connstr, err := connect.ConnectionString()
	if err != nil {
		return err
	}
	config, err := msdsn.Parse(connstr)
	if err != nil {
		return err
	}
	// Match the driver's normalized logical destination, including local
	// aliases and administrator ports. Named-instance ports are discovered
	// after parsing, so they cannot be constrained here.
	d := &proxyDialer{serverName: config.Host, dialer: transport}
	if config.Port > 0 {
		d.serverPort = strconv.FormatUint(config.Port, 10)
	} else if config.Instance == "" {
		d.serverPort = "1433"
	}
	if override {
		d.targetHost = server
		if port > 0 {
			d.targetPort = strconv.FormatUint(port, 10)
		}
	}
	c, ok := connector.(*mssql.Connector)
	if !ok {
		return fmt.Errorf("custom SQL dialing is not supported with the current authentication method")
	}
	c.Dialer = d
	return nil
}

type socksDialer struct {
	proxy   proxy.ContextDialer
	direct  net.Dialer
	noProxy []noProxyRule
}

func socksDialerFromEnvironment() (mssql.Dialer, error) {
	value := proxyEnvironment("ALL_PROXY", "all_proxy")
	if value == "" {
		return nil, nil
	}
	u, err := url.Parse(value)
	// Do not include the URL in errors: it may contain proxy credentials.
	if err != nil {
		return nil, fmt.Errorf("invalid ALL_PROXY URL")
	}
	if u.Scheme != "socks5" && u.Scheme != "socks5h" {
		return nil, fmt.Errorf("ALL_PROXY must use socks5:// or socks5h://")
	}
	if u.Hostname() == "" || u.Opaque != "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, fmt.Errorf("invalid ALL_PROXY URL: expected a proxy host and optional port")
	}
	if strings.Contains(u.Hostname(), ":") && net.ParseIP(u.Hostname()) == nil {
		return nil, fmt.Errorf("invalid ALL_PROXY host")
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("invalid ALL_PROXY port")
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return nil, fmt.Errorf("invalid ALL_PROXY port")
	}
	if u.User != nil {
		password, _ := u.User.Password()
		if len(u.User.Username()) == 0 || len(u.User.Username()) > 255 || len(password) > 255 {
			return nil, fmt.Errorf("invalid ALL_PROXY credentials: SOCKS5 requires a username of 1-255 bytes and a password of at most 255 bytes")
		}
	}
	d := &socksDialer{noProxy: parseNoProxy(proxyEnvironment("NO_PROXY", "no_proxy"))}
	p, err := proxy.FromURL(u, &d.direct)
	if err != nil {
		return nil, fmt.Errorf("could not configure SOCKS5 proxy: %w", err)
	}
	// The supported SOCKS5 transport implements cancellation for both the
	// connection to the proxy and its negotiation; never use a Dial fallback.
	d.proxy = p.(proxy.ContextDialer)
	return d, nil
}

func (d *socksDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	for _, rule := range d.noProxy {
		if rule.matches(host, port) {
			return d.direct.DialContext(ctx, network, addr)
		}
	}
	if !isTCPNetwork(network) {
		return nil, fmt.Errorf("SOCKS5 SQL connections require TCP and an explicit port for named instances (cannot proxy %s)", network)
	}
	return d.proxy.DialContext(ctx, network, addr)
}

type noProxyRule struct {
	host   string
	port   string
	prefix netip.Prefix
}

func parseNoProxy(value string) []noProxyRule {
	var rules []noProxyRule
	for entry := range strings.SplitSeq(value, ",") {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(entry); err == nil {
			rules = append(rules, noProxyRule{prefix: prefix})
			continue
		}
		host, port, err := net.SplitHostPort(entry)
		if err != nil {
			host, port = entry, ""
		}
		host = strings.TrimPrefix(host, "*.")
		if strings.HasPrefix(entry, "*.") {
			host = "." + host
		}
		rules = append(rules, noProxyRule{host: host, port: port})
	}
	return rules
}

func (r noProxyRule) matches(host, port string) bool {
	if r.port != "" && r.port != port {
		return false
	}
	if r.host == "*" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	if r.prefix.IsValid() {
		return err == nil && r.prefix.Contains(ip)
	}
	if ruleIP, ruleErr := netip.ParseAddr(r.host); ruleErr == nil {
		return err == nil && ruleIP == ip
	}
	if err == nil {
		return false
	}
	host = strings.ToLower(host)
	if strings.HasPrefix(r.host, ".") {
		return strings.HasSuffix(host, r.host)
	}
	return host == r.host || strings.HasSuffix(host, "."+r.host)
}
