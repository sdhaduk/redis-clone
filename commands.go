package main

import (
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"
)

type Message struct {
	Args  []string
	Reply chan Value
}

type DataType int

const (
	String DataType = iota
	List
	Hash
	Set
)

type entry struct {
	Kind DataType
	Str string
	List []string
	Hash map[string]string
	Set map[string]struct{}
	expiresAt time.Time
}

var wrongTypeErr Value = Value{Kind: Error, Str:"WRONGTYPE Operation against a key holding the wrong kind of value"}

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
	if stop > length - 1 {
		stop = length - 1
	}

	if start > stop || start >= length {
		return 0, 0, false
	}
	return start, stop, true
}

func cmdGet(store map[string]entry, args []string) Value {
	if len(args) != 2 {
		return NewError("ERR wrong number of arguments for 'GET' command")
	}
	ent, ok := lookup(store, args[1])
	if ok {
		if ent.Kind != String {
			return wrongTypeErr
		}
		return NewBulkString(ent.Str)
	}
	return Value{Kind: Null}
}

func cmdSet(store map[string]entry, args []string) Value {
	if len(args) == 3 {
		store[args[1]] = entry{Str: args[2], Kind: String}
		return NewSimpleString("OK")
	}
	
	if len(args) == 5 {
		count, err := strconv.ParseInt(args[4], 10, 64)
		if err != nil {
			return NewError(fmt.Sprintf("ERR %s", err))	
		}
		if count <= 0 {
			return NewError("ERR invalid expire time in 'SET' command")
		}

		if strings.ToUpper(args[3]) == "EX" {
			store[args[1]] = entry{Str: args[2], expiresAt: time.Now().Add(time.Second * time.Duration(count)), Kind: String}
			return NewSimpleString("OK")
		}
		if strings.ToUpper(args[3]) == "PX" {
			store[args[1]] = entry{Str: args[2], expiresAt: time.Now().Add(time.Millisecond * time.Duration(count)), Kind: String}
			return NewSimpleString("OK")
		}

		return NewError(fmt.Sprintf("ERR command '%s' not recognized", args[3]))
	}	

	return NewError("ERR wrong number of arguments for 'SET' command")
}

func cmdExpire(store map[string]entry, args []string) Value {
	if len(args) == 3 {
		count, err := strconv.ParseInt(args[2], 10, 64)
		if err != nil {
			return NewError(fmt.Sprintf("ERR %s", err))	
		}
		if count <= 0 {
			return NewError("ERR invalid expire time in 'EXPIRE' command")
		}
		
		ent, ok := lookup(store, args[1])
		if ok {
			ent.expiresAt = time.Now().Add(time.Second * time.Duration(count)) 
			store[args[1]] = ent
			return NewInteger(1)
		}
		return NewInteger(0)
	}

	return NewError("ERR wrong number of arguments for 'EXPIRE' command")
}

func cmdTTL(store map[string]entry, args []string) Value {
	if len(args) == 2 {
		ent, ok := lookup(store, args[1])
		if !ok {
			return NewInteger(-2)
		}
		if ent.expiresAt.IsZero() {
			return NewInteger(-1)
		}
		return NewInteger(int64(time.Until(ent.expiresAt).Round(time.Second) / time.Second))
	}
	return NewError("ERR wrong number of arguments for 'TTL' command")
}

func cmdDel(store map[string]entry, args []string) Value {
	if len(args) > 1 {
		var count int64 = 0
		for _, key := range args[1:] {
			_, ok := lookup(store, key)
			if ok {
				count += 1
				delete(store, key)
			}
		}
		return NewInteger(count)
	}
	return NewError("ERR wrong number of arguments for 'DEL' command")	
}

func cmdExists(store map[string]entry, args []string) Value {
	if len(args) > 1 {
		var count int64 = 0
		for _, key := range args[1:] {
			_, ok := lookup(store, key)
			if ok {
				count += 1
			}
		}
		return NewInteger(count)
	}
	return NewError("ERR wrong number of arguments for 'EXISTS' command")	
}

func cmdKeys(store map[string]entry, args []string) Value {
	if len(args) == 2 {
		elems := []Value{}
		for key := range store {
			matched, err := path.Match(args[1], key)
			if err != nil {
				return NewError(fmt.Sprintf("ERR %s", err))
			}
			if matched {
				_, ok := lookup(store, key)
				if ok {
					elems = append(elems, NewBulkString(key))
				}
			}
		}
		return Value{Kind: Array, Elems: elems}
	}
	return NewError("ERR wrong number of arguments for 'KEYS' command")
}

func cmdIncr(store map[string]entry, args []string)	Value {
	if len(args) == 2 {
		ent, ok := lookup(store, args[1])
		if !ok {
			store[args[1]] = entry{Str: "1", Kind: String}
			return NewInteger(1)
		}
		if ent.Kind != String {
			return wrongTypeErr
		}
		num, err := strconv.ParseInt(ent.Str, 10, 64)
		if err != nil {
			return NewError("ERR value is not an integer or out of range")
		}
		store[args[1]] = entry{Str: strconv.FormatInt(num + 1, 10), expiresAt: ent.expiresAt, Kind: String}
		return NewInteger(num + 1)
	}
	return NewError("ERR wrong number of arguments for 'INCR' command")
}

