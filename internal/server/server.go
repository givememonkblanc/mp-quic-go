package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"path/filepath"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/qlog"
	"mp-quic-go/internal/conn"
	"mp-quic-go/internal/handler"
	"mp-quic-go/internal/mpquic/scheduler"
	mpquicsession "mp-quic-go/internal/mpquic/session"
	"mp-quic-go/internal/mpquic/transport"
	"mp-quic-go/internal/rssi"
	"mp-quic-go/pkg/protocols"
)

type Config struct {
	ListenAddr       string        `mapstructure:"listen_addr"`
	MaxIdleTimeout   time.Duration `mapstructure:"max_idle_timeout"`
	MaxStreamNum     int           `mapstructure:"max_stream_num"`
	InitialMaxPathID uint32        `mapstructure:"initial_max_path_id"`
	MaxPathID        uint32        `mapstructure:"max_path_id"`
	ServerName       string        `mapstructure:"server_name"`
	EdgeEnvFile      string   `mapstructure:"edge_env_file"`
	PathInterfaces   string   `mapstructure:"path_interfaces"`
	PathAddresses    []string `mapstructure:"path_addresses"`
	CertFile         string   `mapstructure:"cert_file"`
	KeyFile          string   `mapstructure:"key_file"`
}

type Server struct {
	config        *Config
	logger        *slog.Logger
	sessions      *mpquicsession.Manager
	quicServer    *quic.EarlyListener
	ctx           context.Context
	cancel        context.CancelFunc
	streams       *handler.ImageFrameHandler
	jetsonStreams *handler.JetsonHandler
}

func New(cfg Config, log *slog.Logger) (*Server, error) {
	ctx, cancel := context.WithCancel(context.Background())
	
	baseDir := filepath.Join(".", "frames")
	streams, err := handler.NewImageFrameHandler(baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create image handler: %w", err)
	}

	jetsonStreams, err := handler.NewJetsonHandler(baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create jetson handler: %w", err)
	}
	
	streamHandler := conn.NewStreamHandler(log, streams, jetsonStreams)
	var rssiProvider rssi.Provider
	if cfg.EdgeEnvFile != "" {
		provider, err := rssi.NewSSHProviderFromEnv(cfg.EdgeEnvFile)
		if err != nil {
			return nil, fmt.Errorf("failed to create RSSI provider: %w", err)
		}
		rssiProvider = provider
	}
	pathSched := scheduler.NewPrimaryPathScheduler()
	sessions, err := mpquicsession.NewManager(
		log,
		streamHandler,
		transport.Parameters{InitialMaxPathID: cfg.InitialMaxPathID, MaxPathID: cfg.MaxPathID},
		pathSched,
		rssiProvider,
	)
	if err != nil {
		return nil, fmt.Errorf("invalid session manager config: %w", err)
	}
	if cfg.PathInterfaces != "" {
		ifaceMap, err := ParsePathInterfaces(cfg.PathInterfaces)
		if err != nil {
			return nil, fmt.Errorf("invalid path_interfaces config: %w", err)
		}
		for iface, pathID := range ifaceMap {
			sessions.SetInterfaceMapping(iface, pathID)
			log.Debug("configured path interface mapping", "iface", iface, "path_id", pathID)
		}
	}

		return &Server{
			config:        &cfg,
			logger:        log,
			sessions:      sessions,
			ctx:           ctx,
			cancel:        cancel,
			streams:       streams,
			jetsonStreams: jetsonStreams,
		}, nil
}

func LoadConfig() *Config {
	return DefaultConfig()
}

func (s *Server) Start(ctx context.Context) error {
	// Check if context is already canceled
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context canceled before start: %w", err)
	}

	tlsConfig, err := s.loadTLSConfig()
	if err != nil {
		return fmt.Errorf("failed to load TLS config: %w", err)
	}

	// Listen on UDP port
	addr, err := net.ResolveUDPAddr("udp", s.config.ListenAddr)
	if err != nil {
		return fmt.Errorf("failed to resolve address: %w", err)
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	sched, ok := s.sessions.Scheduler()
	var pathSel quic.PathSelector
	if ok && sched != nil {
		pathSel = mpquicsession.NewQuicPathSelector(sched)
	}
	quicConfig := &quic.Config{
		MaxIdleTimeout:     s.config.MaxIdleTimeout,
		MaxIncomingStreams: int64(s.config.MaxStreamNum),
		KeepAlivePeriod:    30 * time.Second,
		InitialMaxPathID:   s.config.InitialMaxPathID,
		PathSelector:       pathSel,
		Tracer:             qlog.DefaultConnectionTracer,
	}

	s.quicServer, err = quic.ListenEarly(conn, tlsConfig, quicConfig)
	if err != nil {
		return fmt.Errorf("failed to create QUIC server: %w", err)
	}

	fmt.Printf("QUIC server started: %s\n", s.config.ListenAddr)

	s.ctx, s.cancel = context.WithCancel(ctx)

	go func() {
		s.logger.Info("acceptLoop started")
		for {
			conn, err := s.quicServer.Accept(s.ctx)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					s.logger.Info("acceptLoop cancelled")
					return
				}
				s.logger.Error("failed to accept connection", "error", err)
				continue
			}
			s.logger.Info("new connection accepted", "peer", conn.RemoteAddr())
			go s.handleConnection(conn)
		}
	}()
	<-s.ctx.Done()
	return nil
}

func (s *Server) handleConnection(conn quic.Connection) {
	// If additional path addresses are configured, add them as extra paths
	for i, addrStr := range s.config.PathAddresses {
		addr, err := net.ResolveUDPAddr("udp", addrStr)
		if err != nil {
			s.logger.Warn("failed to resolve path address", "index", i, "address", addrStr, "error", err)
			continue
		}
		pathID := quic.PathID(i + 1) // path 1, 2, 3...
		if err := conn.AddPath(addr, pathID); err != nil {
			s.logger.Warn("failed to add path", "path_id", pathID, "address", addrStr, "error", err)
			continue
		}
		s.logger.Info("added multipath path", "path_id", pathID, "address", addrStr)
	}

	if err := s.sessions.Handle(s.ctx, conn); err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		s.logger.Error("failed to handle connection session", "error", err, "peer", conn.RemoteAddr())
	}
}

// loadTLSConfig loads TLS certificate and creates TLS config
func (s *Server) loadTLSConfig() (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(s.config.CertFile, s.config.KeyFile)
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{protocols.ALPN},
	}, nil
}

// Stop stops the QUIC server gracefully
func (s *Server) Stop() error {
	s.logger.Info("stopping QUIC server...")

	// Cancel context to stop accepting new connections
	s.cancel()

	// Close the QUIC listener
	if s.quicServer != nil {
		return s.quicServer.Close()
	}

	return nil
}
