package lan

import "net/netip"

// IsLANAddress 限制设备发现及控制的出站目的地址。
func IsLANAddress(ip netip.Addr) bool {
	return ip.Is4() && (ip.IsPrivate() || ip.IsLinkLocalUnicast())
}
