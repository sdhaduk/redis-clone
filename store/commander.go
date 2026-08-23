package store

import (
	"redis-clone/resp"

	"strings"
	"time"
)

func RunCommander(requests chan Message) {
	store := make(map[string]entry)
	ticker := time.NewTicker(time.Millisecond * time.Duration(100))
	defer ticker.Stop()

	for {
		select {
		case msg, ok := <-requests:
			if !ok {
				return
			}
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
				msg.Reply <- resp.NewError("ERR unknown command name")
			}

		case <-ticker.C:
			sweep(store)
		}
	}
}
