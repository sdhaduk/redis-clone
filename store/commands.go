package store

import (
	"redis-clone/resp"
	"redis-clone/skiplist"

	"fmt"
	"path"
	"strconv"
	"strings"
	"time"
)

type Message struct {
	Args  []string
	Reply chan resp.Value
}

type DataType int

const (
	String DataType = iota
	List
	Hash
	Set
	ZSet
)

type ZSetData struct {
	members map[string]float64
	order   *skiplist.SkipList
}

type entry struct {
	Kind      DataType
	Str       string
	List      []string
	Hash      map[string]string
	Set       map[string]struct{}
	ZSet      ZSetData
	expiresAt time.Time
}

var wrongTypeErr resp.Value = resp.Value{Kind: resp.Error, Str: "WRONGTYPE Operation against a key holding the wrong kind of value"}

func lookup(store map[string]entry, key string) (entry, bool) {
	ent, ok := store[key]
	if !ok {
		return entry{}, false
	}
	if !ent.expiresAt.IsZero() {
		if time.Now().After(ent.expiresAt) {
			delete(store, key)
			return entry{}, false
		}
	}
	return ent, true
}

func sweep(store map[string]entry) {
	count := 0
	for key := range store {
		lookup(store, key)
		count += 1
		if count >= 20 {
			break
		}
	}
}

func normalizeRange(start, stop, length int) (int, int, bool) {
	if start < 0 {
		start += length
	}
	if stop < 0 {
		stop += length
	}

	if start < 0 {
		start = 0
	}
	if stop > length-1 {
		stop = length - 1
	}

	if start > stop || start >= length {
		return 0, 0, false
	}
	return start, stop, true
}

func cmdGet(store map[string]entry, args []string) resp.Value {
	if len(args) != 2 {
		return resp.NewError("ERR wrong number of arguments for 'GET' command")
	}
	ent, ok := lookup(store, args[1])
	if ok {
		if ent.Kind != String {
			return wrongTypeErr
		}
		return resp.NewBulkString(ent.Str)
	}
	return resp.Value{Kind: resp.Null}
}

func cmdSet(store map[string]entry, args []string) resp.Value {
	if len(args) == 3 {
		store[args[1]] = entry{Str: args[2], Kind: String}
		return resp.NewSimpleString("OK")
	}

	if len(args) == 5 {
		count, err := strconv.ParseInt(args[4], 10, 64)
		if err != nil {
			return resp.NewError(fmt.Sprintf("ERR %s", err))
		}
		if count <= 0 {
			return resp.NewError("ERR invalid expire time in 'SET' command")
		}

		if strings.ToUpper(args[3]) == "EX" {
			store[args[1]] = entry{Str: args[2], expiresAt: time.Now().Add(time.Second * time.Duration(count)), Kind: String}
			return resp.NewSimpleString("OK")
		}
		if strings.ToUpper(args[3]) == "PX" {
			store[args[1]] = entry{Str: args[2], expiresAt: time.Now().Add(time.Millisecond * time.Duration(count)), Kind: String}
			return resp.NewSimpleString("OK")
		}

		return resp.NewError(fmt.Sprintf("ERR command '%s' not recognized", args[3]))
	}

	return resp.NewError("ERR wrong number of arguments for 'SET' command")
}

func cmdExpire(store map[string]entry, args []string) resp.Value {
	if len(args) == 3 {
		count, err := strconv.ParseInt(args[2], 10, 64)
		if err != nil {
			return resp.NewError(fmt.Sprintf("ERR %s", err))
		}
		if count <= 0 {
			return resp.NewError("ERR invalid expire time in 'EXPIRE' command")
		}

		ent, ok := lookup(store, args[1])
		if ok {
			ent.expiresAt = time.Now().Add(time.Second * time.Duration(count))
			store[args[1]] = ent
			return resp.NewInteger(1)
		}
		return resp.NewInteger(0)
	}

	return resp.NewError("ERR wrong number of arguments for 'EXPIRE' command")
}

