package multihoptun

import (
	"math/rand"
	"net"
	"sync"
	"sync/atomic"

	"golang.zx2c4.com/wireguard/conn"

	"gvisor.dev/gvisor/pkg/tcpip/header"
)

type multihopBind struct {
	*MultihopTun
	receiverWorkGroup *sync.WaitGroup
	shutdown          atomic.Bool
	socketShutdown    chan struct{}
}

// Close implements tun.Device
func (st *multihopBind) Close() error {
	st.shutdown.Store(true)
	select {
	case _, ok := <-st.socketShutdown:
		if !ok {
			break
		}
	default:
		close(st.socketShutdown)
	}
	st.receiverWorkGroup.Wait()
	return nil
}

// Open implements conn.Bind.
func (st *multihopBind) Open(port uint16) (fns []conn.ReceiveFunc, actualPort uint16, err error) {
	if port != 0 {
		st.localPort = port
	} else {
		st.localPort = uint16(rand.Uint32()>>16) | 1
	}
	st.socketShutdown = make(chan struct{})

	actualPort = st.localPort
	fns = []conn.ReceiveFunc{
		func(packets [][]byte, sizes []int, eps []conn.Endpoint) (n int, err error) {
			if st.shutdown.Load() {
				return 0, net.ErrClosed
			}
			st.receiverWorkGroup.Add(1)
			defer st.receiverWorkGroup.Done()

			var batch packetBatch
			var ok bool

			select {
			case _, _ = <-st.shutdownChan:
			case _, _ = <-st.socketShutdown:
				return 0, net.ErrClosed
			case batch, ok = <-st.writeRecv:
				break
			}
			if !ok {
				return 0, net.ErrClosed
			}

			packetsToProcess := len(packets)
			if len(batch.packets) < packetsToProcess {
				packetsToProcess = len(batch.packets)
			}

			for idx := 0; idx < packetsToProcess; idx += 1 {
				rxPacket := batch.packets[idx][batch.offset:]
				ipVersion := header.IPVersion(rxPacket)
				if ipVersion == 4 {
					var v4 header.IPv4
					var udp header.UDP
					v4 = rxPacket
					udp = v4.Payload()
					copy(packets[idx], udp.Payload())
					sizes[idx] = len(udp.Payload())

				} else if ipVersion == 6 {
					var v6 header.IPv6
					var udp header.UDP
					v6 = rxPacket
					udp = v6.Payload()
					copy(packets[idx], udp.Payload())
					sizes[idx] = len(udp.Payload())
				}

				eps[idx] = st.endpoint
				n += 1
			}
			batch.packetsCopied = n
			batch.completion <- batch
			return
		},
	}
	// since a bind instance can be closed and reopened all the time, whenver it
	// is opened, the state should be updated again.
	st.shutdown.Store(false)

	return fns, actualPort, nil
}

// ParseEndpoint implements conn.Bind.
func (*multihopBind) ParseEndpoint(s string) (conn.Endpoint, error) {
	return conn.NewStdNetBind().ParseEndpoint(s)
}

// Send implements conn.Bind.
func (st *multihopBind) Send(bufs [][]byte, ep conn.Endpoint) error {
	st.receiverWorkGroup.Add(1)
	defer st.receiverWorkGroup.Done()

	var packetBatch packetBatch
	var ok bool

	select {
	case _, _ = <-st.shutdownChan:
	case _, _ = <-st.socketShutdown:
		// it is important to return a net.ErrClosed, since it implements the
		// net.Error interface and indicates that it is not a recoverable error.
		// wg-go uses the net.Error interface to deduce if it should try to send
		// packets again after some time or if it should give up.
		return net.ErrClosed
	case packetBatch, ok = <-st.readRecv:
		break
	}

	if !ok {
		return net.ErrClosed
	}

	packetBatch.packetsCopied = 0
	for idx := range bufs {
		targetPacket := packetBatch.packets[idx][packetBatch.offset:]
		size, err := st.writePayload(targetPacket[:], bufs[idx])
		if err != nil {
			continue
		}
		packetBatch.sizes[idx] = size
		packetBatch.packetsCopied += 1
	}

	packetBatch.completion <- packetBatch
	return nil
}

// SetMark implements conn.Bind.
func (*multihopBind) SetMark(mark uint32) error {
	return nil
}
