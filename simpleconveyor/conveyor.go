package simpleconveyor

import (
	"sync"
	"time"

	"github.com/adyatlov/rooms"
	"github.com/adyatlov/rooms/errors"
)

// Check interface compatibility.
var (
	_ rooms.Conveyor = (*Conveyor)(nil)
)

func hasPermission(pp []rooms.Permission, p rooms.Permission) bool {
	for i := range pp {
		if pp[i] == p {
			return true
		}
	}
	return false
}

type room struct {
	conveyor         *Conveyor
	id               rooms.RoomID
	toHost           chan rooms.Message
	fromHost         <-chan rooms.Message
	newFromHost      chan (<-chan rooms.Message)
	receivers        map[chan []byte]struct{}
	receiversByGuest map[rooms.GuestID]map[chan []byte]struct{}
	nGuests          int
	muGuests         *sync.RWMutex
	muHost           *sync.RWMutex
	done             chan struct{}
}

func newRoom(con *Conveyor, rID rooms.RoomID, fromHost <-chan rooms.Message) *room {
	r := &room{
		conveyor:         con,
		id:               rID,
		toHost:           make(chan rooms.Message, 20),
		fromHost:         fromHost,
		newFromHost:      make(chan (<-chan rooms.Message)),
		receivers:        make(map[chan []byte]struct{}),
		receiversByGuest: make(map[rooms.GuestID]map[chan []byte]struct{}),
		muGuests:         &sync.RWMutex{},
		muHost:           &sync.RWMutex{},
		done:             make(chan struct{}),
	}
	go r.conveyToGuests()
	return r
}

func (r *room) addGuest(u rooms.GuestID, perms []rooms.Permission, fromGuest <-chan []byte) (<-chan []byte, error) {
	const op = errors.Op("guests.addGuest")
	r.muGuests.Lock()
	defer r.muGuests.Unlock()
	if r.toHost == nil {
		return nil, errors.E("The room is already closed.", op, r.id, errors.Invalid)
	}
	if len(perms) == 0 {
		return nil, errors.E(op, errors.Permission, u, "Permissions are not specified.")
	}
	toGuest := make(chan []byte, 20)
	if hasPermission(perms, rooms.PermReceive) {
		r.receivers[toGuest] = struct{}{}
		if _, ok := r.receiversByGuest[u]; !ok {
			r.receiversByGuest[u] = make(map[chan []byte]struct{})
		}
		r.receiversByGuest[u][toGuest] = struct{}{}
	}
	r.nGuests++
	if hasPermission(perms, rooms.PermSend) {
		go r.conveyToHost(u, fromGuest, toGuest)
	} else {
		go r.waitForClosing(u, fromGuest, toGuest)
	}
	return toGuest, nil
}

func (r *room) conveyToGuests() {
	defer func() {
		_ = r.close()
		r.conveyor.removeRoom(r.id)
	}()
	for {
		select {
		case message, ok := <-r.fromHost:
			if !ok {
				return
			}
			r.sendToGuests(message)
		case newFromHost := <-r.newFromHost:
			r.fromHost = newFromHost
		case <-r.done:
			return
		}
	}
}

func (r *room) sendToGuests(message rooms.Message) {
	if message.To == "" {
		r.broadcast(r.receivers, message.Bytes)
		return
	}
	if receivers, ok := r.receiversByGuest[message.To]; ok {
		r.broadcast(receivers, message.Bytes)
	}
}

func (r *room) broadcast(receivers map[chan []byte]struct{}, b []byte) {
	r.muGuests.RLock()
	defer r.muGuests.RUnlock()
	for toGuest := range receivers {
		select {
		case toGuest <- b:
		default:
			// TODO: Count undelivered messages
		}
	}
}

