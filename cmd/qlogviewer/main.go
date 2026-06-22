package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"mp-quic-go/internal/qlogviewer"
)

func main() {
	port := flag.Int("port", 8090, "HTTP port")
	flag.Parse()

	collector := qlogviewer.NewCollector()

	mux := http.NewServeMux()

	// Event ingestion endpoint (for external producers)
	mux.HandleFunc("/api/event", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", 405)
			return
		}
		var ev qlogviewer.Event
		if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if ev.Time == 0 {
			ev.Time = float64(time.Now().UnixMilli())
		}
		collector.BroadcastEvent(ev)
		w.WriteHeader(204)
	})

	// WebSocket endpoint
	mux.Handle("/ws", qlogviewer.NewWSHandler(collector))

	// Static files
	mux.Handle("/", http.FileServer(http.FS(qlogviewer.StaticFS)))

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("qlogviewer starting on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server: %v", err)
	}
}
