package qlogviewer

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"

	"github.com/quic-go/quic-go/logging"
)

type Collector struct {
	mu      sync.RWMutex
	clients []chan<- []byte
}

func NewCollector() *Collector {
	return &Collector{}
}

func (c *Collector) Subscribe(ch chan<- []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clients = append(c.clients, ch)
}

func (c *Collector) Unsubscribe(ch chan<- []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, cl := range c.clients {
		if cl == ch {
			c.clients = append(c.clients[:i], c.clients[i+1:]...)
			return
		}
	}
}

// BroadcastEvent pushes an external event to all WebSocket clients.
func (c *Collector) BroadcastEvent(ev Event) {
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	c.mu.RLock()
	for _, ch := range c.clients {
		select {
		case ch <- data:
		default:
		}
	}
	c.mu.RUnlock()
}

func (c *Collector) broadcast(ev Event) {
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	c.mu.RLock()
	for _, ch := range c.clients {
		select {
		case ch <- data:
		default:
		}
	}
	c.mu.RUnlock()
}

func (c *Collector) TraceAll(_ logging.Perspective) *logging.ConnectionTracer {
	return &logging.ConnectionTracer{
		StartedConnection: func(local, remote net.Addr, srcConnID, destConnID logging.ConnectionID) {
			c.broadcast(Event{
				Time: msSinceRef(),
				Type: EventConnStarted,
				Payload: ConnInfo{
					LocalAddr:  local,
					RemoteAddr: remote,
					SrcConnID:  srcConnID.String(),
					DestConnID: destConnID.String(),
				},
			})
		},
		ClosedConnection: func(err error) {
			errStr := ""
			if err != nil {
				errStr = err.Error()
			}
			c.broadcast(Event{
				Time: msSinceRef(),
				Type: EventConnClosed,
				Payload: map[string]string{"error": errStr},
			})
		},
		SentLongHeaderPacket: func(hdr *logging.ExtendedHeader, size logging.ByteCount, ecn logging.ECN, ack *logging.AckFrame, frames []logging.Frame) {
			c.broadcast(Event{
				Time: msSinceRef(), Type: EventPacketSent, PathID: 0,
				Payload: PacketInfo{
					PacketNumber: int64(hdr.PacketNumber),
					Size:         int64(size),
					EncLevel:     hdr.Type.String(),
					FrameCount:   len(frames),
				},
			})
		},
		SentShortHeaderPacket: func(hdr *logging.ShortHeader, size logging.ByteCount, ecn logging.ECN, ack *logging.AckFrame, frames []logging.Frame) {
			c.broadcast(Event{
				Time: msSinceRef(), Type: EventPacketSent, PathID: 0,
				Payload: PacketInfo{
					PacketNumber: int64(hdr.PacketNumber),
					Size:         int64(size),
					EncLevel:     "1-RTT",
					FrameCount:   len(frames),
				},
			})
		},
		ReceivedLongHeaderPacket: func(hdr *logging.ExtendedHeader, size logging.ByteCount, ecn logging.ECN, frames []logging.Frame) {
			c.broadcast(Event{
				Time: msSinceRef(), Type: EventPacketRecv, PathID: 0,
				Payload: PacketInfo{
					PacketNumber: int64(hdr.PacketNumber),
					Size:         int64(size),
					EncLevel:     hdr.Type.String(),
					FrameCount:   len(frames),
				},
			})
		},
		ReceivedShortHeaderPacket: func(hdr *logging.ShortHeader, size logging.ByteCount, ecn logging.ECN, frames []logging.Frame) {
			c.broadcast(Event{
				Time: msSinceRef(), Type: EventPacketRecv, PathID: 0,
				Payload: PacketInfo{
					PacketNumber: int64(hdr.PacketNumber),
					Size:         int64(size),
					EncLevel:     "1-RTT",
					FrameCount:   len(frames),
				},
			})
		},
		UpdatedMetrics: func(rttStats *logging.RTTStats, cwnd, bytesInFlight logging.ByteCount, packetsInFlight int) {
			c.broadcast(Event{
				Time: msSinceRef(), Type: EventUpdatedMetrics, PathID: 0,
				Payload: MetricsInfo{
					SmoothedRTT:     float64(rttStats.SmoothedRTT().Microseconds()),
					CongestionWind:  int64(cwnd),
					BytesInFlight:   int64(bytesInFlight),
					PacketsInFlight: packetsInFlight,
				},
			})
		},
		AcknowledgedPacket: func(encLevel logging.EncryptionLevel, pn logging.PacketNumber) {
			c.broadcast(Event{
				Time: msSinceRef(), Type: EventPacketAcked, PathID: 0,
				Payload: map[string]interface{}{
					"pn": int64(pn), "enc_level": encLevel.String(),
				},
			})
		},
		LostPacket: func(encLevel logging.EncryptionLevel, pn logging.PacketNumber, lossReason logging.PacketLossReason) {
			c.broadcast(Event{
				Time: msSinceRef(), Type: EventPacketLost, PathID: 0,
				Payload: map[string]interface{}{
					"pn": int64(pn), "enc_level": encLevel.String(), "reason": fmt.Sprintf("%d", lossReason),
				},
			})
		},
		Debug: func(name, msg string) {
			c.broadcast(Event{
				Time: msSinceRef(), Type: "debug",
				Payload: map[string]string{"name": name, "msg": msg},
			})
		},
	}
}