func (r *room) conveyToHost(u rooms.GuestID, fromGuest <-chan []byte, toGuest chan []byte) {
	defer close(toGuest)
	for {
		select {
		case b, ok := <-fromGuest:
			if !ok {
				r.removeGuest(u, toGuest)
				return
			}
			r.muHost.RLock()
			if r.toHost == nil {
				r.muHost.RUnlock()
				return
			}
			select {
			case r.toHost <- rooms.Message{From: u, Bytes: b}:
			default:
				// TODO: Count undelivered messages
			}
			r.muHost.RUnlock()
		case <-r.done:
			return
		}
	}
}

func (r *room) waitForClosing(u rooms.GuestID, fromGuest <-chan []byte, toGuest chan []byte) {
	defer close(toGuest)
	for {
		select {
		case _, ok := <-fromGuest:
			if !ok {
				r.removeGuest(u, toGuest)
				return
			}
		case <-r.done:
			return
		}
	}
}

func (r *room) removeGuest(u rooms.GuestID, toGuest chan []byte) {
	r.muGuests.Lock()
	defer r.muGuests.Unlock()
	delete(r.receivers, toGuest)
	if guestReceivers, ok := r.receiversByGuest[u]; ok {
		delete(guestReceivers, toGuest)
		if len(guestReceivers) == 0 {
			delete(r.receiversByGuest, u)
		}
	}
	r.nGuests--
}

func (r *room) close() error {
	const op = "simpleconveyor.close()"
	r.muHost.Lock()
	defer r.muHost.Unlock()
	if r.toHost == nil {
		return errors.E("The room is already closed.", op, r.id, errors.Invalid)
	}
	close(r.toHost)
	close(r.done)
	r.toHost = nil
	return nil
}

func (r *room) replaceHost(newFromHost <-chan rooms.Message) {
	r.muHost.Lock()
	defer r.muHost.Unlock()
	close(r.toHost)
	r.toHost = make(chan rooms.Message, 20)
	r.newFromHost <- newFromHost
}

// Conveyor implements rooms.Conveyor.
type Conveyor struct {
	rooms   map[rooms.RoomID]*room
	mu      *sync.RWMutex
	created int64
}

// NewConveyor returns a new simpleconveyor.
func NewConveyor() *Conveyor {
	return &Conveyor{
		rooms:   make(map[rooms.RoomID]*room),
		mu:      &sync.RWMutex{},
		created: time.Now().Unix(),
	}
}

// Close implements correspondent method of rooms.Dialer.
func (c *Conveyor) Close() error {
	const op = errors.Op("simpleconveyor.Close")
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rooms == nil {
		return errors.E(op, "Conveyor is already closed.", errors.Invalid)
	}
	for _, room := range c.rooms {
		_ = room.close()
	}
	c.rooms = nil
	return nil
}

// CreateRoom implements rooms.CreateRoom
func (c *Conveyor) CreateRoom(id rooms.RoomID,
	fromHost <-chan rooms.Message) (<-chan rooms.Message, error) {
	const op errors.Op = "simpleconveyor.CreateRoom"
	room, ok := c.rooms[id]
	if !ok {
		room = newRoom(c, id, fromHost)
		c.mu.Lock()
		c.rooms[id] = room
		c.mu.Unlock()
	} else {
		room.replaceHost(fromHost)
	}
	return room.toHost, nil
}

// AddGuest implements correspondent method of rooms.Conveyor.
func (c *Conveyor) AddGuest(rID rooms.RoomID,
	uID rooms.GuestID,
	perms []rooms.Permission,
	fromGuest <-chan []byte) (<-chan []byte, error) {
	const op errors.Op = "simpleconveyor.AddGuest"
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.rooms == nil {
		return nil, errors.E(op, "Conveyor is closed.", errors.Invalid)
	}
	r, ok := c.rooms[rID]
	if !ok {
		return nil, errors.E(op, "No such room.", errors.NotExist, rID)
	}
	return r.addGuest(uID, perms, fromGuest)
}

func (c *Conveyor) roomCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.rooms)
}

func (c *Conveyor) removeRoom(rID rooms.RoomID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r, ok := c.rooms[rID]; ok {
		r.close()
	}
	delete(c.rooms, rID)
}
