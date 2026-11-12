// server
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	expiryTime  = 20 * time.Second
	readTimeout = 15 * time.Second
)

// Defines the state of the device
type State struct {
	Song       string `json:"song"`
	BatteryPct int    `json:"battery_pct"`
	PositionS  int    `json:"position_s"`
}

// Defines the structure of the messages sent between the device and server
type Envelope struct {
	Type string `json:"type"`
}

// Global variables to store the latest state of the device and the last time it was seen
var (
	mu        sync.Mutex
	latest    *State
	lastSeen  time.Time
	connected bool
)

// Defines the response structure for the state handler
type StateResponse struct {
	Online bool   `json:"online"`
	State  *State `json:"state"`
}

// client struct uses channel to send data to browser
type Client struct {
	send chan []byte
}

var (
	clientsMu sync.Mutex
	clients   = map[*Client]bool{}
)

// defines the commands from the browser
type Command struct {
	Type   string `json:"type"`
	ID     int    `json:"id"`
	Action string `json:"action"`
	Value  int    `json:"value"`
}

// defines the command from the browser
type CommandRequest struct {
	Action string `json:"action"`
	Value  int    `json:"value"`
}

// defines the ack message sent back to browser by fakedevice
type Ack struct {
	ID    int    `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// Global variables to store the pending acks
var (
	pendingMu sync.Mutex
	pending   = map[int]chan Ack{}
	nextID    int
)

var (
	errDeviceOffline = errors.New("device offline")
	errDeviceBusy    = errors.New("device busy, please wait")
	errTimeout       = errors.New("unresponsive device")
)

// checks the status of the server
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// returns the current time in rfc3339 format
func timeHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	timenow := time.Now().Format(time.RFC3339)
	json.NewEncoder(w).Encode(map[string]string{"time": timenow})
}

// helps build response since both http endpoint and websocket use it.
func buildResponse() StateResponse {
	mu.Lock()
	defer mu.Unlock()

	if !lastSeen.IsZero() && time.Since(lastSeen) > expiryTime {
		latest = nil
		lastSeen = time.Time{}
	}
	var snapshot *State
	if latest != nil {
		c := *latest
		snapshot = &c
	}
	return StateResponse{
		Online: connected,
		State:  snapshot,
	}
}

// returns the current state of the device
func stateHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(buildResponse())
}

// upgrades the http connection to a websocket connection
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true }, // dev only, we'll tighten this later
}

// Handles the websocket connection from the device
func deviceWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("upgrade:", err)
		return
	}
	defer conn.Close()
	log.Println("device connected:", r.RemoteAddr)
	send := make(chan []byte, 8)
	done := make(chan struct{})
	mu.Lock()
	connected = true
	lastSeen = time.Now()
	deviceSend = send
	mu.Unlock()
	broadcast()
	go func() {
		for {
			select {
			case data := <-send:
				if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
					return
				}
			case <-done:
				return
			}
		}
	}()
	defer func() {
		close(done)
		mu.Lock()
		stillCurrent := deviceSend == send
		if stillCurrent {
			connected = false
			deviceSend = nil
		}
		remaining := time.Until(lastSeen.Add(expiryTime))
		mu.Unlock()
		if stillCurrent {
			broadcast()                                      //tell browsers: disconnected
			time.AfterFunc(remaining+time.Second, broadcast) // recheck: which expires it
		}
	}()
	for {
		conn.SetReadDeadline(time.Now().Add(readTimeout))
		_, msg, err := conn.ReadMessage()
		if err != nil {
			log.Println("disconnected:", err)
			return
		}
		mu.Lock()
		lastSeen = time.Now()
		mu.Unlock()
		var env Envelope
		if err := json.Unmarshal(msg, &env); err != nil {
			log.Println("bad message: ", err)
			continue
		}
		switch env.Type {
		case "state":
			var s State
			if err := json.Unmarshal(msg, &s); err != nil {
				log.Println("bad state: ", err)
				continue
			}
			mu.Lock()
			latest = &s
			mu.Unlock()
			broadcast()
			log.Printf("state: %+v", s)

		case "ack":
			var a Ack
			if err := json.Unmarshal(msg, &a); err != nil {
				log.Println("bad ack: ", err)
				continue
			}
			pendingMu.Lock()
			reply, found := pending[a.ID]
			pendingMu.Unlock()
			if found {
				select {
				case reply <- a:
				default:
				}
			}
		default:
			log.Println("unknown type: ", env.Type)
			continue
		}
	}
}

// Sends the latest state to all connected browsers
func broadcast() {
	data, err := json.Marshal(buildResponse())
	if err != nil {
		log.Println("marshal:", err)
		return
	}
	clientsMu.Lock()
	defer clientsMu.Unlock()
	for c := range clients {
		select {
		case c.send <- data:
		default: // this browser's queue is full, skip it
		}
	}
}

// Handles the websocket connection from the browser
func browserWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("upgrade:", err)
		return
	}
	defer conn.Close()

	c := &Client{send: make(chan []byte, 8)}
	clientsMu.Lock()
	clients[c] = true
	clientsMu.Unlock()
	defer func() {
		clientsMu.Lock()
		delete(clients, c)
		clientsMu.Unlock()
	}()
	log.Println("browser connected:", r.RemoteAddr)

	done := make(chan struct{})
	go func() { // the only writer for this connection
		for {
			select {
			case data := <-c.send:
				if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
					return
				}
			case <-done:
				return
			}
		}
	}()

	// give the new browser the current state right away
	first, _ := json.Marshal(buildResponse())
	c.send <- first

	// the browser sends us nothing, but reading detects when it leaves
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
	close(done)
	log.Println("browser disconnected")
}

var deviceSend chan []byte //guarded by mu, nil when no device connected
// valid map of actions
var validActions = map[string]bool{
	"play":         true,
	"pause":        true,
	"next":         true,
	"prev":         true,
	"set_volume":   true,
	"set_position": true,
	"set_song":     true,
}

func runCommand(ctx context.Context, action string, value int) (Ack, error) {
	mu.Lock()
	ch := deviceSend
	mu.Unlock()
	if ch == nil {
		return Ack{}, errDeviceOffline
	}

	pendingMu.Lock()
	nextID++
	id := nextID
	reply := make(chan Ack, 1)
	pending[id] = reply
	pendingMu.Unlock()
	defer func() {
		pendingMu.Lock()
		delete(pending, id)
		pendingMu.Unlock()
	}()

	msg, err := json.Marshal(Command{Type: "command", ID: id, Action: action, Value: value})
	if err != nil {
		return Ack{}, err
	}
	select {
	case ch <- msg:
	default:
		return Ack{}, errDeviceBusy
	}

	select {
	case ack := <-reply:
		return ack, nil
	case <-time.After(2 * time.Second):
		return Ack{}, errTimeout
	case <-ctx.Done():
		return Ack{}, ctx.Err()
	}
}

// handles the commands from the browser
func commandHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "invalid method", http.StatusMethodNotAllowed)
		return
	}
	var req CommandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if !validActions[req.Action] {
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	if req.Action == "set_volume" && (req.Value < 0 || req.Value > 20) {
		http.Error(w, "volume must be between 0-20", http.StatusBadRequest)
		return
	}
	ack, err := runCommand(r.Context(), req.Action, req.Value)
	switch {
	case errors.Is(err, errDeviceOffline), errors.Is(err, errDeviceBusy):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, errTimeout):
		http.Error(w, err.Error(), http.StatusGatewayTimeout)
	case err != nil:
		return // client went away, nobody listening
	case !ack.OK:
		http.Error(w, ack.Error, http.StatusBadGateway)
	default:
		w.WriteHeader(http.StatusOK)
	}
}

// main
func main() {
	if err := os.MkdirAll("data", 0o755); err != nil {
		log.Fatal(err)
	}
	if err := openDB("data/archive.db"); err != nil {
		log.Fatal(err)
	}
	for _, dir := range []string{songsDir, tmpDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatal(err)
		}
	}
	http.HandleFunc("GET /api/playlists", listPlaylistsHandler)
	http.HandleFunc("POST /api/playlists", createPlaylistHandler)
	http.HandleFunc("PUT /api/songs/{id}/playlist", setSongPlaylistHandler)
	http.HandleFunc("/api/upload", uploadHandler)
	http.HandleFunc("/api/songs", songsHandler)
	http.HandleFunc("/health", healthHandler)
	http.HandleFunc("/time", timeHandler)
	http.HandleFunc("/ws/device", deviceWS)
	http.HandleFunc("/api/state", stateHandler)
	http.HandleFunc("/ws/browser", browserWS)
	http.HandleFunc("/api/command", commandHandler)
	http.Handle("/", http.FileServer(http.Dir("web")))
	log.Println("listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))

}
