package store

import (
	"redis-clone/resp"
	"redis-clone/skiplist"

	"sort"
	"testing"
	"time"
)

// ---------- helpers ----------

func assertInteger(t *testing.T, got resp.Value, want int64) {
	t.Helper()
	if got.Kind != resp.Integer || got.Num != want {
		t.Errorf("got %+v, want Integer %d", got, want)
	}
}

func assertBulkString(t *testing.T, got resp.Value, want string) {
	t.Helper()
	if got.Kind != resp.BulkString || got.Str != want {
		t.Errorf("got %+v, want BulkString %q", got, want)
	}
}

func assertKind(t *testing.T, got resp.Value, want resp.Kind) {
	t.Helper()
	if got.Kind != want {
		t.Errorf("got %+v, want Kind %v", got, want)
	}
}

// expired returns an entry whose deadline is already in the past.
func expired(val string) entry {
	return entry{Str: val, Kind: String, expiresAt: time.Now().Add(-time.Second)}
}

// ---------- lookup: the lazy-expiration primitive ----------

func TestLookup(t *testing.T) {
	store := map[string]entry{
		"live":    {Str: "a", Kind: String},
		"ttl":     {Str: "b", Kind: String, expiresAt: time.Now().Add(time.Minute)},
		"expired": expired("c"),
	}

	if ent, ok := lookup(store, "live"); !ok || ent.Str != "a" {
		t.Errorf("live key: got (%+v, %v), want (val \"a\", true)", ent, ok)
	}
	if ent, ok := lookup(store, "ttl"); !ok || ent.Str != "b" {
		t.Errorf("unexpired ttl key: got (%+v, %v), want (val \"b\", true)", ent, ok)
	}
	if _, ok := lookup(store, "missing"); ok {
		t.Error("missing key: lookup reported found")
	}
	if _, ok := lookup(store, "expired"); ok {
		t.Error("expired key: lookup reported found")
	}
	// lazy deletion: the expired key must actually be gone from the map
	if _, still := store["expired"]; still {
		t.Error("expired key was not deleted from the store by lookup")
	}
}

// ---------- SET / GET ----------

func TestCmdSetGet(t *testing.T) {
	store := make(map[string]entry)

	assertKind(t, cmdSet(store, []string{"SET", "name", "sagar"}), resp.SimpleString)
	assertBulkString(t, cmdGet(store, []string{"GET", "name"}), "sagar")

	// overwrite
	cmdSet(store, []string{"SET", "name", "bob"})
	assertBulkString(t, cmdGet(store, []string{"GET", "name"}), "bob")

	// missing key -> Null
	assertKind(t, cmdGet(store, []string{"GET", "missing"}), resp.Null)

	// wrong arg counts -> Error, never a panic
	assertKind(t, cmdGet(store, []string{"GET"}), resp.Error)
	assertKind(t, cmdGet(store, []string{"GET", "a", "b"}), resp.Error)
	assertKind(t, cmdSet(store, []string{"SET", "k"}), resp.Error)
	assertKind(t, cmdSet(store, []string{"SET", "k", "v", "EX"}), resp.Error)
}

func TestCmdSetExpiryOptions(t *testing.T) {
	store := make(map[string]entry)

	// EX: key exists now, TTL is positive
	assertKind(t, cmdSet(store, []string{"SET", "k", "v", "EX", "100"}), resp.SimpleString)
	assertBulkString(t, cmdGet(store, []string{"GET", "k"}), "v")
	assertInteger(t, cmdTTL(store, []string{"TTL", "k"}), 100)

	// EX/PX are case-insensitive like all of Redis
	assertKind(t, cmdSet(store, []string{"SET", "k2", "v", "ex", "100"}), resp.SimpleString)
	assertInteger(t, cmdTTL(store, []string{"TTL", "k2"}), 100)

	// PX: value readable immediately, gone after the deadline passes
	assertKind(t, cmdSet(store, []string{"SET", "p", "v", "PX", "50"}), resp.SimpleString)
	assertBulkString(t, cmdGet(store, []string{"GET", "p"}), "v")
	time.Sleep(60 * time.Millisecond)
	assertKind(t, cmdGet(store, []string{"GET", "p"}), resp.Null)

	// plain SET on a key that had a TTL clears the TTL
	cmdSet(store, []string{"SET", "s", "v", "EX", "100"})
	cmdSet(store, []string{"SET", "s", "v2"})
	assertInteger(t, cmdTTL(store, []string{"TTL", "s"}), -1)

	// bad expire arguments -> Error
	assertKind(t, cmdSet(store, []string{"SET", "k", "v", "EX", "0"}), resp.Error)
	assertKind(t, cmdSet(store, []string{"SET", "k", "v", "EX", "-5"}), resp.Error)
	assertKind(t, cmdSet(store, []string{"SET", "k", "v", "EX", "abc"}), resp.Error)
	assertKind(t, cmdSet(store, []string{"SET", "k", "v", "XX", "10"}), resp.Error)
}

// ---------- EXPIRE / TTL ----------

