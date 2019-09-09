// Copyright 2016 The rooms Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package errors

import (
	"io"
	"testing"

	"github.com/adyatlov/rooms"
)

func TestMarshal(t *testing.T) {
	room := rooms.RoomID("chat")
	guest := rooms.RoomID("Irina")

	// Single error. No guest is set, so we will have a zero-length field inside.
	e1 := E(Op("Join"), room, IO, "network unreachable")

	// Nested error.
	e2 := E(Op("Expel"), room, guest, Other, e1)

	b := MarshalError(e2)
	e3 := UnmarshalError(b)

	in := e2.(*Error)
	out := e3.(*Error)
	// Compare elementwise.
	if in.Room != out.Room {
		t.Errorf("expected Room %q; got %q", in.Room, out.Room)
	}
	if in.Guest != out.Guest {
		t.Errorf("expected Guest %q; got %q", in.Guest, out.Guest)
	}
	if in.Op != out.Op {
		t.Errorf("expected Op %q; got %q", in.Op, out.Op)
	}
	if in.Kind != out.Kind {
		t.Errorf("expected kind %d; got %d", in.Kind, out.Kind)
	}
	// Note that error will have lost type information, so just check its Error string.
	if in.Err.Error() != out.Err.Error() {
		t.Errorf("expected Err %q; got %q", in.Err, out.Err)
	}
}

func TestSeparator(t *testing.T) {
	defer func(prev string) {
		Separator = prev
	}(Separator)
	Separator = ":: "

	// Same pattern as above.
	room := rooms.RoomID("chat")
	guest := rooms.GuestID("Andrey")

	// Single error. No guest is set, so we will have a zero-length field inside.
	e1 := E(Op("Get"), room, IO, "network unreachable")

	// Nested error.
	e2 := E(Op("Read"), room, guest, Other, e1)

	want := "Operation: Read; Room: chat; Guest: Andrey; Kind: I/O error; Operation: Get; network unreachable"
	if errorAsString(e2) != want {
		t.Errorf("expected %q; got %q", want, e2)
	}
}

func TestDoesNotChangePrevioguestror(t *testing.T) {
	err := E(Permission)
	err2 := E(Op("I will NOT modify err"), err)

	expected := "Operation: I will NOT modify err; Kind: permission denied"
	if errorAsString(err2) != expected {
		t.Fatalf("Expected %q, got %q", expected, err2)
	}
	kind := err.(*Error).Kind
	if kind != Permission {
		t.Fatalf("Expected kind %v, got %v", Permission, kind)
	}
}

func TestNoArgs(t *testing.T) {
	defer func() {
		err := recover()
		if err == nil {
			t.Fatal("E() did not panic")
		}
	}()
	_ = E()
}

type matchTest struct {
	err1, err2 error
	matched    bool
}

const (
	room1 = rooms.RoomID(1)
	room2 = rooms.RoomID(2)
	john  = rooms.GuestID(1)
	jane  = rooms.GuestID(2)
)

const (
	op  = Op("Op")
	op1 = Op("Op1")
	op2 = Op("Op2")
)

var matchTests = []matchTest{
	// Errors not of type *Error fail outright.
	{nil, nil, false},
	{io.EOF, io.EOF, false},
	{E(io.EOF), io.EOF, false},
	{io.EOF, E(io.EOF), false},
	// Success. We can drop fields from the first argument and still match.
	{E(io.EOF), E(io.EOF), true},
	{E(op, Invalid, io.EOF, jane, room1), E(op, Invalid, io.EOF, jane, room1), true},
	{E(op, Invalid, io.EOF, jane), E(op, Invalid, io.EOF, jane, room1), true},
	{E(op, Invalid, io.EOF), E(op, Invalid, io.EOF, jane, room1), true},
	{E(op, Invalid), E(op, Invalid, io.EOF, jane, room1), true},
	{E(op), E(op, Invalid, io.EOF, jane, room1), true},
	// Failure.
	{E(io.EOF), E(io.ErrClosedPipe), false},
	{E(op1), E(op2), false},
	{E(Invalid), E(Permission), false},
	{E(jane), E(john), false},
	{E(room1), E(room2), false},
	{E(op, Invalid, io.EOF, jane, room1), E(op, Invalid, io.EOF, john, room1), false},
	{E(room1, Str("something")), E(room1), false}, // Test nil error on rhs.
	// Nested *Errors.
	{E(op1, E(room1)), E(op1, john, E(op2, jane, room1)), true},
	{E(op1, room1), E(op1, john, E(op2, jane, room1)), false},
	{E(op1, E(room1)), E(op1, john, Str(E(op2, jane, room1).Error())), false},
}

func TestMatch(t *testing.T) {
	for _, test := range matchTests {
		matched := Match(test.err1, test.err2)
		if matched != test.matched {
			t.Errorf("Match(%q, %q)=%t; want %t", test.err1, test.err2, matched, test.matched)
		}
	}
}

type kindTest struct {
	err  error
	kind Kind
	want bool
}

var kindTests = []kindTest{
	// Non-Error errors.
	{nil, NotExist, false},
	{Str("not an *Error"), NotExist, false},

	// Basic comparisons.
	{E(NotExist), NotExist, true},
	{E(Exist), NotExist, false},
	{E("no kind"), NotExist, false},
	{E("no kind"), Other, false},

	// Nested *Error values.
	{E("Nesting", E(NotExist)), NotExist, true},
	{E("Nesting", E(Exist)), NotExist, false},
	{E("Nesting", E("no kind")), NotExist, false},
	{E("Nesting", E("no kind")), Other, false},
}

func TestKind(t *testing.T) {
	for _, test := range kindTests {
		got := Is(test.kind, test.err)
		if got != test.want {
			t.Errorf("Is(%q, %q)=%t; want %t", test.kind, test.err, got, test.want)
		}
	}
}

// errorAsString returns the string form of the provided error value.
// If the given string is an *Error, the stack information is removed
// before the value is stringified.
func errorAsString(err error) string {
	if e, ok := err.(*Error); ok {
		e2 := *e
		return e2.Error()
	}
	return err.Error()
}
