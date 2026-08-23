package store

import (
	"redis-clone/resp"

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
