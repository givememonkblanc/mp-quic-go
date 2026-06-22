package qlogviewer

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // allow all origins for dashboard
	},
}

// NewWSHandler returns an http.Handler that upgrades to WebSocket
// and streams collector events to the connected client.
func NewWSHandler(collector *Collector) http.Handler {
	return &wsHandler{collector: collector}
}

type wsHandler struct {
	collector *Collector
}

func (h *wsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("qlogviewer: upgrade: %v", err)
		return
	}
	defer conn.Close()

	ch := make(chan []byte, 256)
	h.collector.Subscribe(ch)
	defer h.collector.Unsubscribe(ch)

	// Send a "hello" event so the frontend knows we're connected.
	hello, _ := json.Marshal(map[string]string{"type": "hello"})
	conn.WriteMessage(websocket.TextMessage, hello)

	// Read-loop just for keepalive; we ignore client messages.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-done:
			return
		case data := <-ch:
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				return
			}
		}
	}
}

// Server is the web dashboard HTTP server.
type Server struct {
	collector *Collector
	mu        sync.Mutex
	mux       *http.ServeMux
}

// NewServer creates a dashboard server using the given collector.
func NewServer(collector *Collector) *Server {
	s := &Server{collector: collector}
	mux := http.NewServeMux()
	mux.Handle("/ws", &wsHandler{collector: collector})
	mux.Handle("/", http.FileServer(http.FS(StaticFS)))
	s.mux = mux
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}