func TestCmdExpireAndTTL(t *testing.T) {
	store := make(map[string]entry)

	// TTL's three outcomes
	assertInteger(t, cmdTTL(store, []string{"TTL", "missing"}), -2)

	cmdSet(store, []string{"SET", "plain", "v"})
	assertInteger(t, cmdTTL(store, []string{"TTL", "plain"}), -1)

	cmdSet(store, []string{"SET", "timed", "v", "EX", "5"})
	assertInteger(t, cmdTTL(store, []string{"TTL", "timed"}), 5)

	// EXPIRE on an existing key -> 1, and the TTL takes effect
	assertInteger(t, cmdExpire(store, []string{"EXPIRE", "plain", "10"}), 1)
	assertInteger(t, cmdTTL(store, []string{"TTL", "plain"}), 10)

	// EXPIRE on a missing key -> 0
	assertInteger(t, cmdExpire(store, []string{"EXPIRE", "missing", "10"}), 0)

	// a key that expired behaves exactly like one that never existed
	store["gone"] = expired("v")
	assertInteger(t, cmdTTL(store, []string{"TTL", "gone"}), -2)
	assertInteger(t, cmdExpire(store, []string{"EXPIRE", "gone", "10"}), 0)

	// bad arguments -> Error
	assertKind(t, cmdExpire(store, []string{"EXPIRE", "plain", "abc"}), resp.Error)
	assertKind(t, cmdExpire(store, []string{"EXPIRE", "plain"}), resp.Error)
	assertKind(t, cmdTTL(store, []string{"TTL"}), resp.Error)
}

// ---------- DEL / EXISTS ----------

func TestCmdDelExists(t *testing.T) {
	store := make(map[string]entry)
	cmdSet(store, []string{"SET", "a", "1"})
	cmdSet(store, []string{"SET", "b", "2"})

	// EXISTS counts each named key, including repeats, like real Redis
	assertInteger(t, cmdExists(store, []string{"EXISTS", "a"}), 1)
	assertInteger(t, cmdExists(store, []string{"EXISTS", "a", "b", "missing"}), 2)
	assertInteger(t, cmdExists(store, []string{"EXISTS", "a", "a"}), 2)

	// expired keys don't exist
	store["ghost"] = expired("v")
	assertInteger(t, cmdExists(store, []string{"EXISTS", "ghost"}), 0)

	// DEL returns how many it actually removed, and removes them
	assertInteger(t, cmdDel(store, []string{"DEL", "a", "missing", "b"}), 2)
	assertKind(t, cmdGet(store, []string{"GET", "a"}), resp.Null)
	assertKind(t, cmdGet(store, []string{"GET", "b"}), resp.Null)

	// wrong arg counts -> Error
	assertKind(t, cmdDel(store, []string{"DEL"}), resp.Error)
	assertKind(t, cmdExists(store, []string{"EXISTS"}), resp.Error)
}

// ---------- KEYS ----------

// keysOf unpacks a KEYS reply into a sorted []string of key names.
func keysOf(t *testing.T, v resp.Value) []string {
	t.Helper()
	if v.Kind != resp.Array {
		t.Fatalf("KEYS reply: got %+v, want an Array", v)
	}
	names := []string{}
	for _, e := range v.Elems {
		if e.Kind != resp.BulkString {
			t.Fatalf("KEYS element: got %+v, want a BulkString", e)
		}
		names = append(names, e.Str)
	}
	sort.Strings(names)
	return names
}

func TestCmdKeys(t *testing.T) {
	store := make(map[string]entry)
	cmdSet(store, []string{"SET", "name", "sagar"})
	cmdSet(store, []string{"SET", "age", "28"})
	cmdSet(store, []string{"SET", "session:1", "tok"})

	// * matches everything; the reply holds the KEY NAMES
	got := keysOf(t, cmdKeys(store, []string{"KEYS", "*"}))
	want := []string{"age", "name", "session:1"}
	if len(got) != len(want) {
		t.Fatalf("KEYS * returned %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("KEYS * returned %v, want %v", got, want)
		}
	}

	// glob pattern
	got = keysOf(t, cmdKeys(store, []string{"KEYS", "session:*"}))
	if len(got) != 1 || got[0] != "session:1" {
		t.Errorf("KEYS session:* returned %v, want [session:1]", got)
	}

	// no match -> empty Array, NOT Null
	v := cmdKeys(store, []string{"KEYS", "zzz*"})
	if v.Kind != resp.Array || len(v.Elems) != 0 {
		t.Errorf("KEYS zzz* returned %+v, want empty Array", v)
	}

	// expired keys never appear, even before any sweep runs
	store["ghost"] = expired("v")
	got = keysOf(t, cmdKeys(store, []string{"KEYS", "*"}))
	for _, k := range got {
		if k == "ghost" {
			t.Error("KEYS * listed an expired key")
		}
	}
}

// ---------- INCR / DECR ----------

func TestCmdIncrDecr(t *testing.T) {
	store := make(map[string]entry)

	// missing key counts as 0
	assertInteger(t, cmdIncr(store, []string{"INCR", "n"}), 1)
	assertInteger(t, cmdIncr(store, []string{"INCR", "n"}), 2)
	assertInteger(t, cmdDecr(store, []string{"DECR", "n"}), 1)
	assertInteger(t, cmdDecr(store, []string{"DECR", "d"}), -1)

	// the stored value stays a string
	assertBulkString(t, cmdGet(store, []string{"GET", "n"}), "1")

	// works on values written by SET
	cmdSet(store, []string{"SET", "hits", "41"})
	assertInteger(t, cmdIncr(store, []string{"INCR", "hits"}), 42)

	// non-integer value -> the canonical error, not a panic
	cmdSet(store, []string{"SET", "s", "abc"})
	v := cmdIncr(store, []string{"INCR", "s"})
	if v.Kind != resp.Error || v.Str != "ERR value is not an integer or out of range" {
		t.Errorf("INCR on non-integer: got %+v, want the canonical ERR message", v)
	}

	// INCR must preserve an existing TTL
	cmdSet(store, []string{"SET", "c", "1", "EX", "100"})
	cmdIncr(store, []string{"INCR", "c"})
	assertInteger(t, cmdTTL(store, []string{"TTL", "c"}), 100)

	// wrong arg counts -> Error
	assertKind(t, cmdIncr(store, []string{"INCR"}), resp.Error)
	assertKind(t, cmdDecr(store, []string{"DECR", "a", "b"}), resp.Error)
}