func cmdTTL(store map[string]entry, args []string) resp.Value {
	if len(args) == 2 {
		ent, ok := lookup(store, args[1])
		if !ok {
			return resp.NewInteger(-2)
		}
		if ent.expiresAt.IsZero() {
			return resp.NewInteger(-1)
		}
		return resp.NewInteger(int64(time.Until(ent.expiresAt).Round(time.Second) / time.Second))
	}
	return resp.NewError("ERR wrong number of arguments for 'TTL' command")
}

func cmdDel(store map[string]entry, args []string) resp.Value {
	if len(args) > 1 {
		var count int64 = 0
		for _, key := range args[1:] {
			_, ok := lookup(store, key)
			if ok {
				count += 1
				delete(store, key)
			}
		}
		return resp.NewInteger(count)
	}
	return resp.NewError("ERR wrong number of arguments for 'DEL' command")
}

func cmdExists(store map[string]entry, args []string) resp.Value {
	if len(args) > 1 {
		var count int64 = 0
		for _, key := range args[1:] {
			_, ok := lookup(store, key)
			if ok {
				count += 1
			}
		}
		return resp.NewInteger(count)
	}
	return resp.NewError("ERR wrong number of arguments for 'EXISTS' command")
}

func cmdKeys(store map[string]entry, args []string) resp.Value {
	if len(args) == 2 {
		elems := []resp.Value{}
		for key := range store {
			matched, err := path.Match(args[1], key)
			if err != nil {
				return resp.NewError(fmt.Sprintf("ERR %s", err))
			}
			if matched {
				_, ok := lookup(store, key)
				if ok {
					elems = append(elems, resp.NewBulkString(key))
				}
			}
		}
		return resp.Value{Kind: resp.Array, Elems: elems}
	}
	return resp.NewError("ERR wrong number of arguments for 'KEYS' command")
}

func cmdIncr(store map[string]entry, args []string) resp.Value {
	if len(args) == 2 {
		ent, ok := lookup(store, args[1])
		if !ok {
			store[args[1]] = entry{Str: "1", Kind: String}
			return resp.NewInteger(1)
		}
		if ent.Kind != String {
			return wrongTypeErr
		}
		num, err := strconv.ParseInt(ent.Str, 10, 64)
		if err != nil {
			return resp.NewError("ERR value is not an integer or out of range")
		}
		store[args[1]] = entry{Str: strconv.FormatInt(num+1, 10), expiresAt: ent.expiresAt, Kind: String}
		return resp.NewInteger(num + 1)
	}
	return resp.NewError("ERR wrong number of arguments for 'INCR' command")
}

func cmdDecr(store map[string]entry, args []string) resp.Value {
	if len(args) == 2 {
		ent, ok := lookup(store, args[1])
		if !ok {
			store[args[1]] = entry{Str: "-1", Kind: String}
			return resp.NewInteger(-1)
		}
		if ent.Kind != String {
			return wrongTypeErr
		}
		num, err := strconv.ParseInt(ent.Str, 10, 64)
		if err != nil {
			return resp.NewError("ERR value is not an integer or out of range")
		}
		store[args[1]] = entry{Str: strconv.FormatInt(num-1, 10), expiresAt: ent.expiresAt, Kind: String}
		return resp.NewInteger(num - 1)
	}
	return resp.NewError("ERR wrong number of arguments for 'DECR' command")
}

func cmdLPush(store map[string]entry, args []string) resp.Value {
	if len(args) >= 3 {
		ent, ok := lookup(store, args[1])
		if ok && ent.Kind != List {
			return wrongTypeErr
		}
		new_list := make([]string, len(args)-2)
		args_counter := 2
		for i := len(new_list) - 1; i >= 0; i-- {
			new_list[i] = args[args_counter]
			args_counter += 1
		}
		if !ok {
			store[args[1]] = entry{Kind: List, List: new_list}
			return resp.NewInteger(int64(len(new_list)))
		}
		new_list = append(new_list, ent.List...)
		store[args[1]] = entry{Kind: List, List: new_list, expiresAt: ent.expiresAt}
		return resp.NewInteger(int64(len(new_list)))
	}
	return resp.NewError("ERR wrong number of arguments for 'LPUSH' command")
}

