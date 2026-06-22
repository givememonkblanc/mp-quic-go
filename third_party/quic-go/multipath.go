package quic

import (
	"net"

	"github.com/quic-go/quic-go/internal/ackhandler"
	"github.com/quic-go/quic-go/internal/protocol"
	"github.com/quic-go/quic-go/internal/utils"
	"github.com/quic-go/quic-go/logging"
)

// PathID identifies a path in a multipath QUIC connection.
type PathID = uint32

// PathSelector is used by the connection to decide which path
// to use for sending non-probe, non-control packets.
type PathSelector interface {
	// SelectPath returns the path ID to use for the next outgoing packet.
	// It is called once for each packet in the send loop.
	// Implementations should be fast (no blocking I/O).
	SelectPath(paths []PathState) PathID

	// Name returns a human-readable name for the selector.
	Name() string
}

// PathState is a snapshot of a path's state for the selector's decision.
type PathState struct {
	ID        PathID
	Available bool
	RSSI      int
	HasRSSI   bool
}

// pathSendQueue wraps a sendConn and sendQueue together for one path.
type pathSendQueue struct {
	id    PathID
	conn  sendConn
	queue sender
}

func newPathSendQueue(id PathID, conn sendConn) *pathSendQueue {
	return &pathSendQueue{
		id:    id,
		conn:  conn,
		queue: newSendQueue(conn),
	}
}

func (q *pathSendQueue) Send(buf *packetBuffer, gsoSize uint16, ecn protocol.ECN) {
	q.queue.Send(buf, gsoSize, ecn)
}

func (q *pathSendQueue) WouldBlock() bool {
	return q.queue.WouldBlock()
}

// PathHandler bundles the per-path state needed for independent send on a path.
type PathHandler struct {
	ID      PathID
	SendQ   *pathSendQueue
	SentPH  ackhandler.SentPacketHandler
	RecvPH  ackhandler.ReceivedPacketHandler
	RTTStats *utils.RTTStats
}

// NewPathHandler creates a new PathHandler with its own packet number space,
// send queue, and congestion controller.
//
// srcInfo carries the local (source) address that outgoing packets on this path
// must be sent from. This matters on the server side, where the underlying
// socket is bound to a wildcard address: without an explicit source address the
// kernel picks the primary local IP for every path, so replies on an alternate
// path would egress from the wrong address and the peer's path validation would
// never complete. Pass an empty packetInfo{} to let the OS choose the source
// (correct for the client, which routes by destination from a single socket).
func NewPathHandler(
	id PathID,
	rawConn rawConn,
	remoteAddr net.Addr,
	srcInfo packetInfo,
	initialPacketNumber protocol.PacketNumber,
	initialMaxDatagramSize protocol.ByteCount,
	rttStats *utils.RTTStats,
	clientAddressValidated bool,
	enableECN bool,
	pers protocol.Perspective,
	tracer *logging.ConnectionTracer,
	logger utils.Logger,
) (*PathHandler, error) {
	sendConn := newSendConn(rawConn, remoteAddr, srcInfo, logger)
	sentPH, recvPH := ackhandler.NewAckHandler(
		initialPacketNumber,
		initialMaxDatagramSize,
		rttStats,
		clientAddressValidated,
		enableECN,
		pers,
		tracer,
		logger,
	)
	return &PathHandler{
		ID:       id,
		SendQ:    newPathSendQueue(id, sendConn),
		SentPH:   sentPH,
		RecvPH:   recvPH,
		RTTStats: rttStats,
	}, nil
}

// Close closes the path's send queue.
func (h *PathHandler) Close() {
	h.SendQ.queue.Close()
}

// RunSendQueue starts the send queue's run loop.
func (h *PathHandler) RunSendQueue() error {
	return h.SendQ.queue.Run()
}
