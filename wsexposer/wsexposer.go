package wsexposer

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"

	"github.com/adyatlov/rooms"
	"github.com/adyatlov/rooms/errors"
)

const (
	// Time allowed to write a messageJ to the guest.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong messageJ from the guest.
	pongWait = 60 * time.Second

	// Send pings to guest with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum messageJ size allowed from guest.
	maxMessageSize = 512

	// Connect endpointConnect
	endpointCreate  = "/roomsws/v1/create"
	endpointConnect = "/roomsws/v1/connect"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type connectParams struct {
	Guest       string
	Room        string
	Permissions []string
}

type messageJ struct {
	From  string `json:"from"`
	To    string `json:"to,omitempty"`
	Bytes string `json:"bytes,omitempty"`
}

// Exposer Implements rooms.Exposer
type Exposer struct {
	Host       string
	Port       int
	PathPrefix string
	conveyor   rooms.Conveyor
}

// Expose Implements rooms.Exposer.Expose
func (e *Exposer) Expose(conveyor rooms.Conveyor) error {
	if e.Port == 0 {
		e.Port = 8080
	}
	if e.Host == "" {
		e.Host = "127.0.0.1"
	}
	e.conveyor = conveyor
	r := mux.NewRouter().PathPrefix(e.PathPrefix).Subrouter()
	r.HandleFunc(endpointConnect, e.connect)
	r.HandleFunc(endpointCreate, e.create)
	return http.ListenAndServe(fmt.Sprintf("%v:%v", e.Host, e.Port), r)
}

func (e *Exposer) connect(w http.ResponseWriter, r *http.Request) {
	Guest := r.URL.Query().Get("guest")
	Room := r.URL.Query().Get("room")
	Permissions := r.URL.Query()["permission"]
	guest, err := parseGuestID(Guest)
	if err != nil {
		writeError(w, err)
		log.Println(err)
		return
	}
	room, err := parseRoomID(Room)
	if err != nil {
		writeError(w, err)
		log.Println(err)
		return
	}
	permissions, err := parsePermissions(Permissions)
	if err != nil {
		writeError(w, err)
		log.Println(err)
		return
	}
	in := make(chan []byte)
	out, err := e.conveyor.AddGuest(room, guest, permissions, in)
	if err != nil {
		errMsg := fmt.Sprintf("Cannot add guest %v to room %v.",
			guest, room)
		e := errors.E(errors.Transient, errMsg, err)
		writeError(w, e)
		log.Println(e)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		e := errors.E(errors.Transient, "Cannot upgrade to WebSocket connection.", err)
		writeError(w, e)
		log.Println(e)
		return
	}
	configureConn(conn)
	go readGuestConn(conn, in)
	go writeGuestConn(conn, out)
}

func (e *Exposer) create(w http.ResponseWriter, r *http.Request) {
	params := connectParams{}
	params.Room = r.URL.Query().Get("room")
	room, err := parseRoomID(params.Room)
	if err != nil {
		writeError(w, err)
		log.Println(err)
		return
	}
	in := make(chan rooms.Message)
	out, err := e.conveyor.CreateRoom(room, in)
	if err != nil {
		errMsg := fmt.Sprintf("Cannot create room %v.", room)
		e := errors.E(errors.Transient, errMsg, err)
		writeError(w, e)
		log.Println(e)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		e := errors.E(errors.Transient, "Cannot upgrade to WebSocket connection.", err)
		writeError(w, e)
		log.Println(e)
		return
	}
	configureConn(conn)
	go readHostConn(conn, in)
	go writeHostConn(conn, out)
}

func readHostConn(conn *websocket.Conn, c chan rooms.Message) {
	defer func() {
		close(c)
		_ = conn.Close()
	}()
	for {
		mType, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Println(err)
			}
			return
		}
		switch mType {
		case websocket.CloseMessage:
			return
		case websocket.BinaryMessage:
			msg, err := decodeMessage(message)
			if err != nil {
				log.Println("Cannot decode message:", err.Error())
				continue
			}
			c <- msg
		case websocket.TextMessage:
			msg, err := decodeMessage(message)
			if err != nil {
				log.Println("Cannot decode message:", err.Error())
				continue
			}
			c <- msg
		case websocket.PingMessage:
			continue
		case websocket.PongMessage:
			continue
		default:
			return
		}
	}
}

