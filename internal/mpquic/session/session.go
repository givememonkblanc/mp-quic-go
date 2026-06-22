package session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/quic-go/quic-go"
	"mp-quic-go/internal/conn"
	"mp-quic-go/internal/mpquic/cid"
	"mp-quic-go/internal/mpquic/frame"
	"mp-quic-go/internal/mpquic/path"
	"mp-quic-go/internal/mpquic/scheduler"
	"mp-quic-go/internal/mpquic/transport"
	"mp-quic-go/internal/rssi"
)

type Manager struct {
	logger       *slog.Logger
	streams      *conn.StreamHandler
	params       transport.Parameters
	scheduler    scheduler.Scheduler
	rssi         rssi.Provider
	frameTypes   []frame.Type
	interfaceMap map[string]path.ID
	ifaceByPath  map[path.ID]string
}

func NewManager(logger *slog.Logger, streams *conn.StreamHandler, params transport.Parameters, sched scheduler.Scheduler, rssiProvider rssi.Provider) (*Manager, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}

	return &Manager{
		logger:       logger,
		streams:      streams,
		params:       params,
		scheduler:    sched,
		rssi:         rssiProvider,
		frameTypes:   frame.All(),
		interfaceMap: make(map[string]path.ID),
		ifaceByPath:  make(map[path.ID]string),
	}, nil
}

// Scheduler returns the path scheduler used by this manager.
func (m *Manager) Scheduler() (scheduler.Scheduler, bool) {
	return m.scheduler, m.scheduler != nil
}

// SetInterfaceMapping configures which wireless interface maps to which path ID.
// This mapping is inherited by all sessions created by this Manager.
// Interfaces discovered at runtime via the RSSI provider that are not
// explicitly mapped here will be auto-assigned.
func (m *Manager) SetInterfaceMapping(iface string, pathID path.ID) {
	m.interfaceMap[iface] = pathID
	m.ifaceByPath[pathID] = iface
}

type Session struct {
	logger        *slog.Logger
	conn          quic.Connection
	paths         *path.Manager
	connIDs       *cid.Registry
	params        transport.Parameters
	peerMaxPathID uint32
	scheduler     scheduler.Scheduler
	rssi          rssi.Provider
	streams       *conn.StreamHandler
	frameTypes    []frame.Type
	interfaceMap  map[string]path.ID // interface name -> path ID
	ifaceByPath   map[path.ID]string // reverse: path ID -> interface name
}

func New(logger *slog.Logger, streams *conn.StreamHandler, params transport.Parameters, sched scheduler.Scheduler, rssiProvider rssi.Provider) (*Session, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	return &Session{
		logger:        logger,
		paths:         path.NewManager(params.MaxPathID),
		connIDs:       cid.NewRegistry(),
		params:        params,
		peerMaxPathID: params.InitialMaxPathID,
		scheduler:     sched,
		rssi:          rssiProvider,
		streams:       streams,
		frameTypes:    frame.All(),
		interfaceMap:  make(map[string]path.ID),
		ifaceByPath:   make(map[path.ID]string),
	}, nil
}

func (s *Session) Run(ctx context.Context) error {
	initialPath := s.paths.EnsureInitialPath(s.conn.RemoteAddr().String())
	if s.rssi != nil {
		if err := s.refreshPathRSSI(ctx); err != nil {
			s.logger.Warn("failed to fetch initial path rssi", "error", err, "source", s.rssi.Source())
		}
		go s.pollRSSI(ctx)
	}
	selectedPath, ok := s.scheduler.SelectPath(s.paths.Snapshot())
	if !ok {
		return fmt.Errorf("no schedulable path available")
	}

	s.logger.Info(
		"mp-quic session initialized",
		"peer", s.conn.RemoteAddr(),
		"initial_path_id", initialPath.ID,
		"selected_path_id", selectedPath.ID,
		"selected_path_rssi", selectedPath.RSSI,
		"scheduler", s.scheduler.Name(),
		"initial_max_path_id", s.params.InitialMaxPathID,
		"supported_frames", len(s.frameTypes),
	)

	for {
		stream, err := s.conn.AcceptStream(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return fmt.Errorf("accept stream: %w", err)
		}

		go func(stream quic.Stream) {
			if err := s.streams.Handle(ctx, stream); err != nil {
				s.logger.Error("failed to handle stream", "error", err, "stream_id", stream.StreamID())
			}
		}(stream)
	}
}