// ---------- sweep ----------

func TestSweep(t *testing.T) {
	store := map[string]entry{
		"live":  {Str: "a", Kind: String},
		"ttl":   {Str: "b", Kind: String, expiresAt: time.Now().Add(time.Minute)},
		"dead1": expired("x"),
		"dead2": expired("y"),
	}

	sweep(store)

	if _, ok := store["dead1"]; ok {
		t.Error("sweep left an expired key behind")
	}
	if _, ok := store["dead2"]; ok {
		t.Error("sweep left an expired key behind")
	}
	if _, ok := store["live"]; !ok {
		t.Error("sweep deleted a key with no expiry")
	}
	if _, ok := store["ttl"]; !ok {
		t.Error("sweep deleted a key whose deadline has not passed")
	}
}

// ---------- helpers for the data-structure commands ----------

func assertErrorMsg(t *testing.T, got resp.Value, want string) {
	t.Helper()
	if got.Kind != resp.Error || got.Str != want {
		t.Errorf("got %+v, want Error %q", got, want)
	}
}

// assertArray asserts an Array of BulkStrings equal to want, in order.
func assertArray(t *testing.T, got resp.Value, want []string) {
	t.Helper()
	if got.Kind != resp.Array {
		t.Errorf("got %+v, want Array %v", got, want)
		return
	}
	if len(got.Elems) != len(want) {
		t.Errorf("got %d elements %+v, want %v", len(got.Elems), got.Elems, want)
		return
	}
	for i, e := range got.Elems {
		if e.Kind != resp.BulkString || e.Str != want[i] {
			t.Errorf("element %d: got %+v, want BulkString %q", i, e, want[i])
		}
	}
}

// assertArrayUnordered is assertArray for replies whose order is undefined
// (SMEMBERS iterates a map).
func assertArrayUnordered(t *testing.T, got resp.Value, want []string) {
	t.Helper()
	if got.Kind != resp.Array {
		t.Errorf("got %+v, want Array %v", got, want)
		return
	}
	names := []string{}
	for _, e := range got.Elems {
		names = append(names, e.Str)
	}
	sort.Strings(names)
	sorted := append([]string{}, want...)
	sort.Strings(sorted)
	if len(names) != len(sorted) {
		t.Errorf("got %v, want %v (any order)", names, sorted)
		return
	}
	for i := range sorted {
		if names[i] != sorted[i] {
			t.Errorf("got %v, want %v (any order)", names, sorted)
			return
		}
	}
}

// assertHashReply asserts an HGETALL-style flat field/value Array, ignoring
// pair order (Hash iterates a map).
func assertHashReply(t *testing.T, got resp.Value, want map[string]string) {
	t.Helper()
	if got.Kind != resp.Array {
		t.Errorf("got %+v, want Array of field/value pairs %v", got, want)
		return
	}
	if len(got.Elems)%2 != 0 {
		t.Errorf("got odd number of elements %+v, want field/value pairs", got.Elems)
		return
	}
	pairs := map[string]string{}
	for i := 0; i < len(got.Elems); i += 2 {
		pairs[got.Elems[i].Str] = got.Elems[i+1].Str
	}
	if len(pairs) != len(want) {
		t.Errorf("got pairs %v, want %v", pairs, want)
		return
	}
	for k, v := range want {
		if pairs[k] != v {
			t.Errorf("got pairs %v, want %v", pairs, want)
			return
		}
	}
}

type zpair struct {
	member string
	score  float64
}

// zsetEntry builds a ZSet entry directly, so ZSCORE/ZRANGE/ZRANK/ZREM can be
// tested without going through cmdZAdd.
func zsetEntry(pairs ...zpair) entry {
	data := ZSetData{members: map[string]float64{}, order: skiplist.New()}
	for _, p := range pairs {
		data.members[p.member] = p.score
		data.order.Insert(p.score, p.member)
	}
	return entry{Kind: ZSet, ZSet: data}
}

// ---------- normalizeRange ----------

func TestNormalizeRange(t *testing.T) {
	cases := []struct {
		name                string
		start, stop, length int
		wantStart, wantStop int
		wantOK              bool
	}{
		{"in bounds", 1, 3, 5, 1, 3, true},
		{"full range via -1", 0, -1, 5, 0, 4, true},
		{"stop past end clamps", 0, 99, 5, 0, 4, true},
		{"negative start", -2, 4, 5, 3, 4, true},
		{"both negative", -3, -1, 5, 2, 4, true},
		{"start below -length clamps to 0", -99, 2, 5, 0, 2, true},
		{"single element", 2, 2, 5, 2, 2, true},
		{"crossed", 3, 1, 5, 0, 0, false},
		{"crossed after normalizing", -1, -3, 5, 0, 0, false},
		{"start past end", 5, 9, 5, 0, 0, false},
		{"empty length", 0, -1, 0, 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, stop, ok := normalizeRange(tc.start, tc.stop, tc.length)
			if start != tc.wantStart || stop != tc.wantStop || ok != tc.wantOK {
				t.Errorf("normalizeRange(%d, %d, %d) = (%d, %d, %v), want (%d, %d, %v)",
					tc.start, tc.stop, tc.length, start, stop, ok, tc.wantStart, tc.wantStop, tc.wantOK)
			}
		})
	}
}