func cmdRPush(store map[string]entry, args []string) resp.Value {
	if len(args) >= 3 {
		ent, ok := lookup(store, args[1])
		if ok && ent.Kind != List {
			return wrongTypeErr
		}
		new_list := make([]string, len(args)-2)
		args_counter := 2
		for i := 0; i < len(args)-2; i++ {
			new_list[i] = args[args_counter]
			args_counter += 1
		}
		if !ok {
			store[args[1]] = entry{Kind: List, List: new_list}
			return resp.NewInteger(int64(len(new_list)))
		}
		new_list = append(ent.List, new_list...)
		store[args[1]] = entry{Kind: List, List: new_list, expiresAt: ent.expiresAt}
		return resp.NewInteger(int64(len(new_list)))
	}
	return resp.NewError("ERR wrong number of arguments for 'RPUSH' command")
}

func cmdLLen(store map[string]entry, args []string) resp.Value {
	if len(args) == 2 {
		ent, ok := lookup(store, args[1])
		if !ok {
			return resp.NewInteger(0)
		}
		if ent.Kind != List {
			return wrongTypeErr
		}
		return resp.NewInteger(int64(len(ent.List)))
	}
	return resp.NewError("ERR wrong number of arguments for 'LLEN' command")
}

func cmdLRange(store map[string]entry, args []string) resp.Value {
	if len(args) == 4 {
		start, err := strconv.Atoi(args[2])
		if err != nil {
			return resp.NewError(fmt.Sprintf("ERR %s", err))
		}
		stop, err := strconv.Atoi(args[3])
		if err != nil {
			return resp.NewError(fmt.Sprintf("ERR %s", err))
		}
		ent, ok := lookup(store, args[1])
		if !ok {
			return resp.Value{Kind: resp.Array}
		}
		if ent.Kind != List {
			return wrongTypeErr
		}
		start, stop, ok = normalizeRange(start, stop, len(ent.List))
		if !ok {
			return resp.Value{Kind: resp.Array}
		}

		elems := []resp.Value{}
		for i := start; i <= stop; i++ {
			item := resp.Value{Kind: resp.BulkString, Str: ent.List[i]}
			elems = append(elems, item)
		}
		return resp.Value{Kind: resp.Array, Elems: elems}
	}
	return resp.NewError("ERR wrong number of arguments for 'LRANGE' command")
}

func cmdRPop(store map[string]entry, args []string) resp.Value {
	if len(args) == 2 {
		ent, ok := lookup(store, args[1])
		if !ok {
			return resp.Value{Kind: resp.Null}
		}
		if ent.Kind != List {
			return wrongTypeErr
		}
		popped := ent.List[len(ent.List)-1]
		updated_list := ent.List[:len(ent.List)-1]
		if len(updated_list) == 0 {
			delete(store, args[1])
		} else {
			ent.List = updated_list
			store[args[1]] = ent
		}
		return resp.NewBulkString(popped)
	}

	if len(args) == 3 {
		ent, ok := lookup(store, args[1])
		if !ok {
			return resp.Value{Kind: resp.Null}
		}
		if ent.Kind != List {
			return wrongTypeErr
		}
		count, err := strconv.Atoi(args[2])
		if err != nil {
			return resp.NewError(fmt.Sprintf("ERR %s", err))
		}
		if count <= 0 {
			return resp.NewError("ERR value is out of range, must be positive")
		}
		if count > len(ent.List) {
			count = len(ent.List)
		}
		items := []resp.Value{}
		for i := 0; i < count; i++ {
			items = append(items, resp.Value{Kind: resp.BulkString, Str: ent.List[len(ent.List)-1-i]})
		}
		updated_list := ent.List[:len(ent.List)-count]
		if len(updated_list) == 0 {
			delete(store, args[1])
		} else {
			ent.List = updated_list
			store[args[1]] = ent
		}
		return resp.Value{Kind: resp.Array, Elems: items}
	}
	return resp.NewError("ERR wrong number of arguments for 'RPOP' command")
}

