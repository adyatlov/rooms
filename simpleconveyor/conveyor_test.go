package simpleconveyor

import (
	"bytes"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"testing"

	"github.com/adyatlov/rooms"
)

const (
	Andrey rooms.GuestID = "Andrey"
	Irina  rooms.GuestID = "Irina"
	Chat   rooms.RoomID  = "Chat"
	Chess  rooms.RoomID  = "Chess"
)

var permsAll = []rooms.Permission{
	rooms.PermReceive,
	rooms.PermSend,
}

func mCh() chan rooms.Message {
	return make(chan rooms.Message)
}

func bCh() chan []byte {
	return make(chan []byte)
}

func compareMessages(message1 rooms.Message, message2 rooms.Message) string {
	res := make([]string, 0, 3)
	if !bytes.Equal(message1.Bytes, message2.Bytes) {
		res = append(res, fmt.Sprintf("Bytes are different: %v vs %v", string(message1.Bytes), string(message2.Bytes)))
	}
	if message1.From != message2.From {
		res = append(res, fmt.Sprintf("'From' fileds are different %v vs %v.", message1.From, message2.From))
	}
	if message1.To != message2.To {
		res = append(res, fmt.Sprintf("'To' fields are different %v vs %v.", message1.To, message2.To))
	}
	return strings.Join(res, " ")
}

func TestCloseClosesEmptyConveyorWithoutErrors(t *testing.T) {
	conveyor := NewConveyor()
	if err := conveyor.Close(); err != nil {
		t.Errorf("Unexpected error wen closing empty Conveyor: %v", err.Error())
	}
}

func TestCloseReturnsErrorIfConveyorAlreadyClosed(t *testing.T) {
	conveyor := NewConveyor()
	conveyor.Close()
	if err := conveyor.Close(); err == nil {
		t.Error("Expected error when Close simpleconveyor second time, but Close returned nil.")
	}
}

func TestCloseClosesHostAndGuestChannels(t *testing.T) {
	conveyor := NewConveyor()
	hostOut, _ := conveyor.CreateRoom("Chat", mCh())
	guestOut, _ := conveyor.AddGuest("Chat", "Andrey", permsAll, bCh())
	if err := conveyor.Close(); err != nil {
		t.Errorf("Error when closing Conveyor: %v", err.Error())
	}
	if _, ok := <-hostOut; ok {
		t.Errorf("Conveyor.Close() should close host output channel")
	}
	if _, ok := <-guestOut; ok {
		t.Errorf("Conveyor.Close() should close guest output channel")
	}
}

func TestAddingNewHostClosesChannelForOldHost(t *testing.T) {
	conveyor := NewConveyor()
	toHost1, _ := conveyor.CreateRoom("Chat", mCh())
	fromHost2 := mCh()
	toHost2, err := conveyor.CreateRoom("Chat", fromHost2)
	if err != nil {
		t.Fatalf("Error when adding a new host: %v", err.Error())
	}
	if _, ok := <-toHost1; ok {
		t.Error("Adding a new host should close host output channel of the old one")
	}
	fromGuest := bCh()
	toGuest, err := conveyor.AddGuest("Chat", "Andrey", permsAll, fromGuest)
	if err != nil {
		t.Error("There should be no error when adding a guest after replacing a host. Error occurred:", err)
	}
	fromGuest <- []byte("from host to guest")
	if _, ok := <-toHost2; !ok {
		t.Error("New host should receive messages from the guest")
	}
	fromHost2 <- rooms.Message{To: "Andrey", Bytes: []byte("from guest to host")}
	if _, ok := <-toGuest; !ok {
		t.Error("Guest should receive messages from the guest")
	}
}

func TestAddingGuestDoesntCreateNewRoom(t *testing.T) {
	conveyor := NewConveyor()
	inGuest := bCh()
	outGuest, err := conveyor.AddGuest(Chat, Irina, permsAll, inGuest)
	if outGuest != nil {
		t.Error("Conveyor returns non-nil out channel when guest joining nonexistent room.")
	}
	if err == nil {
		t.Error("Conveyor returns nil error when guest joining nonexistent room.")
	}
	if conveyor.roomCount() > 0 {
		t.Error("Conveyor created a room when guest joining nonexistent room.")
	}
}

func TestConveyorBroadcastsMessages(t *testing.T) {
	conveyor := NewConveyor()
	inHost := mCh()
	_, _ = conveyor.CreateRoom("Chat", inHost)
	outGuest1, _ := conveyor.AddGuest("Chat", "Andrey", permsAll, bCh())
	outGuest2, _ := conveyor.AddGuest("Chat", "Irina", permsAll, bCh())
	bytesFromHost := []byte("I'm Irina!")
	inHost <- rooms.Message{To: "", Bytes: bytesFromHost}
	var receivedFromHost1 []byte
	select {
	case receivedFromHost1 = <-outGuest1:
	default:
		t.Error("Guest1 didn't receive the message from the host")
	}
	var receivedFromHost2 []byte
	select {
	case receivedFromHost2 = <-outGuest2:
	default:
		t.Error("Guest2 didn't receive the message from the host")
	}
	if !bytes.Equal(receivedFromHost1, bytesFromHost) || !bytes.Equal(receivedFromHost2, bytesFromHost) {
		t.Error("Bytes sent by the host and received by the guests are not equal:", string(receivedFromHost1), string(receivedFromHost2))
	}
}

func TestConveyorSendsDirectMessages(t *testing.T) {
	conveyor := NewConveyor()
	inHost := mCh()
	inGuest := bCh()
	outHost, _ := conveyor.CreateRoom("Chat", inHost)
	outGuest, _ := conveyor.AddGuest("Chat", "Andrey", permsAll, inGuest)
	bytesFromHost := []byte("I'm Host")
	bytesFromGuest := []byte("I'm Guest")
	inHost <- rooms.Message{To: "Andrey", Bytes: bytesFromHost}
	inGuest <- bytesFromGuest
	receivedFromHost := <-outGuest
	receivedFromGuest := <-outHost
	expectedFromGuest := rooms.Message{Bytes: bytesFromGuest, From: "Andrey"}
	if !bytes.Equal(bytesFromHost, receivedFromHost) {
		t.Error("Bytes sent by the host and received by the guest are not equal")
	}
	if res := compareMessages(expectedFromGuest, receivedFromGuest); res != "" {
		t.Error(res)
	}
}

func BenchmarkBroadcast(b *testing.B) {
	const N = 10000
	conveyor := NewConveyor()
	fromHost := mCh()
	_, _ = conveyor.CreateRoom("Chat", fromHost)
	toGuests := make(map[rooms.GuestID]<-chan []byte)
	// Create N guests
	for i := 0; i < N; i++ {
		uID := rooms.GuestID(strconv.Itoa(i))
		toGuest, _ := conveyor.AddGuest("Chat", uID, permsAll, bCh())
		toGuests[uID] = toGuest
	}
	b.Run("Broadcast", func(b *testing.B) {
		bytesMsg := []byte("Test message")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			fromHost <- rooms.Message{Bytes: bytesMsg, To: ""}
			for _, toGuest := range toGuests {
				<-toGuest
			}
		}
	})
	b.Run("Direct", func(b *testing.B) {
		bytesMsg := []byte("Test message")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			uID := rooms.GuestID(strconv.Itoa(int(rand.Int31n(N))))
			fromHost <- rooms.Message{Bytes: bytesMsg, To: uID}
			<-toGuests[uID]
		}
	})
}
