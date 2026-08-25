package store

import (
	"redis-clone/resp"

	"strings"
	"time"
)

func dispatch(store map[string]entry, args []string) resp.Value {
	switch strings.ToUpper(args[0]) {
	case "SET":
		return cmdSet(store,args)
	case "GET":
		return cmdGet(store, args)
	case "EXPIRE":
		return cmdExpire(store, args)
	case "TTL":
		return cmdTTL(store, args)
	case "DEL":
		return cmdDel(store, args)
	case "EXISTS":
		return cmdExists(store, args)
	case "KEYS":
		return cmdKeys(store, args)
	case "INCR":
		return cmdIncr(store, args)
	case "DECR":
		return cmdDecr(store, args)
	case "LPUSH":
		return cmdLPush(store, args)
	case "RPUSH":
		return cmdRPush(store, args)
	case "LLEN":
		return cmdLLen(store, args)
	case "LRANGE":
		return cmdLRange(store, args)
	case "RPOP":
		return cmdRPop(store, args)
	case "LPOP":
		return cmdLPop(store, args)
	case "SADD":
		return cmdSAdd(store, args)
	case "SREM":
		return cmdSRem(store, args)
	case "SISMEMBER":
		return cmdSIsMember(store, args)
	case "SMEMBERS":
		return cmdSMembers(store, args)
	case "SCARD":
		return cmdSCard(store, args)
	case "ZADD":
		return cmdZAdd(store, args)
	case "ZSCORE":
		return cmdZScore(store, args)
	case "ZRANGE":
		return cmdZRange(store, args)
	case "ZRANK":
		return cmdZRank(store, args)
	case "ZREM":
		return cmdZRem(store, args)
	default:
		return resp.NewError("ERR unknown command name")
	}
}

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
			msg.Reply <- dispatch(store, msg.Args)

		case <-ticker.C:
			sweep(store)
		}
	}
}