func cmdLPop(store map[string]entry, args []string) resp.Value {
	if len(args) == 2 {
		ent, ok := lookup(store, args[1])
		if !ok {
			return resp.Value{Kind: resp.Null}
		}
		if ent.Kind != List {
			return wrongTypeErr
		}
		popped := ent.List[0]
		updated_list := ent.List[1:]
		if len(updated_list) == 0 {
			delete(store, args[1])
		} else {
			ent.List = updated_list
			store[args[1]] = ent
		}
		return resp.NewBulkString(popped)
	}

	if len(args) == 3 {
		ent, ok := lookup(store, args[1])
		if !ok {
			return resp.Value{Kind: resp.Null}
		}
		if ent.Kind != List {
			return wrongTypeErr
		}
		count, err := strconv.Atoi(args[2])
		if err != nil {
			return resp.NewError(fmt.Sprintf("ERR %s", err))
		}
		if count <= 0 {
			return resp.NewError("ERR value is out of range, must be positive")
		}
		if count > len(ent.List) {
			count = len(ent.List)
		}
		items := []resp.Value{}
		for i := 0; i < count; i++ {
			items = append(items, resp.Value{Kind: resp.BulkString, Str: ent.List[i]})
		}
		updated_list := ent.List[count:]
		if len(updated_list) == 0 {
			delete(store, args[1])
		} else {
			ent.List = updated_list
			store[args[1]] = ent
		}
		return resp.Value{Kind: resp.Array, Elems: items}
	}
	return resp.NewError("ERR wrong number of arguments for 'LPOP' command")
}

func HSet(store map[string]entry, args []string) resp.Value {
	if len(args) >= 4 {
		if len(args)%2 != 0 {
			return resp.NewError("ERR number of arguments for 'HSET' command must be even")
		}
		new := 0
		ent, ok := lookup(store, args[1])
		if !ok {
			ent = entry{Kind: Hash, Hash: make(map[string]string)}
		}
		if ent.Kind != Hash {
			return wrongTypeErr
		}
		idx := 2
		for idx < len(args) {
			_, ok := ent.Hash[args[idx]]
			if !ok {
				new += 1
			}
			ent.Hash[args[idx]] = args[idx+1]
			idx += 2
		}
		store[args[1]] = ent
		return resp.NewInteger(int64(new))
	}
	return resp.NewError("ERR wrong number of arguments for 'HSET' command")
}

func HGet(store map[string]entry, args []string) resp.Value {
	if len(args) == 3 {
		ent, ok := lookup(store, args[1])
		if !ok {
			return resp.Value{Kind: resp.Null}
		}
		if ent.Kind != Hash {
			return wrongTypeErr
		}
		val, ok := ent.Hash[args[2]]
		if !ok {
			return resp.Value{Kind: resp.Null}
		}
		return resp.NewBulkString(val)
	}
	return resp.NewError("ERR wrong number of arguments for 'HGET' command")
}

func HDel(store map[string]entry, args []string) resp.Value {
	if len(args) >= 3 {
		ent, ok := lookup(store, args[1])
		if !ok {
			return resp.NewInteger(int64(0))
		}
		if ent.Kind != Hash {
			return wrongTypeErr
		}
		count := 0
		for idx := 2; idx < len(args); idx++ {
			_, ok = ent.Hash[args[idx]]
			if !ok {
				continue
			}
			count += 1
			delete(ent.Hash, args[idx])
		}
		if len(ent.Hash) == 0 {
			delete(store, args[1])
		}
		return resp.NewInteger(int64(count))
	}
	return resp.NewError("ERR wrong number of arguments for 'HDEL' command")
}

func HGetAll(store map[string]entry, args []string) resp.Value {
	if len(args) == 2 {
		ent, ok := lookup(store, args[1])
		if !ok {
			return resp.Value{Kind: resp.Array}
		}
		if ent.Kind != Hash {
			return wrongTypeErr
		}
		elems := []resp.Value{}
		for key, val := range ent.Hash {
			elems = append(elems, resp.Value{Kind: resp.BulkString, Str: key}, resp.Value{Kind: resp.BulkString, Str: val})
		}
		return resp.Value{Kind: resp.Array, Elems: elems}
	}
	return resp.NewError("ERR wrong number of arguments for 'HGETALL' command")
}