// ---------- WRONGTYPE in both directions ----------

func TestWrongType(t *testing.T) {
	store := make(map[string]entry)
	cmdSet(store, []string{"SET", "str", "v"})
	cmdRPush(store, []string{"RPUSH", "list", "a"})
	cmdHSet(store, []string{"HSET", "hash", "f", "v"})
	cmdSAdd(store, []string{"SADD", "set", "m"})
	store["zset"] = zsetEntry(zpair{"m", 1})

	cases := []struct {
		name string
		fn   func(map[string]entry, []string) resp.Value
		args []string
	}{
		// every non-string command against a String entry
		{"LPUSH on string", cmdLPush, []string{"LPUSH", "str", "x"}},
		{"RPUSH on string", cmdRPush, []string{"RPUSH", "str", "x"}},
		{"LLEN on string", cmdLLen, []string{"LLEN", "str"}},
		{"LRANGE on string", cmdLRange, []string{"LRANGE", "str", "0", "-1"}},
		{"LPOP on string", cmdLPop, []string{"LPOP", "str"}},
		{"RPOP on string", cmdRPop, []string{"RPOP", "str"}},
		{"HSET on string", cmdHSet, []string{"HSET", "str", "f", "v"}},
		{"HGET on string", cmdHGet, []string{"HGET", "str", "f"}},
		{"HDEL on string", cmdHDel, []string{"HDEL", "str", "f"}},
		{"HGETALL on string", cmdHGetAll, []string{"HGETALL", "str"}},
		{"SADD on string", cmdSAdd, []string{"SADD", "str", "m"}},
		{"SREM on string", cmdSRem, []string{"SREM", "str", "m"}},
		{"SISMEMBER on string", cmdSIsMember, []string{"SISMEMBER", "str", "m"}},
		{"SMEMBERS on string", cmdSMembers, []string{"SMEMBERS", "str"}},
		{"SCARD on string", cmdSCard, []string{"SCARD", "str"}},
		{"ZADD on string", cmdZAdd, []string{"ZADD", "str", "1", "m"}},
		{"ZSCORE on string", cmdZScore, []string{"ZSCORE", "str", "m"}},
		{"ZRANGE on string", cmdZRange, []string{"ZRANGE", "str", "0", "-1", "WITHSCORES"}},
		{"ZRANK on string", cmdZRank, []string{"ZRANK", "str", "m"}},
		{"ZREM on string", cmdZRem, []string{"ZREM", "str", "m"}},
		// and string commands against every other entry kind
		{"GET on list", cmdGet, []string{"GET", "list"}},
		{"INCR on list", cmdIncr, []string{"INCR", "list"}},
		{"DECR on list", cmdDecr, []string{"DECR", "list"}},
		{"GET on hash", cmdGet, []string{"GET", "hash"}},
		{"GET on set", cmdGet, []string{"GET", "set"}},
		{"GET on zset", cmdGet, []string{"GET", "zset"}},
		// and across the non-string types
		{"LPUSH on hash", cmdLPush, []string{"LPUSH", "hash", "x"}},
		{"HSET on set", cmdHSet, []string{"HSET", "set", "f", "v"}},
		{"SADD on zset", cmdSAdd, []string{"SADD", "zset", "m"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertErrorMsg(t, tc.fn(store, tc.args), wrongTypeErr.Str)
		})
	}

	// none of the rejected calls may have clobbered the existing entries
	assertBulkString(t, cmdGet(store, []string{"GET", "str"}), "v")
	assertArray(t, cmdLRange(store, []string{"LRANGE", "list", "0", "-1"}), []string{"a"})
	assertBulkString(t, cmdHGet(store, []string{"HGET", "hash", "f"}), "v")
	assertInteger(t, cmdSIsMember(store, []string{"SISMEMBER", "set", "m"}), 1)
}

// ---------- LPUSH / RPUSH ----------

func TestCmdLPushRPush(t *testing.T) {
	store := make(map[string]entry)

	// LPUSH pushes each value at the head, so the last argument ends up first
	assertInteger(t, cmdLPush(store, []string{"LPUSH", "l", "a", "b", "c"}), 3)
	assertArray(t, cmdLRange(store, []string{"LRANGE", "l", "0", "-1"}), []string{"c", "b", "a"})

	// pushing onto an existing list returns the NEW total length
	assertInteger(t, cmdLPush(store, []string{"LPUSH", "l", "x"}), 4)
	assertArray(t, cmdLRange(store, []string{"LRANGE", "l", "0", "-1"}), []string{"x", "c", "b", "a"})

	// RPUSH appends in argument order
	assertInteger(t, cmdRPush(store, []string{"RPUSH", "r", "a", "b", "c"}), 3)
	assertArray(t, cmdLRange(store, []string{"LRANGE", "r", "0", "-1"}), []string{"a", "b", "c"})
	assertInteger(t, cmdRPush(store, []string{"RPUSH", "r", "d"}), 4)
	assertArray(t, cmdLRange(store, []string{"LRANGE", "r", "0", "-1"}), []string{"a", "b", "c", "d"})

	// pushes must preserve an existing TTL
	cmdExpire(store, []string{"EXPIRE", "l", "100"})
	cmdLPush(store, []string{"LPUSH", "l", "y"})
	assertInteger(t, cmdTTL(store, []string{"TTL", "l"}), 100)
	cmdExpire(store, []string{"EXPIRE", "r", "100"})
	cmdRPush(store, []string{"RPUSH", "r", "y"})
	assertInteger(t, cmdTTL(store, []string{"TTL", "r"}), 100)

	// wrong arg counts -> Error
	assertKind(t, cmdLPush(store, []string{"LPUSH"}), resp.Error)
	assertKind(t, cmdLPush(store, []string{"LPUSH", "l"}), resp.Error)
	assertKind(t, cmdRPush(store, []string{"RPUSH"}), resp.Error)
	assertKind(t, cmdRPush(store, []string{"RPUSH", "r"}), resp.Error)
}

