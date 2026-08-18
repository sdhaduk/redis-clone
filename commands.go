package main

import (
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"
)

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

func cmdGet(store map[string]entry, args []string) Value {
	if len(args) != 2 {
		return NewError("ERR wrong number of arguments for 'GET' command")
	}
	ent, ok := lookup(store, args[1])
	if ok {
		return NewBulkString(ent.val)
	}
	return Value{Kind: Null}
}

func cmdSet(store map[string]entry, args []string) Value {
	if len(args) == 3 {
		store[args[1]] = entry{val: args[2]}
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
			store[args[1]] = entry{val: args[2], expiresAt: time.Now().Add(time.Second * time.Duration(count))}
			return NewSimpleString("OK")
		}
		if strings.ToUpper(args[3]) == "PX" {
			store[args[1]] = entry{val: args[2], expiresAt: time.Now().Add(time.Millisecond * time.Duration(count))}
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
				ent, ok := lookup(store, key)
				if ok {
					elems = append(elems, NewBulkString(ent.val))
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
			store[args[1]] = entry{val: "1"}
			return NewInteger(1)
		}
		num, err := strconv.ParseInt(ent.val, 10, 64)
		if err != nil {
			return NewError("ERR value is not an integer or out of range")
		}
		store[args[1]] = entry{val: strconv.FormatInt(num + 1, 10), expiresAt: ent.expiresAt}
		return NewInteger(num + 1)
	}
	return NewError("ERR wrong number of arguments for 'INCR' command")
}

func cmdDecr(store map[string]entry, args []string)	Value {
	if len(args) == 2 {
		ent, ok := lookup(store, args[1])
		if !ok {
			store[args[1]] = entry{val: "-1"}
			return NewInteger(-1)
		}
		num, err := strconv.ParseInt(ent.val, 10, 64)
		if err != nil {
			return NewError("ERR value is not an integer or out of range")
		}
		store[args[1]] = entry{val: strconv.FormatInt(num - 1, 10), expiresAt: ent.expiresAt}
		return NewInteger(num - 1)
	}
	return NewError("ERR wrong number of arguments for 'DECR' command")
}