func (m *Manager) NewSession() (*Session, error) {
	s, err := New(m.logger, m.streams, m.params, m.scheduler, m.rssi)
	if err != nil {
		return nil, err
	}
	// Copy interface mappings from the manager so they are inherited by all sessions.
	for k, v := range m.interfaceMap {
		s.interfaceMap[k] = v
		s.ifaceByPath[v] = k
	}
	return s, nil
}

func (m *Manager) Handle(ctx context.Context, qc quic.Connection) error {
	session, err := m.NewSession()
	if err != nil {
		return err
	}
	session.conn = qc
	return session.Run(ctx)
}

func (s *Session) Bootstrap(peerAddr string) path.State {
	return s.paths.EnsureInitialPath(peerAddr)
}

func (s *Session) OpenPath(remoteAddr string) (path.State, []frame.Frame, error) {
	nextPathID, ok := s.paths.NextUnusedPathID()
	if !ok || uint32(nextPathID) > s.peerMaxPathID {
		return path.State{}, []frame.Frame{frame.PathsBlockedFrame{MaximumPathID: uint64(s.peerMaxPathID)}}, fmt.Errorf("peer max path id limit reached")
	}
	if !s.connIDs.HasUsableRemote(nextPathID) {
		return path.State{}, []frame.Frame{frame.PathCIDsBlockedFrame{PathID: uint64(nextPathID), NextSequenceNumber: s.connIDs.NextRemoteSequence(nextPathID)}}, fmt.Errorf("no remote connection id available for path %d", nextPathID)
	}
	state, err := s.paths.RegisterPath(nextPathID, remoteAddr)
	if err != nil {
		return path.State{}, nil, err
	}
	state, err = s.paths.SetLocalStatus(nextPathID, path.StatusAvailable)
	if err != nil {
		return path.State{}, nil, err
	}
	return state, []frame.Frame{frame.PathStatusAvailableFrame{PathID: uint64(nextPathID), SequenceNumber: state.LocalStatusSeq}}, nil
}

func (s *Session) SetPathAvailable(pathID path.ID) (frame.PathStatusAvailableFrame, error) {
	state, err := s.paths.SetLocalStatus(pathID, path.StatusAvailable)
	if err != nil {
		return frame.PathStatusAvailableFrame{}, err
	}
	return frame.PathStatusAvailableFrame{PathID: uint64(pathID), SequenceNumber: state.LocalStatusSeq}, nil
}

func (s *Session) SetPathBackup(pathID path.ID) (frame.PathStatusBackupFrame, error) {
	state, err := s.paths.SetLocalStatus(pathID, path.StatusBackup)
	if err != nil {
		return frame.PathStatusBackupFrame{}, err
	}
	return frame.PathStatusBackupFrame{PathID: uint64(pathID), SequenceNumber: state.LocalStatusSeq}, nil
}

func (s *Session) UpdatePathRSSI(pathID path.ID, rssi int) (path.State, error) {
	return s.paths.UpdateRSSI(pathID, rssi)
}

func (s *Session) refreshPathRSSI(ctx context.Context) error {
	if s.rssi == nil {
		return nil
	}
	rssiMap, err := s.rssi.FetchRSSI(ctx)
	if err != nil {
		return err
	}
	for iface, rssi := range rssiMap {
		pathID, ok := s.interfaceMap[iface]
		if !ok {
			// Auto-map: first unmapped interface gets the next available path ID.
			// For the common single-path case this maps to path 0.
			pathID = path.ID(len(s.interfaceMap))
			s.interfaceMap[iface] = pathID
			s.ifaceByPath[pathID] = iface
		}
		if _, exists := s.paths.Get(pathID); exists {
			if _, err := s.UpdatePathRSSI(pathID, rssi); err != nil {
				s.logger.Warn("failed to update rssi", "path", pathID, "iface", iface, "error", err)
			}
			// Push RSSI to the fork's connection for PathSelector consumption.
			if s.conn != nil {
				if err := s.conn.UpdatePathRSSI(quic.PathID(pathID), rssi); err != nil {
					s.logger.Warn("failed to push rssi to fork", "path", pathID, "error", err)
				}
			}
		}
	}
	return nil
}