// ---------- LLEN ----------

func TestCmdLLen(t *testing.T) {
	store := make(map[string]entry)

	// missing key counts as an empty list
	assertInteger(t, cmdLLen(store, []string{"LLEN", "missing"}), 0)

	cmdRPush(store, []string{"RPUSH", "l", "a", "b", "c"})
	assertInteger(t, cmdLLen(store, []string{"LLEN", "l"}), 3)

	// wrong arg counts -> Error
	assertKind(t, cmdLLen(store, []string{"LLEN"}), resp.Error)
	assertKind(t, cmdLLen(store, []string{"LLEN", "l", "x"}), resp.Error)
}

// ---------- LRANGE ----------

func TestCmdLRange(t *testing.T) {
	store := make(map[string]entry)
	cmdRPush(store, []string{"RPUSH", "l", "a", "b", "c", "d", "e"})

	assertArray(t, cmdLRange(store, []string{"LRANGE", "l", "0", "-1"}), []string{"a", "b", "c", "d", "e"})
	assertArray(t, cmdLRange(store, []string{"LRANGE", "l", "1", "3"}), []string{"b", "c", "d"})
	assertArray(t, cmdLRange(store, []string{"LRANGE", "l", "-2", "-1"}), []string{"d", "e"})

	// stop past the end clamps
	assertArray(t, cmdLRange(store, []string{"LRANGE", "l", "0", "99"}), []string{"a", "b", "c", "d", "e"})

	// crossed or fully out-of-range indexes -> empty Array, NOT Null
	assertArray(t, cmdLRange(store, []string{"LRANGE", "l", "3", "1"}), []string{})
	assertArray(t, cmdLRange(store, []string{"LRANGE", "l", "99", "100"}), []string{})

	// missing key -> empty Array
	assertArray(t, cmdLRange(store, []string{"LRANGE", "missing", "0", "-1"}), []string{})

	// non-integer index -> Error
	assertKind(t, cmdLRange(store, []string{"LRANGE", "l", "abc", "-1"}), resp.Error)
	assertKind(t, cmdLRange(store, []string{"LRANGE", "l", "0", "abc"}), resp.Error)

	// wrong arg counts -> Error
	assertKind(t, cmdLRange(store, []string{"LRANGE", "l", "0"}), resp.Error)
	assertKind(t, cmdLRange(store, []string{"LRANGE", "l", "0", "-1", "x"}), resp.Error)
}

// ---------- LPOP / RPOP ----------

func TestCmdLPopRPop(t *testing.T) {
	store := make(map[string]entry)
	cmdRPush(store, []string{"RPUSH", "l", "a", "b", "c", "d", "e"})

	// single pop: LPOP takes the head, RPOP the tail
	assertBulkString(t, cmdLPop(store, []string{"LPOP", "l"}), "a")
	assertBulkString(t, cmdRPop(store, []string{"RPOP", "l"}), "e")
	assertArray(t, cmdLRange(store, []string{"LRANGE", "l", "0", "-1"}), []string{"b", "c", "d"})

	// LPOP with count returns the first n in list order
	assertArray(t, cmdLPop(store, []string{"LPOP", "l", "2"}), []string{"b", "c"})

	// count larger than the list clamps, and emptying the list deletes the key
	assertArray(t, cmdRPop(store, []string{"RPOP", "l", "5"}), []string{"d"})
	assertInteger(t, cmdExists(store, []string{"EXISTS", "l"}), 0)

	// RPOP with count returns items in pop order: tail first
	cmdRPush(store, []string{"RPUSH", "r", "a", "b", "c"})
	assertArray(t, cmdRPop(store, []string{"RPOP", "r", "2"}), []string{"c", "b"})
	assertArray(t, cmdLRange(store, []string{"LRANGE", "r", "0", "-1"}), []string{"a"})

	// a single pop of the last element also deletes the key
	assertBulkString(t, cmdLPop(store, []string{"LPOP", "r"}), "a")
	assertInteger(t, cmdExists(store, []string{"EXISTS", "r"}), 0)

	// missing key -> Null in both forms
	assertKind(t, cmdLPop(store, []string{"LPOP", "missing"}), resp.Null)
	assertKind(t, cmdRPop(store, []string{"RPOP", "missing"}), resp.Null)
	assertKind(t, cmdLPop(store, []string{"LPOP", "missing", "2"}), resp.Null)

	// zero, negative, or non-integer counts -> Error
	cmdRPush(store, []string{"RPUSH", "b", "x", "y"})
	assertKind(t, cmdLPop(store, []string{"LPOP", "b", "0"}), resp.Error)
	assertKind(t, cmdRPop(store, []string{"RPOP", "b", "-1"}), resp.Error)
	assertKind(t, cmdLPop(store, []string{"LPOP", "b", "abc"}), resp.Error)

	// wrong arg counts -> Error
	assertKind(t, cmdLPop(store, []string{"LPOP"}), resp.Error)
	assertKind(t, cmdRPop(store, []string{"RPOP", "b", "1", "x"}), resp.Error)
}

