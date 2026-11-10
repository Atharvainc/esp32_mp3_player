package main

import (
	"encoding/json"
	"log"
	"time"

	"github.com/gorilla/websocket"
)

func ackJSON(id int, ok bool, errMsg string) []byte {
	b, _ := json.Marshal(map[string]any{"type": "ack", "id": id, "ok": ok, "error": errMsg})
	return b
}

func main() {
	conn, _, err := websocket.DefaultDialer.Dial("ws://localhost:8080/ws/device", nil)
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer conn.Close()

	outgoing := make(chan []byte, 8) // everything we send goes through here

	go func() { // reader
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				log.Println("read:", err)
				return
			}
			log.Printf("server msg: %s", msg)

			var cmd struct {
				Type   string `json:"type"`
				ID     int    `json:"id"`
				Action string `json:"action"`
			}
			if json.Unmarshal(msg, &cmd) != nil || cmd.Type != "command" {
				continue
			}
			switch cmd.Action {
			case "pause": // test hook: never answer
				continue
			case "prev": // test hook: report a failure
				outgoing <- ackJSON(cmd.ID, false, "no previous track")
			default:
				outgoing <- ackJSON(cmd.ID, true, "")
			}
		}
	}()

	state := []byte(`{"type":"state","song":"song1.mp3","battery_pct":69,"position_s":45}`)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for { // the only writer
		var data []byte
		select {
		case <-ticker.C:
			data = state
		case data = <-outgoing:
		}
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			log.Println("write:", err)
			return
		}
	}
}
