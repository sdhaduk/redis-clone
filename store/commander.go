package store

import (
	"fmt"
	"log"
	"redis-clone/resp"
	"strconv"

	"strings"
	"time"
)

func logForm(store map[string]entry, args []string) [][]string {
	cmds := [][]string{}
	cmd := strings.ToUpper(args[0])
	if cmd == "EXPIRE" {
		ent, ok := lookup(store, args[1])
		if !ok {
			return nil
		}
		deadline := strconv.FormatInt(ent.expiresAt.UnixMilli(), 10)
		cmds = append(cmds, []string{"PEXPIREAT", args[1], deadline})
		return cmds
	}
	if cmd == "SET" && len(args) == 5 {
		ent, ok := lookup(store, args[1])
		if !ok {
			return nil
		}
		deadline := strconv.FormatInt(ent.expiresAt.UnixMilli(), 10) 
		cmds = append(cmds, []string{"SET", args[1], args[2]})
		cmds = append(cmds, []string{"PEXPIREAT", args[1], deadline})
		return cmds
	}
	cmds = append(cmds, args)
	return cmds
}

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
	case "HSET":
		return cmdHSet(store, args)
	case "HGET":
		return cmdHGet(store, args)
	case "HDEL":
		return cmdHDel(store, args)
	case "HGETALL":
		return cmdHGetAll(store, args)
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
	case "PEXPIREAT":
		return cmdPExpireAt(store, args)
	default:
		return resp.NewError("ERR unknown command name")
	}
}

var writeCommands = map[string]bool{
	"SET": true,
	"DEL": true,
	"EXPIRE": true,
	"INCR": true,
	"DECR": true,
	"LPUSH": true,
	"RPUSH": true,
	"LPOP": true,
	"RPOP": true,
	"HSET": true,
	"HDEL": true,
	"SADD": true,
	"SREM": true,
	"ZADD": true,
	"ZREM": true,
	"PEXPIREAT": true,
}

func RunCommander(requests chan Message, aof *AOF) {
	store := make(map[string]entry)
	if aof != nil {
		aof.load(store)
	}
	ticker := time.NewTicker(time.Millisecond * time.Duration(100))
	defer ticker.Stop()

	for {
		select {
		case msg, ok := <-requests:
			if !ok {
				return
			}
			if strings.ToUpper(msg.Args[0]) == "REWRITEAOF" && aof != nil {
				err := aof.rewrite(store)
				reply := resp.Value{}
				
				if err != nil {
					reply = resp.Value{Kind: resp.Error, Str: fmt.Sprintf("Error rewriting AOF: %v", err)}
				} else {
					reply = resp.Value{Kind: resp.Integer, Num: 1}
				}

				msg.Reply <- reply
				continue
			}
			reply := dispatch(store, msg.Args)
			if aof != nil {
				if writeCommands[strings.ToUpper(msg.Args[0])] && reply.Kind != resp.Error {
					cmds := logForm(store, msg.Args)
					for _, cmd := range cmds {
						res := resp.Command(cmd)
						err := aof.Append(res.Encode())
						if err != nil {
							log.Fatal(err)
						}
					}
				}
			}
			msg.Reply <- reply

		case <-ticker.C:
			sweep(store)
		}
	}
}
