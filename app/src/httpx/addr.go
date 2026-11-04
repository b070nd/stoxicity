package httpx

import (
	"fmt"
	"net/netip"
	"syscall"
)

// Ranges that net/netip does not classify but a fetcher must never reach:
// "this network", carrier-grade NAT, the reserved class E block (which holds
// the broadcast address), and the NAT64 prefix, whose low 32 bits carry an
// IPv4 address that is checked again.
var (
	thisNetwork = netip.MustParsePrefix("0.0.0.0/8")
	carrierNAT  = netip.MustParsePrefix("100.64.0.0/10")
	reservedE   = netip.MustParsePrefix("240.0.0.0/4")
	nat64       = netip.MustParsePrefix("64:ff9b::/96")
)

// addrError reports an address the client refuses to connect to.
type addrError struct{ addr string }

func (e *addrError) Error() string { return "address " + e.addr + " is not allowed" }

// isDeniedAddr reports whether a is loopback, private (RFC 1918, fc00::/7),
// link-local (169.254.0.0/16, fe80::/10, which holds the cloud metadata
// address), unspecified, multicast, carrier-grade NAT or otherwise not a
// public unicast address. IPv4-mapped IPv6 forms are judged as IPv4.
func isDeniedAddr(a netip.Addr) bool {
	a = a.WithZone("").Unmap()
	if !a.IsValid() {
		return true
	}
	if a.Is6() && nat64.Contains(a) {
		b := a.As16()
		return isDeniedAddr(netip.AddrFrom4([4]byte(b[12:])))
	}
	return a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() ||
		a.IsLinkLocalMulticast() || a.IsInterfaceLocalMulticast() || a.IsMulticast() ||
		a.IsUnspecified() || thisNetwork.Contains(a) || carrierNAT.Contains(a) || reservedE.Contains(a)
}

// denyPrivate is a net.Dialer Control function. It sees the IP address that
// is about to be connected to, after DNS resolution, so a name that resolves
// to a private address (DNS rebinding) is refused just like a literal one.
func denyPrivate(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return &addrError{addr: fmt.Sprintf("%q", address)}
	}
	if isDeniedAddr(ap.Addr()) {
		return &addrError{addr: ap.Addr().String()}
	}
	return nil
}