func cmdSAdd(store map[string]entry, args []string) resp.Value {
	if len(args) >= 3 {
		ent, ok := lookup(store, args[1])
		if !ok {
			ent = entry{Kind: Set, Set: make(map[string]struct{})}
		}
		if ent.Kind != Set {
			return wrongTypeErr
		}
		count := 0
		for i := 2; i < len(args); i++ {
			_, ok := ent.Set[args[i]]
			if ok {
				continue
			}
			ent.Set[args[i]] = struct{}{}
			count += 1
		}
		store[args[1]] = ent
		return resp.NewInteger(int64(count))
	}
	return resp.NewError("ERR wrong number of arguments for 'SADD' command")
}

func cmdSRem(store map[string]entry, args []string) resp.Value {
	if len(args) >= 3 {
		ent, ok := lookup(store, args[1])
		if !ok {
			return resp.NewInteger(0)
		}
		if ent.Kind != Set {
			return wrongTypeErr
		}
		count := 0
		for idx := 2; idx < len(args); idx++ {
			_, ok = ent.Set[args[idx]]
			if !ok {
				continue
			}
			count += 1
			delete(ent.Set, args[idx])
		}
		if len(ent.Set) == 0 {
			delete(store, args[1])
		}
		return resp.NewInteger(int64(count))
	}
	return resp.NewError("ERR wrong number of arguments for 'SREM' command")
}

func cmdSIsMember(store map[string]entry, args []string) resp.Value {
	if len(args) == 3 {
		ent, ok := lookup(store, args[1])
		if !ok {
			return resp.NewInteger(0)
		}
		if ent.Kind != Set {
			return wrongTypeErr
		}
		_, ok = ent.Set[args[2]]
		if !ok {
			return resp.NewInteger(0)
		}
		return resp.NewInteger(1)
	}
	return resp.NewError("ERR wrong number of arguments for 'SISMEMBER' command")
}

func cmdSMembers(store map[string]entry, args []string) resp.Value {
	if len(args) == 2 {
		ent, ok := lookup(store, args[1])
		if !ok {
			return resp.Value{Kind: resp.Array}
		}
		if ent.Kind != Set {
			return wrongTypeErr
		}
		elems := []resp.Value{}
		for key := range ent.Set {
			elems = append(elems, resp.NewBulkString(key))
		}
		return resp.Value{Kind: resp.Array, Elems: elems}
	}
	return resp.NewError("ERR wrong number of arguments for 'SMEMBERS' command")
}

func cmdSCard(store map[string]entry, args []string) resp.Value {
	if len(args) == 2 {
		ent, ok := lookup(store, args[1])
		if !ok {
			return resp.NewInteger(0)
		}
		if ent.Kind != Set {
			return wrongTypeErr
		}
		return resp.NewInteger(int64(len(ent.Set)))
	}
	return resp.NewError("ERR wrong number of arguments for 'SCARD' command")
}

func cmdZAdd(store map[string]entry, args []string) resp.Value {
	if len(args) >= 4 {
		if len(args) % 2 != 0 {
			return resp.NewError("ERR number of arguments for 'ZADD' command must be even")	
		}
		ent, ok := lookup(store, args[1])
		if !ok {
			ZSetData := ZSetData{members: map[string]float64{}, order: skiplist.New()}
			ent = entry{Kind: ZSet, ZSet: ZSetData}
		}
		if ent.Kind != ZSet {
			return wrongTypeErr
		}
		pairs := (len(args) - 2) / 2
		scores := make([]float64, pairs)
		score_idx := 2
		for i := range pairs {
			score, err := strconv.ParseFloat(args[score_idx], 64)
			if err != nil {
				return resp.NewError("ERR value is not a valid float")
			}
			scores[i] = score
			score_idx += 2
		}
		member_idx := 3
		inserted := 0
		for i := range pairs {
			cur_score, ok := ent.ZSet.members[args[member_idx]]
			if !ok {
				ent.ZSet.members[args[member_idx]] = scores[i]
				ent.ZSet.order.Insert(scores[i], args[member_idx])
				inserted += 1
				member_idx += 2
				continue
			}
			ent.ZSet.members[args[member_idx]] = scores[i]
			ent.ZSet.order.Delete(cur_score, args[member_idx])
			ent.ZSet.order.Insert(scores[i], args[member_idx])
			member_idx += 2
		}
		store[args[1]] = ent
		return resp.NewInteger(int64(inserted))
	}
	return resp.NewError("ERR wrong number of arguments for 'ZADD' command")
}

