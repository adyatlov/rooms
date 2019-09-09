// Package errors defines the error handling used in Rooms.
// Inspired by https://commandcenter.blogspot.com/2017/12/error-handling-in-upspin.html
package errors

import (
	"encoding"
	"encoding/binary"
	"fmt"
	"log"
	"runtime"
	"strings"

	"github.com/adyatlov/rooms"
)

// Error is the type that implements the error interface.
// It contains a number of fields, each of different type.
// An Error value may leave some values unset.
type Error struct {
	// Room is the name of the room where the operation is performed.
	Room rooms.RoomID
	// Guest is the name of the guest attempting the operation.
	Guest rooms.GuestID
	// Op is the operation being performed, usually the name of the method
	// being invoked (Join, Expel, etc.). It should not contain an at sign @.
	Op Op
	// Kind is the class of error, such as permission failure,
	// or "Other" if its class is unknown or irrelevant.
	Kind Kind
	// The underlying error that triggered this one, if any.
	Err error
}

func (e *Error) isZero() bool {
	return e.Room == "" && e.Guest == "" && e.Op == "" && e.Kind == 0 && e.Err == nil
}

// Check interface compatibility.
var (
	_ error                      = (*Error)(nil)
	_ encoding.BinaryUnmarshaler = (*Error)(nil)
	_ encoding.BinaryMarshaler   = (*Error)(nil)
)

// Op describes an operation, usually as the package and method,
// such as "simpleroom/room.Join".
type Op string

// Separator is the string used to separate nested errors. By
// default, to make errors easier on the eye, nested errors are
// indented on a new line. A server may instead choose to keep each
// error on a single line by modifying the separator string, perhaps
// to ":: ".
var Separator = ":\n\t"

// Kind defines the kind of error.
type Kind uint8

// Kinds of errors.
//
// The values of the error kinds are common between both
// clients and servers. Do not reorder this list or remove
// any items since that will change their values.
// New items must be added only to the end.
const (
	Other      Kind = iota // Unclassified error.
	Invalid                // Invalid operation for this type of item.
	Permission             // Permission denied.
	IO                     // External I/O error such as network failure.
	Exist                  // Item already exists.
	NotExist               // Item does not exist.
	Transient              // A transient error.
)

func (k Kind) String() string {
	switch k {
	case Other:
		return "other error"
	case Invalid:
		return "invalid operation"
	case Permission:
		return "permission denied"
	case IO:
		return "I/O error"
	case Exist:
		return "item already exists"
	case NotExist:
		return "item does not exist"
	case Transient:
		return "transient error"
	}
	return "unknown error kind"
}