func cmdDecr(store map[string]entry, args []string)	Value {
	if len(args) == 2 {
		ent, ok := lookup(store, args[1])
		if !ok {
			store[args[1]] = entry{Str: "-1", Kind: String}
			return NewInteger(-1)
		}
		if ent.Kind != String {
			return wrongTypeErr
		}	
		num, err := strconv.ParseInt(ent.Str, 10, 64)
		if err != nil {
			return NewError("ERR value is not an integer or out of range")
		}
		store[args[1]] = entry{Str: strconv.FormatInt(num - 1, 10), expiresAt: ent.expiresAt, Kind: String}
		return NewInteger(num - 1)
	}
	return NewError("ERR wrong number of arguments for 'DECR' command")
}

func cmdLPush(store map[string]entry, args []string) Value {
	if len(args) >= 3 {
		ent, ok := lookup(store, args[1])
		if ok && ent.Kind != List {
			return wrongTypeErr
		}
		new_list := make([]string, len(args) - 2)
		args_counter := 2
		for i := len(new_list) - 1; i >= 0; i-- {
			new_list[i] = args[args_counter]
			args_counter += 1
		}
		if !ok {
			store[args[1]] = entry{Kind: List, List: new_list}
			return NewInteger(int64(len(new_list)))
		}
		new_list = append(new_list, ent.List...)
		store[args[1]] = entry{Kind: List, List: new_list, expiresAt: ent.expiresAt}
		return NewInteger(int64(len(new_list)))
	}
	return NewError("ERR wrong number of arguments for 'LPUSH' command")	
}

func cmdRPush(store map[string]entry, args []string) Value {
	if len(args) >= 3 {
		ent, ok := lookup(store, args[1])
		if ok && ent.Kind != List {
			return wrongTypeErr
		}
		new_list := make([]string, len(args) - 2)
		args_counter := 2
		for i := 0; i < len(args) - 2; i++ {
			new_list[i] = args[args_counter]
			args_counter += 1
		}
		if !ok {
			store[args[1]] = entry{Kind: List, List: new_list}
			return NewInteger(int64(len(new_list)))
		}
		new_list = append(ent.List, new_list...)
		store[args[1]] = entry{Kind: List, List: new_list, expiresAt: ent.expiresAt}
		return NewInteger(int64(len(new_list)))
	}
	return NewError("ERR wrong number of arguments for 'RPUSH' command")	
}	

func cmdLLen(store map[string]entry, args []string) Value {
	if len(args) == 2 {
		ent, ok := lookup(store, args[1])
		if !ok {
			return NewInteger(0)
		}
		if ent.Kind != List {
			return wrongTypeErr
		}
		return NewInteger(int64(len(ent.List)))
	}
	return NewError("ERR wrong number of arguments for 'LLEN' command")	
}

func cmdLRange(store map[string]entry, args []string) Value {
	if len(args) == 4 {
		start, err := strconv.Atoi(args[2])
		if err != nil {
			return NewError(fmt.Sprintf("ERR %s", err))
		}
		stop, err := strconv.Atoi(args[3])
		if err != nil {
			return NewError(fmt.Sprintf("ERR %s", err))
		}
		ent, ok := lookup(store, args[1])
		if !ok {
			return Value{Kind: Array}
		}
		if ent.Kind != List {
			return wrongTypeErr
		}
		start, stop, ok = normalizeRange(start, stop, len(ent.List))
		if !ok {
			return Value{Kind: Array}
		}
		
		elems := []Value{}
		for i := start; i <= stop; i++ {
			item := Value{Kind: BulkString, Str: ent.List[i]}
			elems = append(elems, item)
		}
		return Value{Kind: Array, Elems: elems}
	}
	return NewError("ERR wrong number of arguments for 'LRANGE' command")		
}

func cmdRPop(store map[string]entry, args []string) Value {
	if len(args) == 2 {
		ent, ok := lookup(store, args[1])
		if !ok {
			return Value{Kind: Null}
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
		return NewBulkString(popped)
	}

	if len(args) == 3 {
		ent, ok := lookup(store, args[1])
		if !ok {
			return Value{Kind: Null}
		}
		if ent.Kind != List {
			return wrongTypeErr
		}
		count, err := strconv.Atoi(args[2])
		if err != nil {
			return NewError(fmt.Sprintf("ERR %s", err))
		}
		if count <= 0 {
			return NewError("ERR value is out of range, must be positive")
		}
		if count > len(ent.List) {
			count = len(ent.List)
		}
		items := []Value{}
		for i := 0; i < count; i++ {
			items = append(items, Value{Kind: BulkString, Str: ent.List[len(ent.List)-1-i]})
		}
		updated_list := ent.List[:len(ent.List)-count]
		if len(updated_list) == 0 {
			delete(store, args[1])
		} else {
			ent.List = updated_list
			store[args[1]] = ent
		}
		return Value{Kind: Array, Elems: items}	
	}
	return NewError("ERR wrong number of arguments for 'RPOP' command")
}