// ---------- HSET / HGET ----------

func TestHSet(t *testing.T) {
	store := make(map[string]entry)

	// counts only newly created fields
	assertInteger(t, cmdHSet(store, []string{"HSET", "h", "f1", "v1", "f2", "v2"}), 2)
	assertInteger(t, cmdHSet(store, []string{"HSET", "h", "f1", "changed"}), 0)
	assertBulkString(t, cmdHGet(store, []string{"HGET", "h", "f1"}), "changed")

	// mixed new and overwrite in one call
	assertInteger(t, cmdHSet(store, []string{"HSET", "h", "f1", "again", "f3", "v3"}), 1)
	assertBulkString(t, cmdHGet(store, []string{"HGET", "h", "f3"}), "v3")

	// same field twice in one call: created once, last value wins
	assertInteger(t, cmdHSet(store, []string{"HSET", "h2", "f", "a", "f", "b"}), 1)
	assertBulkString(t, cmdHGet(store, []string{"HGET", "h2", "f"}), "b")

	// HSET must preserve an existing TTL
	cmdExpire(store, []string{"EXPIRE", "h", "100"})
	cmdHSet(store, []string{"HSET", "h", "f4", "v4"})
	assertInteger(t, cmdTTL(store, []string{"TTL", "h"}), 100)

	// dangling field name or too few args -> Error
	assertKind(t, cmdHSet(store, []string{"HSET", "h", "f1", "v1", "f2"}), resp.Error)
	assertKind(t, cmdHSet(store, []string{"HSET", "h", "f1"}), resp.Error)
	assertKind(t, cmdHSet(store, []string{"HSET", "h"}), resp.Error)
}

func TestHGet(t *testing.T) {
	store := make(map[string]entry)
	cmdHSet(store, []string{"HSET", "h", "f", "v"})

	assertBulkString(t, cmdHGet(store, []string{"HGET", "h", "f"}), "v")

	// missing field and missing key both -> Null
	assertKind(t, cmdHGet(store, []string{"HGET", "h", "missing"}), resp.Null)
	assertKind(t, cmdHGet(store, []string{"HGET", "missing", "f"}), resp.Null)

	// wrong arg counts -> Error
	assertKind(t, cmdHGet(store, []string{"HGET", "h"}), resp.Error)
	assertKind(t, cmdHGet(store, []string{"HGET", "h", "f", "x"}), resp.Error)
}

// ---------- HDEL / HGETALL ----------

func TestHDel(t *testing.T) {
	store := make(map[string]entry)
	cmdHSet(store, []string{"HSET", "h", "f1", "v1", "f2", "v2", "f3", "v3"})

	// counts only fields that existed
	assertInteger(t, cmdHDel(store, []string{"HDEL", "h", "f1", "missing"}), 1)
	assertKind(t, cmdHGet(store, []string{"HGET", "h", "f1"}), resp.Null)

	// deleting the last field removes the key entirely
	assertInteger(t, cmdHDel(store, []string{"HDEL", "h", "f2", "f3"}), 2)
	assertInteger(t, cmdExists(store, []string{"EXISTS", "h"}), 0)

	// missing key -> 0
	assertInteger(t, cmdHDel(store, []string{"HDEL", "missing", "f"}), 0)

	// wrong arg counts -> Error
	assertKind(t, cmdHDel(store, []string{"HDEL", "h"}), resp.Error)
	assertKind(t, cmdHDel(store, []string{"HDEL"}), resp.Error)
}

func TestHGetAll(t *testing.T) {
	store := make(map[string]entry)
	cmdHSet(store, []string{"HSET", "h", "f1", "v1", "f2", "v2"})

	assertHashReply(t, cmdHGetAll(store, []string{"HGETALL", "h"}), map[string]string{"f1": "v1", "f2": "v2"})

	// missing key -> empty Array, NOT Null
	assertArray(t, cmdHGetAll(store, []string{"HGETALL", "missing"}), []string{})

	// wrong arg counts -> Error
	assertKind(t, cmdHGetAll(store, []string{"HGETALL"}), resp.Error)
	assertKind(t, cmdHGetAll(store, []string{"HGETALL", "h", "x"}), resp.Error)
}

// ---------- SADD / SREM / SISMEMBER / SMEMBERS / SCARD ----------

func TestCmdSAdd(t *testing.T) {
	store := make(map[string]entry)

	// counts only genuinely new members
	assertInteger(t, cmdSAdd(store, []string{"SADD", "s", "a", "b", "c"}), 3)
	assertInteger(t, cmdSAdd(store, []string{"SADD", "s", "a", "d"}), 1)
	assertInteger(t, cmdSAdd(store, []string{"SADD", "s", "a", "b"}), 0)
	assertInteger(t, cmdSCard(store, []string{"SCARD", "s"}), 4)

	// duplicates within a single call count once
	assertInteger(t, cmdSAdd(store, []string{"SADD", "s2", "x", "x"}), 1)
	assertInteger(t, cmdSCard(store, []string{"SCARD", "s2"}), 1)

	// wrong arg counts -> Error
	assertKind(t, cmdSAdd(store, []string{"SADD", "s"}), resp.Error)
	assertKind(t, cmdSAdd(store, []string{"SADD"}), resp.Error)
}