// InterfaceForPath returns the interface name mapped to a path, if any.
func (s *Session) InterfaceForPath(pathID path.ID) (string, bool) {
	iface, ok := s.ifaceByPath[pathID]
	return iface, ok
}

func (s *Session) pollRSSI(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.refreshPathRSSI(ctx); err != nil {
				s.logger.Warn("failed to refresh path rssi", "error", err, "source", s.rssi.Source())
			}
		}
	}
}

func (s *Session) AbandonPath(pathID path.ID, errorCode uint64) (frame.PathAbandonFrame, error) {
	if err := s.paths.Abandon(pathID); err != nil {
		return frame.PathAbandonFrame{}, err
	}
	s.connIDs.RetirePath(pathID)
	return frame.PathAbandonFrame{PathID: uint64(pathID), ErrorCode: errorCode}, nil
}

func (s *Session) IssueLocalConnectionID(pathID path.ID, connectionID []byte, token [16]byte, retirePriorTo uint64) (frame.PathNewConnectionIDFrame, error) {
	entry, err := s.connIDs.IssueLocal(pathID, connectionID, token, retirePriorTo)
	if err != nil {
		return frame.PathNewConnectionIDFrame{}, err
	}
	return frame.PathNewConnectionIDFrame{
		PathID:              uint64(pathID),
		SequenceNumber:      entry.SequenceNumber,
		RetirePriorTo:       retirePriorTo,
		ConnectionID:        entry.ConnectionID,
		StatelessResetToken: entry.StatelessResetToken,
		Retired:             entry.Retired,
	}, nil
}

func (s *Session) HandleFrame(f frame.Frame) ([]frame.Frame, error) {
	switch incoming := f.(type) {
	case frame.MaxPathIDFrame:
		if incoming.MaximumPathID < uint64(s.params.InitialMaxPathID) {
			return nil, fmt.Errorf("received invalid max_path_id %d below initial_max_path_id %d", incoming.MaximumPathID, s.params.InitialMaxPathID)
		}
		if incoming.MaximumPathID > uint64(s.peerMaxPathID) {
			s.peerMaxPathID = uint32(incoming.MaximumPathID)
		}
		return nil, nil
	case frame.PathNewConnectionIDFrame:
		if err := s.connIDs.RegisterRemote(path.ID(incoming.PathID), incoming.SequenceNumber, incoming.RetirePriorTo, incoming.ConnectionID, incoming.StatelessResetToken); err != nil {
			return nil, err
		}
		return nil, nil
	case frame.PathRetireConnectionIDFrame:
		if err := s.connIDs.RetireLocal(path.ID(incoming.PathID), incoming.SequenceNumber); err != nil {
			return nil, err
		}
		return nil, nil
	case frame.PathStatusAvailableFrame:
		_, _, err := s.paths.ApplyPeerStatus(path.ID(incoming.PathID), path.StatusAvailable, incoming.SequenceNumber)
		return nil, err
	case frame.PathStatusBackupFrame:
		_, _, err := s.paths.ApplyPeerStatus(path.ID(incoming.PathID), path.StatusBackup, incoming.SequenceNumber)
		return nil, err
	case frame.PathAbandonFrame:
		response, err := s.AbandonPath(path.ID(incoming.PathID), incoming.ErrorCode)
		if err != nil {
			return nil, err
		}
		return []frame.Frame{response}, nil
	case frame.PathsBlockedFrame:
		if incoming.MaximumPathID > uint64(s.params.MaxPathID) {
			return nil, fmt.Errorf("received blocked maximum path id %d above local max %d", incoming.MaximumPathID, s.params.MaxPathID)
		}
		return nil, nil
	case frame.PathCIDsBlockedFrame:
		if incoming.PathID > uint64(s.params.MaxPathID) {
			return nil, fmt.Errorf("received path_cids_blocked path id %d above local max %d", incoming.PathID, s.params.MaxPathID)
		}
		if incoming.NextSequenceNumber > s.connIDs.NextLocalSequence(path.ID(incoming.PathID)) {
			return nil, fmt.Errorf("received next sequence %d above local next sequence", incoming.NextSequenceNumber)
		}
		return nil, nil
	case frame.PathACKFrame:
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported incoming frame %T", incoming)
	}
}
