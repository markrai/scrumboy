package safehttp

import "net"

// IsForbiddenIP reports whether ip is unsafe as an outbound destination.
// Loopback, private (RFC1918 and IPv6 ULA), link-local, multicast, unspecified,
// IPv4-mapped forms of those ranges, and additional special-purpose nets are
// forbidden. A nil IP is forbidden.
func IsForbiddenIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() ||
		blockedSpecialPurposeIP(ip)
}

var blockedSpecialPurposeNets = mustParseCIDRs(
	"100.64.0.0/10", // CGNAT / shared address space (RFC 6598)
	"0.0.0.0/8",     // this network
	"192.0.0.0/24",  // IETF protocol assignments
	"192.0.2.0/24",  // TEST-NET-1
	"198.51.100.0/24",
	"203.0.113.0/24",
	"198.18.0.0/15", // benchmarking
	"240.0.0.0/4",   // reserved
	"255.255.255.255/32",
	"64:ff9b::/96",  // NAT64
	"100::/64",      // discard-only
	"2001:db8::/32", // documentation
	"2002::/16",     // 6to4
	"fec0::/10",     // deprecated site-local
)

func mustParseCIDRs(cidrs ...string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(err)
		}
		out = append(out, network)
	}
	return out
}

func blockedSpecialPurposeIP(ip net.IP) bool {
	for _, network := range blockedSpecialPurposeNets {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
