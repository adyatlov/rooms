package rooms

// RoomID uniquely identifies a room in a rooms service.
type RoomID string

// GuestID uniquely identifies a guest in a rooms service.
type GuestID string

// Permission defines what the permission holder (guest) is allowed to do in a room.
type Permission uint8

const (
	// PermSend allows guests to send direct messages.
	PermSend = iota
	// PermReceive allows guests to receive messages.
	PermReceive
)

// Message carries a payload and routing information. If To == 0 then the message
// should be broadcasted.
type Message struct {
	From  GuestID
	To    GuestID
	Bytes []byte
}

// Conveyor conveys messages from guests to hosts and vice versa.
type Conveyor interface {
	CreateRoom(RoomID, <-chan Message) (<-chan Message, error)
	AddGuest(RoomID, GuestID, []Permission, <-chan []byte) (<-chan []byte, error)
	Close() error
}

// ConveyorStater provides a current state of the Conveyor.
type ConveyorStater interface {
	// State returns state of the Conveyor.
	State() (ConveyorState, error)
}

// Exposer exposes methods and channels of the given Conveyor using network
// (e.g. WebSocket, gRPC) or locally (e.g. via standard streams or IPC socket).
type Exposer interface {
	Expose(Conveyor) error
}

// Config contains data required for joining a service.
type Config struct {
	// Client name
	Name string
	// Auth secret
	Secret []byte
	// Other values which may be required to establish a connection to the
	// service
	Values map[string]string
}

// ConveyorState represents current state of the simpleconveyor.
type ConveyorState struct {
	Rooms   []RoomState
	Created int64
	Stats   interface{}
}

// RoomState represents current state of the Room.
type RoomState struct {
	Name       RoomID
	GuestState []GuestState
	Created    int64
	Stats      interface{}
}

// GuestState represents current state of the Guest.
type GuestState struct {
	GuestID GuestID
	Added   int64
	Guests  int
	Stats   interface{}
}

// Dialer defines a way for clients to gain access to services.
type Dialer interface {
	// Dial performs authorization and returns a reference to the service.
	Dial(Config) (Dialer, error)
	// Close disconnects the client from the service and releases resources.
	Close() error
}
