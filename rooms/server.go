package main

import (
	"log"

	"github.com/adyatlov/rooms/simpleconveyor"
	"github.com/adyatlov/rooms/wsexposer"
)

func main() {
	exposer := &wsexposer.Exposer{Port: 8080, Host: "0.0.0.0"}
	conveyor := simpleconveyor.NewConveyor()
	err := exposer.Expose(conveyor)
	if err != nil {
		log.Println(err)
	}
}
