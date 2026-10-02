// Package netguard is the SSRF guard (spec §5.3/§9): a host is accepted only if
// every address it resolves to is public (no loopback, private, link-local,
// CGNAT, ULA, multicast or unspecified ranges). Checked after DNS resolution.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
)

// ErrForbidden means the host resolves to a non-public address.
var ErrForbidden = errors.New("host resolves to a non-public address")

// Resolver is net.Resolver's lookup (replaceable in tests).
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Guard validates hosts.
type Guard struct {
	R Resolver
}

// New returns a guard using the system resolver.
func New() *Guard { return &Guard{R: net.DefaultResolver} }

var blocked = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // CGNAT
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("64:ff9b::/96"), // NAT64 may map to private v4
}

// Public reports whether an address is globally routable.
func Public(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() || a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsMulticast() || a.IsUnspecified() || a.IsInterfaceLocalMulticast() {
		return false
	}
	for _, p := range blocked {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

// Check resolves host and rejects it unless all addresses are public.
func (g *Guard) Check(ctx context.Context, host string) error {
	if ip, err := netip.ParseAddr(host); err == nil {
		if !Public(ip) {
			return ErrForbidden
		}
		return nil
	}
	addrs, err := g.R.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", host, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("resolve %s: no addresses", host)
	}
	for _, a := range addrs {
		if !Public(a) {
			return ErrForbidden
		}
	}
	return nil
}
