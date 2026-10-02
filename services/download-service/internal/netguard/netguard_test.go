package netguard

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

type fakeResolver map[string][]netip.Addr

func (f fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	a, ok := f[host]
	if !ok {
		return nil, errors.New("no such host")
	}
	return a, nil
}

func addrs(s ...string) []netip.Addr {
	out := make([]netip.Addr, 0, len(s))
	for _, v := range s {
		out = append(out, netip.MustParseAddr(v))
	}
	return out
}

func TestPublic(t *testing.T) {
	for ip, want := range map[string]bool{
		"8.8.8.8": true, "2a00:1450:4001::200e": true, "127.0.0.1": false, "10.1.2.3": false, "172.16.0.1": false,
		"192.168.1.1": false, "169.254.169.254": false, "100.64.0.1": false, "0.0.0.0": false, "::1": false,
		"fc00::1": false, "fe80::1": false, "224.0.0.1": false, "::ffff:10.0.0.1": false, "198.18.0.1": false, "64:ff9b::a00:1": false,
	} {
		if got := Public(netip.MustParseAddr(ip)); got != want {
			t.Errorf("Public(%s) = %v", ip, got)
		}
	}
	if Public(netip.Addr{}) {
		t.Error("invalid address")
	}
}

func TestCheck(t *testing.T) {
	g := &Guard{R: fakeResolver{
		"youtube.com": addrs("142.250.1.1", "2a00:1450::1"),
		"evil.test":   addrs("93.184.216.34", "10.0.0.5"),
		"empty.test":  {},
	}}
	ctx := context.Background()
	if err := g.Check(ctx, "youtube.com"); err != nil {
		t.Fatal(err)
	}
	if err := g.Check(ctx, "evil.test"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("mixed = %v", err)
	}
	if err := g.Check(ctx, "127.0.0.1"); !errors.Is(err, ErrForbidden) {
		t.Fatal("loopback literal")
	}
	if err := g.Check(ctx, "1.1.1.1"); err != nil {
		t.Fatal(err)
	}
	if err := g.Check(ctx, "missing.test"); err == nil || errors.Is(err, ErrForbidden) {
		t.Fatalf("resolve error = %v", err)
	}
	if err := g.Check(ctx, "empty.test"); err == nil {
		t.Fatal("no addresses")
	}
	if New().R == nil {
		t.Fatal("default resolver")
	}
}
