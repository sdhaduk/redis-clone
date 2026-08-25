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

			case "LPUSH":
				msg.Reply <- cmdLPush(store, msg.Args)

			case "RPUSH":
				msg.Reply <- cmdRPush(store, msg.Args)

			case "LLEN":
				msg.Reply <- cmdLLen(store, msg.Args)

			case "LRANGE":
				msg.Reply <- cmdLRange(store, msg.Args)

			case "RPOP":
				msg.Reply <- cmdRPop(store, msg.Args)

			case "LPOP":
				msg.Reply <- cmdLPop(store, msg.Args)

			case "SADD":
				msg.Reply <- cmdSAdd(store, msg.Args)

			case "SREM":
				msg.Reply <- cmdSRem(store, msg.Args)

			case "SISMEMBER":
				msg.Reply <- cmdSIsMember(store, msg.Args)

			case "SMEMBERS":
				msg.Reply <- cmdSMembers(store, msg.Args)

			case "SCARD":
				msg.Reply <- cmdSCard(store, msg.Args)

			case "ZADD":
				msg.Reply <- cmdZAdd(store, msg.Args)

			case "ZSCORE":
				msg.Reply <- cmdZScore(store, msg.Args)

			case "ZRANGE":
				msg.Reply <- cmdZRange(store, msg.Args)

			case "ZRANK":
				msg.Reply <- cmdZRank(store, msg.Args)

			case "ZREM":
				msg.Reply <- cmdZRem(store, msg.Args)

			default:
				msg.Reply <- resp.NewError("ERR unknown command name")
			}

		case <-ticker.C:
			sweep(store)
		}
	}
}