// E builds an error value from its arguments.
// There must be at least one argument or E panics.
// The type of each argument determines its meaning.
// If more than one argument of a given type is presented,
// only the last one is recorded.
//
// The types are:
//	rooms.RoomID
//		Room is the name of the room where the operation is performed.
//	room.GuestID
//      GuestID is the name of the guest attempting the operation.
//	errors.Op
//		The operation being performed, usually the method
//		being invoked (Join, Expel, etc.).
//	string
//		Treated as an error message and assigned to the
//		Err field after a call to errors.Str.
//	errors.Kind
//		The class of error, such as permission failure.
//	error
//		The underlying error that triggered this one.
//
// If the error is printed, only those items that have been
// set to non-zero values will appear in the result.
//
// If Kind is not specified or Other, we set it to the Kind of
// the underlying error.
func E(args ...interface{}) error {
	if len(args) == 0 {
		panic("call to errors.E with no arguments")
	}
	e := &Error{}
	for _, arg := range args {
		switch arg := arg.(type) {
		case rooms.RoomID:
			e.Room = arg
		case rooms.GuestID:
			e.Guest = arg
		case Op:
			e.Op = arg
		case string:
			e.Err = Str(arg)
		case Kind:
			e.Kind = arg
		case *Error:
			// Make a copy
			copy := *arg
			e.Err = &copy
		case error:
			e.Err = arg
		default:
			_, file, line, _ := runtime.Caller(1)
			log.Printf("errors.E: bad call from %s:%d: %v", file, line, args)
			return Errorf("unknown type %T, value %v in error call", arg, arg)
		}
	}

	prev, ok := e.Err.(*Error)
	if !ok {
		return e
	}

	// The previous error was also one of ours. Suppress duplications
	// so the message won't contain the same kind, room name or guest name
	// twice.
	if prev.Room == e.Room {
		prev.Room = ""
	}
	if prev.Guest == e.Guest {
		prev.Guest = ""
	}
	if prev.Kind == e.Kind {
		prev.Kind = Other
	}
	// If this error has Kind unset or Other, pull up the inner one.
	if e.Kind == Other {
		e.Kind = prev.Kind
		prev.Kind = Other
	}
	return e
}
func (e *Error) Error() string {
	var msg []string
	if e.Op != "" {
		msg = append(msg, "Operation: "+string(e.Op))
	}
	if e.Room != "" {
		msg = append(msg, "Room: "+string(e.Room))
	}
	if e.Guest != "" {
		msg = append(msg, "Guest: "+string(e.Guest))
	}
	if e.Kind != 0 {
		msg = append(msg, "Kind: "+e.Kind.String())
	}
	if e.Err != nil {
		// Indent on new line if we are cascading non-empty errors.
		if prevErr, ok := e.Err.(*Error); ok {
			if !prevErr.isZero() {
				if len(msg) == 0 {
					msg = append(msg, Separator)
				}
				msg = append(msg, e.Err.Error())
			}
		} else {
			msg = append(msg, e.Err.Error())
		}
	}
	if len(msg) == 0 {
		return "no error"
	}
	return strings.Join(msg, "; ")
}

// Recreate the errors.New functionality of the standard Go errors package
// so we can create simple text errors when needed.

// Str returns an error that formats as the given text. It is intended to
// be used as the error-typed argument to the E function.
func Str(text string) error {
	return &errorString{text}
}

// errorString is a trivial implementation of error.
type errorString struct {
	s string
}

func (e *errorString) Error() string {
	return e.s
}

// Errorf is equivalent to fmt.Errorf, but allows clients to import only this
// package for all error handling.
func Errorf(format string, args ...interface{}) error {
	return &errorString{fmt.Sprintf(format, args...)}
}

// MarshalAppend marshals err into a byte slice. The result is appended to b,
// which may be nil.
// It returns the argument slice unchanged if the error is nil.
func (e *Error) MarshalAppend(b []byte) []byte {
	if e == nil {
		return b
	}
	b = appendString(b, string(e.Room))
	b = appendString(b, string(e.Guest))
	b = appendString(b, string(e.Op))
	var tmp [16]byte // For use by PutVariant.
	N := binary.PutVarint(tmp[:], int64(e.Kind))
	b = append(b, tmp[:N]...)
	b = MarshalErrorAppend(e.Err, b)
	return b
}

// MarshalBinary marshals its receiver into a byte slice, which it returns.
// It returns nil if the error is nil. The returned error is always nil.
func (e *Error) MarshalBinary() ([]byte, error) {
	return e.MarshalAppend(nil), nil
}

// MarshalErrorAppend marshals an arbitrary error into a byte slice.
// The result is appended to b, which may be nil.
// It returns the argument slice unchanged if the error is nil.
// If the error is not an *Error, it just records the result of err.Error().
// Otherwise it encodes the full Error struct.
func MarshalErrorAppend(err error, b []byte) []byte {
	if err == nil {
		return b
	}
	if e, ok := err.(*Error); ok {
		// This is an errors.Error. Mark it as such.
		b = append(b, 'E')
		return e.MarshalAppend(b)
	}
	// Ordinary error.
	b = append(b, 'e')
	b = appendString(b, err.Error())
	return b

}

// MarshalError marshals an arbitrary error and returns the byte slice.
// If the error is nil, it returns nil.
// It returns the argument slice unchanged if the error is nil.
// If the error is not an *Error, it just records the result of err.Error().
// Otherwise it encodes the full Error struct.
func MarshalError(err error) []byte {
	return MarshalErrorAppend(err, nil)
}

