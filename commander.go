package main

import (
	"strings"
	"time"
)

type Message struct {
	Args []string
	Reply chan Value
}

type entry struct {
	val string
	expiresAt time.Time
}

func RunCommander(requests chan Message) {
	store := make(map[string]entry)

	for msg := range requests {
		switch strings.ToUpper(msg.Args[0]) {
		case "SET":
			msg.Reply <- cmdSet(store, msg.Args)
			
		case "GET":
			msg.Reply <- cmdGet(store, msg.Args)		
		
		case "EXPIRE":
			msg.Reply <- cmdExpire(store, msg.Args)

		case "TTL":
			msg.Reply <- cmdTTL(store, msg.Args)

		case "DEL":
			msg.Reply <- cmdDel(store, msg.Args)
		
		case "EXISTS":
			msg.Reply <- cmdExists(store, msg.Args)
		
		case "KEYS":
			msg.Reply <- cmdKeys(store, msg.Args)
			
		case "INCR":
			msg.Reply <- cmdIncr(store, msg.Args)
		
		case "DECR":
			msg.Reply <- cmdDecr(store, msg.Args)

		default:
			msg.Reply <- NewError("ERR unknown command name")
		}
	}
}