func cmdZScore(store map[string]entry, args []string) resp.Value {
	if len(args) == 3 {
		ent, ok := lookup(store, args[1])
		if !ok {
			return resp.Value{Kind: resp.Null}
		}
		if ent.Kind != ZSet {
			return wrongTypeErr
		}
		score, ok := ent.ZSet.members[args[2]]
		if !ok {
			return resp.Value{Kind: resp.Null}
		}
		return resp.NewBulkString(strconv.FormatFloat(score, 'g', -1, 64))
	}
	return resp.NewError("ERR wrong number of arguments for 'ZSCORE' command")
}

func cmdZRange(store map[string]entry, args []string) resp.Value {
	if len(args) >= 4 && len(args) < 6 {
		withScores := false
		if len(args) == 5 && strings.ToUpper(args[4]) == "WITHSCORES" {
			withScores = true
		}
		if len(args) == 5 && strings.ToUpper(args[4]) != "WITHSCORES" {
			return resp.NewError("ERR syntax error")
		}
		ent, ok := lookup(store, args[1])
		if !ok {
			return resp.Value{Kind: resp.Array}
		}
		if ent.Kind != ZSet {
			return wrongTypeErr
		}

		start, err := strconv.Atoi(args[2])
		if err != nil {
			return resp.NewError("ERR value is not an integer or out of range")
		}

		stop, err := strconv.Atoi(args[3])
		if err != nil {
			return resp.NewError("ERR value is not an integer or out of range")
		}

		nodes, ok := ent.ZSet.order.GetRange(start, stop)
		if !ok {
			return resp.Value{Kind: resp.Array}
		}
		elems := []resp.Value{}
		for _, node := range nodes {
			elems = append(elems, resp.NewBulkString(node.Member))
			if withScores {
				elems = append(elems, resp.NewBulkString(strconv.FormatFloat(node.Score, 'g', -1, 64)))
			}
		}
		return resp.Value{Kind: resp.Array, Elems: elems}
	}

	return resp.NewError("ERR wrong number of arguments for 'ZRANGE' command")
}

func cmdZRank(store map[string]entry, args []string) resp.Value {
	if len(args) == 3 {
		ent, ok := lookup(store, args[1])
		if !ok {
			return resp.Value{Kind: resp.Null}
		}
		if ent.Kind != ZSet {
			return wrongTypeErr
		}
		score, ok := ent.ZSet.members[args[2]]
		if !ok {
			return resp.Value{Kind: resp.Null}
		}
		rank := ent.ZSet.order.Rank(score, args[2])
		return resp.NewInteger(rank)
	}
	return resp.NewError("ERR wrong number of arguments for 'ZRANK' command")
}

func cmdZRem(store map[string]entry, args []string) resp.Value {
	if len(args) >= 3 {
		ent, ok := lookup(store, args[1])
		if !ok {
			return resp.NewInteger(0)
		}
		if ent.Kind != ZSet {
			return wrongTypeErr
		}
		deleted := 0
		for i := 2; i < len(args); i++ {
			score, ok := ent.ZSet.members[args[i]]
			if !ok {
				continue
			}
			ent.ZSet.order.Delete(score, args[i])
			delete(ent.ZSet.members, args[i])
			deleted += 1
		}
		if len(ent.ZSet.members) == 0 {
			delete(store, args[1])
		}
		return resp.NewInteger(int64(deleted))
	}
	return resp.NewError("ERR wrong number of arguments for 'ZREM' command")
}