// UnmarshalBinary unmarshals the byte slice into the receiver, which must be non-nil.
// The returned error is always nil.
func (e *Error) UnmarshalBinary(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	data, b := getBytes(b)
	if data != nil {
		e.Room = rooms.RoomID(data)
	}
	data, b = getBytes(b)
	if data != nil {
		e.Guest = rooms.GuestID(data)
	}
	data, b = getBytes(b)
	if data != nil {
		e.Op = Op(data)
	}
	k, N := binary.Varint(b)
	e.Kind = Kind(k)
	b = b[N:]
	e.Err = UnmarshalError(b)
	return nil
}

// UnmarshalError unmarshals the byte slice into an error value.
// If the slice is nil or empty, it returns nil.
// Otherwise the byte slice must have been created by MarshalError or
// MarshalErrorAppend.
// If the encoded error was of type *Error, the returned error value
// will have that underlying type. Otherwise it will be just a simple
// value that implements the error interface.
func UnmarshalError(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	code := b[0]
	b = b[1:]
	switch code {
	case 'e':
		// Plain error.
		var data []byte
		data, b = getBytes(b)
		if len(b) != 0 {
			log.Printf("Unmarshal error: trailing bytes")
		}
		return Str(string(data))
	case 'E':
		// Error value.
		var err Error
		err.UnmarshalBinary(b)
		return &err
	default:
		log.Printf("Unmarshal error: corrupt data %q", b)
		return Str(string(b))
	}
}

func appendString(b []byte, str string) []byte {
	var tmp [16]byte // For use by PutUvarint.
	N := binary.PutUvarint(tmp[:], uint64(len(str)))
	b = append(b, tmp[:N]...)
	b = append(b, str...)
	return b
}

func appendUint64(b []byte, num uint64) []byte {
	buf := make([]byte, binary.MaxVarintLen64)
	N := binary.PutUvarint(buf, uint64(num))
	b = append(b, buf[:N]...)
	return b
}

// getBytes unmarshals the byte slice at b (uvarint count followed by bytes)
// and returns the slice followed by the remaining bytes.
// If there is insufficient data, both return values will be nil.
func getBytes(b []byte) (data, remaining []byte) {
	u, N := binary.Uvarint(b)
	if len(b) < N+int(u) {
		log.Printf("Unmarshal error: bad encoding")
		return nil, nil
	}
	if N == 0 {
		log.Printf("Unmarshal error: bad encoding")
		return nil, b
	}
	return b[N : N+int(u)], b[N+int(u):]
}

// Match compares its two error arguments. It can be used to check
// for expected errors in tests. Both arguments must have underlying
// type *Error or Match will return false. Otherwise it returns true
// iff every non-zero element of the first error is equal to the
// corresponding element of the second.
// If the Err field is a *Error, Match recurs on that field;
// otherwise it compares the strings returned by the Error methods.
// Elements that are in the second argument but not present in
// the first are ignored.
//
// For example,
//	Match(errors.E(upspin.GuestID("joe@schmoe.com"), errors.Permission), err)
// tests whether err is an Error with Kind=Permission and Guest=joe@schmoe.com.
func Match(err1, err2 error) bool {
	e1, ok := err1.(*Error)
	if !ok {
		return false
	}
	e2, ok := err2.(*Error)
	if !ok {
		return false
	}
	if e1.Room != "" && e2.Room != e1.Room {
		return false
	}
	if e1.Guest != "" && e2.Guest != e1.Guest {
		return false
	}
	if e1.Op != "" && e2.Op != e1.Op {
		return false
	}
	if e1.Kind != Other && e2.Kind != e1.Kind {
		return false
	}
	if e1.Err != nil {
		if _, ok := e1.Err.(*Error); ok {
			return Match(e1.Err, e2.Err)
		}
		if e2.Err == nil || e2.Err.Error() != e1.Err.Error() {
			return false
		}
	}
	return true
}

// Is reports whether err is an *Error of the given Kind.
// If err is nil then Is returns false.
func Is(kind Kind, err error) bool {
	e, ok := err.(*Error)
	if !ok {
		return false
	}
	if e.Kind != Other {
		return e.Kind == kind
	}
	if e.Err != nil {
		return Is(kind, e.Err)
	}
	return false
}
