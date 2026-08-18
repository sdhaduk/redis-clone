package main

import "strings"

type Message struct {
	Args []string
	Reply chan Value
}

func RunCommander(requests chan Message) {
	store := make(map[string]string)

	for msg := range requests {
		switch strings.ToUpper(msg.Args[0]) {
		case "SET":
			if len(msg.Args) != 3 {
				msg.Reply <- Value{Kind: Error, Str: "ERR wrong number of arguments for 'set' command"}
				continue
			}
			store[msg.Args[1]] = msg.Args[2]
			msg.Reply <- Value{Kind: SimpleString, Str: "OK"}
		
		case "GET":
			if len(msg.Args) != 2 {
				msg.Reply <- Value{Kind: Error, Str: "ERR wrong number of arguments for 'get' command"}
				continue
			}
			val, ok := store[msg.Args[1]]
			if ok {
				msg.Reply <- Value{Kind: BulkString, Str: val}
				continue
			}
			msg.Reply <- Value{Kind: Null}
		
		default:
			msg.Reply <- Value{Kind: Error, Str: "ERR unknown command name"}
		}
	}
}