func TestCmdSRem(t *testing.T) {
	store := make(map[string]entry)
	cmdSAdd(store, []string{"SADD", "s", "a", "b", "c"})

	// counts only members that existed
	assertInteger(t, cmdSRem(store, []string{"SREM", "s", "a", "missing"}), 1)
	assertInteger(t, cmdSIsMember(store, []string{"SISMEMBER", "s", "a"}), 0)

	// removing the last members deletes the key entirely
	assertInteger(t, cmdSRem(store, []string{"SREM", "s", "b", "c"}), 2)
	assertInteger(t, cmdExists(store, []string{"EXISTS", "s"}), 0)

	// missing key -> 0
	assertInteger(t, cmdSRem(store, []string{"SREM", "missing", "a"}), 0)

	// wrong arg counts -> Error
	assertKind(t, cmdSRem(store, []string{"SREM", "s"}), resp.Error)
	assertKind(t, cmdSRem(store, []string{"SREM"}), resp.Error)
}

func TestCmdSIsMemberSMembersSCard(t *testing.T) {
	store := make(map[string]entry)
	cmdSAdd(store, []string{"SADD", "s", "a", "b", "c"})

	assertInteger(t, cmdSIsMember(store, []string{"SISMEMBER", "s", "a"}), 1)
	assertInteger(t, cmdSIsMember(store, []string{"SISMEMBER", "s", "zz"}), 0)
	assertInteger(t, cmdSIsMember(store, []string{"SISMEMBER", "missing", "a"}), 0)

	assertArrayUnordered(t, cmdSMembers(store, []string{"SMEMBERS", "s"}), []string{"a", "b", "c"})
	assertArray(t, cmdSMembers(store, []string{"SMEMBERS", "missing"}), []string{})

	assertInteger(t, cmdSCard(store, []string{"SCARD", "s"}), 3)
	assertInteger(t, cmdSCard(store, []string{"SCARD", "missing"}), 0)

	// wrong arg counts -> Error
	assertKind(t, cmdSIsMember(store, []string{"SISMEMBER", "s"}), resp.Error)
	assertKind(t, cmdSMembers(store, []string{"SMEMBERS"}), resp.Error)
	assertKind(t, cmdSCard(store, []string{"SCARD", "s", "x"}), resp.Error)
}

// ---------- ZSCORE ----------

func TestCmdZScore(t *testing.T) {
	store := make(map[string]entry)
	store["z"] = zsetEntry(zpair{"a", 1.5}, zpair{"b", 2})

	// scores come back as bulk strings; whole numbers have no trailing ".0"
	assertBulkString(t, cmdZScore(store, []string{"ZSCORE", "z", "a"}), "1.5")
	assertBulkString(t, cmdZScore(store, []string{"ZSCORE", "z", "b"}), "2")

	// missing member and missing key both -> Null
	assertKind(t, cmdZScore(store, []string{"ZSCORE", "z", "missing"}), resp.Null)
	assertKind(t, cmdZScore(store, []string{"ZSCORE", "missing", "a"}), resp.Null)

	// wrong arg counts -> Error
	assertKind(t, cmdZScore(store, []string{"ZSCORE", "z"}), resp.Error)
	assertKind(t, cmdZScore(store, []string{"ZSCORE", "z", "a", "x"}), resp.Error)
}

// ---------- ZRANGE ----------

func TestCmdZRange(t *testing.T) {
	store := make(map[string]entry)
	store["z"] = zsetEntry(zpair{"a", 1}, zpair{"b", 2}, zpair{"c", 3})

	// the plain form (no WITHSCORES) is valid ZRANGE
	assertArray(t, cmdZRange(store, []string{"ZRANGE", "z", "0", "-1"}), []string{"a", "b", "c"})
	assertArray(t, cmdZRange(store, []string{"ZRANGE", "z", "1", "2"}), []string{"b", "c"})

	// WITHSCORES interleaves member, score
	assertArray(t, cmdZRange(store, []string{"ZRANGE", "z", "0", "-1", "WITHSCORES"}),
		[]string{"a", "1", "b", "2", "c", "3"})

	// negative indexes
	assertArray(t, cmdZRange(store, []string{"ZRANGE", "z", "-2", "-1", "WITHSCORES"}),
		[]string{"b", "2", "c", "3"})

	// tied scores order lexicographically by member, regardless of insert order
	store["tied"] = zsetEntry(zpair{"b", 1}, zpair{"a", 1}, zpair{"c", 1})
	assertArray(t, cmdZRange(store, []string{"ZRANGE", "tied", "0", "-1", "WITHSCORES"}),
		[]string{"a", "1", "b", "1", "c", "1"})

	// out-of-range or crossed indexes -> empty Array, NOT Null
	assertArray(t, cmdZRange(store, []string{"ZRANGE", "z", "5", "9", "WITHSCORES"}), []string{})
	assertArray(t, cmdZRange(store, []string{"ZRANGE", "z", "2", "1", "WITHSCORES"}), []string{})

	// missing key -> empty Array
	assertArray(t, cmdZRange(store, []string{"ZRANGE", "missing", "0", "-1", "WITHSCORES"}), []string{})

	// non-integer index -> Error
	assertKind(t, cmdZRange(store, []string{"ZRANGE", "z", "abc", "-1", "WITHSCORES"}), resp.Error)
	assertKind(t, cmdZRange(store, []string{"ZRANGE", "z", "0", "abc", "WITHSCORES"}), resp.Error)

	// a 5th arg that isn't WITHSCORES -> Error
	assertKind(t, cmdZRange(store, []string{"ZRANGE", "z", "0", "-1", "BOGUS"}), resp.Error)

	// wrong arg counts -> Error
	assertKind(t, cmdZRange(store, []string{"ZRANGE", "z", "0"}), resp.Error)
	assertKind(t, cmdZRange(store, []string{"ZRANGE", "z", "0", "-1", "WITHSCORES", "x"}), resp.Error)
}

