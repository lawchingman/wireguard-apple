package main

import (
	"bufio"
	"net/netip"
	"strings"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
)

type NetstackBind struct {
	*netstack.Net
	socket         *gonet.UDPConn
	localEndpoint  netip.AddrPort
	remoteEndpoint netip.AddrPort
	logger         *device.Logger
}

type NetstackBindAddr struct {
	remoteEndpoint *netip.AddrPort
	localEndpoint  *netip.AddrPort
}

// ClearSrc implements conn.Endpoint.
func (a *NetstackBindAddr) ClearSrc() {
	a.localEndpoint = nil
}

// DstIP implements conn.Endpoint.
func (a *NetstackBindAddr) DstIP() netip.Addr {
	return a.remoteEndpoint.Addr()
}

// DstToBytes implements conn.Endpoint.
func (a *NetstackBindAddr) DstToBytes() []byte {
	b, _ := a.remoteEndpoint.MarshalBinary()
	return b
}

// DstToString implements conn.Endpoint.
func (a *NetstackBindAddr) DstToString() string {
	return a.remoteEndpoint.String()
}

// SrcIP implements conn.Endpoint.
func (a *NetstackBindAddr) SrcIP() netip.Addr {
	if a.localEndpoint == nil {
		if a.remoteEndpoint != nil && a.remoteEndpoint.Addr().Is6() {
			return netip.IPv6Unspecified()
		} else {
			return netip.IPv4Unspecified()
		}
	}
	return a.localEndpoint.Addr()
}

// SrcToString implements conn.Endpoint.
func (a *NetstackBindAddr) SrcToString() string {
	return a.localEndpoint.String()
}

func NewNetstackBind(net *netstack.Net, localAddr netip.AddrPort, peerEndpoint netip.AddrPort, logger *device.Logger) NetstackBind {
	return NetstackBind{
		net,
		nil,
		localAddr,
		peerEndpoint,
		logger,
	}

}

// BatchSize implements conn.Bind.
func (*NetstackBind) BatchSize() int {
	return 128
}

// Close implements conn.Bind.
func (b *NetstackBind) Close() error {
	if b.socket != nil {
		return b.socket.Close()
	}
	return nil
}

// Open implements conn.Bind.
func (b *NetstackBind) Open(port uint16) (fns []conn.ReceiveFunc, actualPort uint16, err error) {

	listener, err := b.DialUDPAddrPort(b.localEndpoint, b.remoteEndpoint)
	if err != nil {
		return []conn.ReceiveFunc{}, 0, err
	}

	b.socket = listener

	recvFunc := func(packets [][]byte, sizes []int, eps []conn.Endpoint) (n int, err error) {
		if len(packets) == 0 {
			return
		}

		bytesRead, err := listener.Read(packets[0])
		if err != nil {
			return 0, err
		}

		eps[0] = &NetstackBindAddr{remoteEndpoint: &b.remoteEndpoint, localEndpoint: &b.localEndpoint}
		sizes[0] = bytesRead
		if err == nil {
			n = 1
		}

		return
	}

	return []conn.ReceiveFunc{recvFunc}, uint16(b.localEndpoint.Port()), nil
}

// ParseEndpoint implements conn.Bind.
func (*NetstackBind) ParseEndpoint(s string) (conn.Endpoint, error) {
	addr, err := netip.ParseAddrPort(s)
	if err != nil {
		return nil, err
	}

	return &NetstackBindAddr{remoteEndpoint: &addr}, nil
}

// Send implements conn.Bind.
// Endpoint argument is ignored, the endpoint is known ahead of time anyway.
func (b *NetstackBind) Send(bufs [][]byte, ep conn.Endpoint) (err error) {
	for idx := range bufs {
		_, err = b.socket.Write(bufs[idx])
		if err != nil {
			return
		}
	}
	return

}

// SetMark implements conn.Bind.
func (*NetstackBind) SetMark(mark uint32) error {
	return nil
}

// Parse a wireguard config and return the first endpoint address it finds and
// parses successfully.
func parseEndpointFromGoConfig(config string) *netip.AddrPort {
	scanner := bufio.NewScanner(strings.NewReader(config))
	for scanner.Scan() {
		line := scanner.Text()
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		if key == "endpoint" {
			endpoint, err := netip.ParseAddrPort(value)
			if err == nil {
				return &endpoint
			}
		}

	}
	return nil
}
