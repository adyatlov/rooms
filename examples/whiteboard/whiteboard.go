package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"log"
	"net/url"
	"os"
	"os/signal"
	"time"

	"github.com/gorilla/websocket"
)

var addr = flag.String("addr", "localhost:8080", "http service address")

func main() {
	flag.Parse()
	log.SetFlags(0)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	u := url.URL{Scheme: "ws", Host: *addr, Path: "/roomsws/v1/create", RawQuery: "room=whiteboard"}
	log.Printf("connecting to %s", u.String())

	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer c.Close()

	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			_, b, err := c.ReadMessage()
			if err != nil {
				log.Println("read:", err)
				return
			}
			m, err := decodeMessage(b)
			if err != nil {
				log.Println("Cannot decode message:", err.Error())
				continue
			}
			log.Printf("From: %s, To: %s, Bytes: %s", m.From, m.To, m.Bytes)
			m.From = ""
			m.To = ""
			b, err = encodeMessage(m)
			if err != nil {
				log.Println("Cannot encode message:", err.Error())
				continue
			}
			err = c.WriteMessage(websocket.TextMessage, b)
			if err != nil {
				log.Println("Cannot send message:", err.Error())
				continue
			}
		}
	}()

	for {
		select {
		case <-done:
			return
		case <-interrupt:
			log.Println("interrupt")

			// Cleanly close the connection by sending a close message and then
			// waiting (with timeout) for the server to close the connection.
			err := c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			if err != nil {
				log.Println("write close:", err)
				return
			}
			select {
			case <-done:
			case <-time.After(time.Second):
			}
			return
		}
	}
}

type message struct {
	From  string `json:"from"`
	To    string `json:"to,omitempty"`
	Bytes string `json:"bytes,omitempty"`
}

func decodeMessage(b []byte) (message, error) {
	var m message
	err := json.Unmarshal(b, &m)
	if err != nil {
		return message{}, err
	}
	b, err = base64.StdEncoding.DecodeString(m.Bytes)
	if err != nil {
		return message{}, err
	}
	m.Bytes = string(b)
	return m, nil
}

func encodeMessage(m message) ([]byte, error) {
	m.Bytes = base64.StdEncoding.EncodeToString([]byte(m.Bytes))
	return json.Marshal(m)
}