func writeHostConn(conn *websocket.Conn, c <-chan rooms.Message) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = conn.Close()
	}()
	for {
		log.Println("Message sent")
		select {
		case message, ok := <-c:
			if !ok {
				// The conveyor closed the channel.
				_ = conn.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, "Room closed. Or host changed."))
				return
			}
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			msg, err := encodeMessage(message)
			if err != nil {
				log.Println("Cannot encode message:", err)
				continue
			}
			err = conn.WriteMessage(websocket.TextMessage, msg)
			if err != nil {
				log.Print(err)
				return
			}
		case <-ticker.C:
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			err := conn.WriteMessage(websocket.PingMessage, nil)
			if err != nil {
				log.Print(err)
				return
			}
		}
	}
}

func parseRoomID(id string) (rooms.RoomID, error) {
	if len(id) > 16 || len(id) == 0 {
		return "", errors.E(errors.Invalid, "Wrong format of the Room ID: length should be > 0 and < 16: "+"id")
	}
	return rooms.RoomID(id), nil
}

func parseGuestID(id string) (rooms.GuestID, error) {
	if len(id) > 16 || len(id) == 0 {
		return "", errors.E(errors.Invalid, "Wrong format of the Guest ID: length should be > 0 and < 16: "+"id")
	}
	return rooms.GuestID(id), nil
}

func parsePermissions(s []string) ([]rooms.Permission, error) {
	permissionsM := make(map[rooms.Permission]struct{})
	for i := range s {
		switch strings.ToLower(s[i]) {
		case "send":
			permissionsM[rooms.PermSend] = struct{}{}
		case "receive":
			permissionsM[rooms.PermReceive] = struct{}{}
		}
	}
	if len(permissionsM) == 0 {
		return nil, errors.E(errors.Invalid,
			"Didn't recognize any permissions, at least one should be set")
	}
	permissions := make([]rooms.Permission, 0, len(permissionsM))
	for permission := range permissionsM {
		permissions = append(permissions, permission)
	}
	return permissions, nil
}

func configureConn(conn *websocket.Conn) {
	conn.SetReadLimit(maxMessageSize)
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(
		func(string) error {
			_ = conn.SetReadDeadline(time.Now().Add(pongWait))
			return nil
		})
}

func writeError(w http.ResponseWriter, err error) {
	if _, ok := err.(*errors.Error); !ok {
		err = errors.E(err, errors.Transient)
	}
	e := err.(*errors.Error)
	var status int
	switch e.Kind {
	case errors.Permission:
		status = http.StatusForbidden
	case errors.Exist:
		status = http.StatusConflict
	case errors.NotExist:
		status = http.StatusNotFound
	case errors.Invalid:
		status = http.StatusBadRequest
	case errors.Transient:
		status = http.StatusBadRequest
	default:
		status = http.StatusInternalServerError
	}
	http.Error(w, e.Error(), status)
}

func readGuestConn(conn *websocket.Conn, c chan<- []byte) {
	defer func() {
		close(c)
		_ = conn.Close()
	}()
	for {
		mType, message, err := conn.ReadMessage()
		log.Println("Message received, type:", mType)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Println(err)
			}
			return
		}
		switch mType {
		case websocket.CloseMessage:
			return
		case websocket.BinaryMessage:
			c <- message
		case websocket.TextMessage:
			c <- message
		case websocket.PingMessage:
			continue
		case websocket.PongMessage:
			continue
		default:
			return
		}
	}
}

func writeGuestConn(conn *websocket.Conn, c <-chan []byte) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = conn.Close()
	}()
	for {
		log.Println("Message sent")
		select {
		case message, ok := <-c:
			if !ok {
				// The conveyor closed the channel.
				_ = conn.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, "Room closed."))
				return
			}
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			err := conn.WriteMessage(websocket.TextMessage, message)
			if err != nil {
				log.Print(err)
				return
			}
		case <-ticker.C:
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			err := conn.WriteMessage(websocket.PingMessage, nil)
			if err != nil {
				log.Print(err)
				return
			}
		}
	}
}

func decodeMessage(b []byte) (rooms.Message, error) {
	var m messageJ
	err := json.Unmarshal(b, &m)
	if err != nil {
		return rooms.Message{}, err
	}
	message := rooms.Message{}
	if m.To != "" {
		message.To, err = parseGuestID(m.To)
	}
	if err != nil {
		return rooms.Message{}, err
	}
	message.Bytes, err = base64.StdEncoding.DecodeString(m.Bytes)
	if err != nil {
		return rooms.Message{}, err
	}
	return message, nil
}

func encodeMessage(message rooms.Message) ([]byte, error) {
	var m messageJ
	m.From = string(message.From)
	m.Bytes = base64.StdEncoding.EncodeToString(message.Bytes)
	return json.Marshal(m)
}