// ---------- ZRANK ----------

func TestCmdZRank(t *testing.T) {
	store := make(map[string]entry)
	store["z"] = zsetEntry(zpair{"a", 1}, zpair{"c", 2}, zpair{"b", 2})

	// rank is 0-based, ordered by score then member
	assertInteger(t, cmdZRank(store, []string{"ZRANK", "z", "a"}), 0)
	assertInteger(t, cmdZRank(store, []string{"ZRANK", "z", "b"}), 1)
	assertInteger(t, cmdZRank(store, []string{"ZRANK", "z", "c"}), 2)

	// missing member and missing key both -> Null
	assertKind(t, cmdZRank(store, []string{"ZRANK", "z", "missing"}), resp.Null)
	assertKind(t, cmdZRank(store, []string{"ZRANK", "missing", "a"}), resp.Null)

	// wrong arg counts -> Error
	assertKind(t, cmdZRank(store, []string{"ZRANK", "z"}), resp.Error)
	assertKind(t, cmdZRank(store, []string{"ZRANK", "z", "a", "x"}), resp.Error)
}

// ---------- ZREM ----------

func TestCmdZRem(t *testing.T) {
	store := make(map[string]entry)
	store["z"] = zsetEntry(zpair{"a", 1}, zpair{"b", 2}, zpair{"c", 3})

	// counts only members that existed, and both views agree afterward
	assertInteger(t, cmdZRem(store, []string{"ZREM", "z", "b", "missing"}), 1)
	assertKind(t, cmdZScore(store, []string{"ZSCORE", "z", "b"}), resp.Null)
	assertArray(t, cmdZRange(store, []string{"ZRANGE", "z", "0", "-1", "WITHSCORES"}),
		[]string{"a", "1", "c", "3"})
	assertInteger(t, cmdZRank(store, []string{"ZRANK", "z", "c"}), 1)

	// removing the last members deletes the key entirely
	assertInteger(t, cmdZRem(store, []string{"ZREM", "z", "a", "c"}), 2)
	assertInteger(t, cmdExists(store, []string{"EXISTS", "z"}), 0)

	// missing key -> 0
	assertInteger(t, cmdZRem(store, []string{"ZREM", "missing", "a"}), 0)

	// wrong arg counts -> Error
	assertKind(t, cmdZRem(store, []string{"ZREM", "z"}), resp.Error)
	assertKind(t, cmdZRem(store, []string{"ZREM"}), resp.Error)
}

// ---------- ZADD ----------
// NOTE: keep this test LAST in the file. cmdZAdd currently panics, and a panic
// aborts the whole test binary; declared last, every other test reports first.

func TestCmdZAdd(t *testing.T) {
	store := make(map[string]entry)

	// a new member returns 1 and is immediately visible to ZSCORE
	assertInteger(t, cmdZAdd(store, []string{"ZADD", "z", "1", "a"}), 1)
	assertBulkString(t, cmdZScore(store, []string{"ZSCORE", "z", "a"}), "1")

	// multiple score/member pairs in one call
	assertInteger(t, cmdZAdd(store, []string{"ZADD", "z", "2", "b", "3", "c"}), 2)
	assertArray(t, cmdZRange(store, []string{"ZRANGE", "z", "0", "-1", "WITHSCORES"}),
		[]string{"a", "1", "b", "2", "c", "3"})

	// re-scoring an existing member returns 0, and BOTH views agree: the map
	// has the new score and the skip list holds the member exactly once, at
	// its new position
	assertInteger(t, cmdZAdd(store, []string{"ZADD", "z", "5", "a"}), 0)
	assertBulkString(t, cmdZScore(store, []string{"ZSCORE", "z", "a"}), "5")
	assertArray(t, cmdZRange(store, []string{"ZRANGE", "z", "0", "-1", "WITHSCORES"}),
		[]string{"b", "2", "c", "3", "a", "5"})
	assertInteger(t, cmdZRank(store, []string{"ZRANK", "z", "a"}), 2)

	// non-numeric score -> the canonical error
	assertErrorMsg(t, cmdZAdd(store, []string{"ZADD", "z", "abc", "m"}), "ERR value is not a valid float")

	// dangling score with no member -> Error
	assertKind(t, cmdZAdd(store, []string{"ZADD", "z", "1", "a", "2"}), resp.Error)

	// wrong arg counts -> Error
	assertKind(t, cmdZAdd(store, []string{"ZADD", "z", "1"}), resp.Error)
	assertKind(t, cmdZAdd(store, []string{"ZADD", "z"}), resp.Error)
}
