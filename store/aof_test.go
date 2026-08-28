package store

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"redis-clone/resp"
)

// ---------- helpers ----------

// encodeCmds turns a command script into the exact bytes an AOF would hold.
func encodeCmds(cmds [][]string) []byte {
	var buf bytes.Buffer
	for _, cmd := range cmds {
		v := resp.Command(cmd)
		buf.Write(v.Encode())
	}
	return buf.Bytes()
}

// canonCmd renders one dumped command as a comparable string. Hash, Set, and
// ZSet iterate Go maps, so the field/member order inside a single HSET, SADD,
// or ZADD varies run to run; sorting those groups makes semantically equal
// commands textually equal. RPUSH order is meaningful and left alone.
func canonCmd(cmd []string) string {
	c := append([]string{}, cmd...)
	switch strings.ToUpper(c[0]) {
	case "SADD":
		sort.Strings(c[2:])
	case "HSET", "ZADD":
		rest := c[2:]
		pairs := []string{}
		for i := 0; i+1 < len(rest); i += 2 {
			pairs = append(pairs, rest[i]+"\x1f"+rest[i+1])
		}
		sort.Strings(pairs)
		c = append(c[:2], pairs...)
	}
	return strings.Join(c, "\x1f")
}

// assertSameState compares two stores by comparing their minimal recreations:
// if dumpCommands of both, canonicalized and sorted, are identical, the
// stores hold the same data.
func assertSameState(t *testing.T, want, got map[string]entry) {
	t.Helper()
	dw, dg := dumpCommands(want), dumpCommands(got)
	cw := make([]string, len(dw))
	cg := make([]string, len(dg))
	for i, c := range dw {
		cw[i] = canonCmd(c)
	}
	for i, c := range dg {
		cg[i] = canonCmd(c)
	}
	sort.Strings(cw)
	sort.Strings(cg)
	if len(cw) != len(cg) {
		t.Fatalf("stores differ: %d commands to recreate one, %d the other\nwant: %v\ngot:  %v", len(cw), len(cg), cw, cg)
	}
	for i := range cw {
		if cw[i] != cg[i] {
			t.Errorf("stores differ at command %d:\nwant: %s\ngot:  %s", i, cw[i], cg[i])
		}
	}
}

// runScript dispatches a command script into a fresh store.
func runScript(t *testing.T, cmds [][]string) map[string]entry {
	t.Helper()
	store := make(map[string]entry)
	for _, cmd := range cmds {
		if v := dispatch(store, cmd); v.Kind == resp.Error {
			t.Fatalf("script command %v failed: %s", cmd, v.Str)
		}
	}
	return store
}

// a script that touches every data type, TTLs, deletes, and an INCR on a
// missing key — the shape Part F asks for.
var fullScript = [][]string{
	{"SET", "name", "sagar"},
	{"SET", "gone", "x"},
	{"DEL", "gone"},
	{"INCR", "counter"},
	{"INCR", "counter"},
	{"RPUSH", "list", "a", "b", "c"},
	{"LPUSH", "list", "z"},
	{"HSET", "hash", "f1", "v1", "f2", "v2"},
	{"SADD", "set", "m1", "m2", "m3"},
	{"SREM", "set", "m3"},
	{"ZADD", "zset", "1.5", "alice", "2", "bob"},
	{"SET", "ttlkey", "v"},
	{"EXPIRE", "ttlkey", "100"},
}

// ---------- replayFrom ----------

func TestReplayFromBuffer(t *testing.T) {
	data := encodeCmds([][]string{
		{"SET", "a", "1"},
		{"SET", "b", "2"},
		{"DEL", "a"},
		{"RPUSH", "l", "x", "y"},
	})

	store := make(map[string]entry)
	replayFrom(bytes.NewReader(data), store)

	if _, ok := lookup(store, "a"); ok {
		t.Error("deleted key 'a' survived replay")
	}
	assertBulkString(t, cmdGet(store, []string{"GET", "b"}), "2")
	ent, ok := lookup(store, "l")
	if !ok || ent.Kind != List || len(ent.List) != 2 || ent.List[0] != "x" || ent.List[1] != "y" {
		t.Errorf("list after replay: got %+v, want [x y]", ent.List)
	}
}

func TestReplayFromEmpty(t *testing.T) {
	store := make(map[string]entry)
	replayFrom(bytes.NewReader(nil), store)
	if len(store) != 0 {
		t.Errorf("replay of empty input produced %d keys", len(store))
	}
}

func TestReplayFromTruncatedTail(t *testing.T) {
	data := encodeCmds([][]string{
		{"SET", "k1", "v1"},
		{"SET", "k2", "v2"},
		{"SET", "k3", "v3"},
	})
	// half of a fourth command, as a crash mid-write would leave it
	fourthCmd := resp.Command([]string{"SET", "k4", "v4"})
	fourth := fourthCmd.Encode()
	data = append(data, fourth[:len(fourth)/2]...)

	store := make(map[string]entry)
	replayFrom(bytes.NewReader(data), store) // must not panic

	for _, k := range []string{"k1", "k2", "k3"} {
		if _, ok := lookup(store, k); !ok {
			t.Errorf("key %s from the intact prefix is missing", k)
		}
	}
	if _, ok := lookup(store, "k4"); ok {
		t.Error("truncated command k4 was applied")
	}
}

func TestReplayFromStopsOnNonCommand(t *testing.T) {
	data := encodeCmds([][]string{{"SET", "before", "1"}})
	data = append(data, []byte(":42\r\n")...) // valid RESP, not a command
	data = append(data, encodeCmds([][]string{{"SET", "after", "2"}})...)

	store := make(map[string]entry)
	replayFrom(bytes.NewReader(data), store)

	if _, ok := lookup(store, "before"); !ok {
		t.Error("command before the bad entry was not applied")
	}
	// the file is untrustworthy past the bad entry: replay must stop, not skip
	if _, ok := lookup(store, "after"); ok {
		t.Error("replay continued past a non-command entry")
	}
}

// ---------- logForm ----------

func TestLogForm(t *testing.T) {
	store := make(map[string]entry)
	dispatch(store, []string{"SET", "k", "v"})

	// plain writes log verbatim
	got := logForm(store, []string{"SET", "k", "v"})
	if len(got) != 1 || strings.Join(got[0], " ") != "SET k v" {
		t.Errorf("plain SET: got %v, want [[SET k v]]", got)
	}

	// EXPIRE logs as one PEXPIREAT with the absolute deadline
	dispatch(store, []string{"EXPIRE", "k", "100"})
	got = logForm(store, []string{"EXPIRE", "k", "100"})
	if len(got) != 1 || got[0][0] != "PEXPIREAT" || got[0][1] != "k" {
		t.Fatalf("EXPIRE: got %v, want one PEXPIREAT", got)
	}
	assertPlausibleDeadline(t, got[0][2], 100*time.Second)

	// 5-arg SET logs as two commands: plain SET, then PEXPIREAT
	dispatch(store, []string{"SET", "k2", "v2", "EX", "50"})
	got = logForm(store, []string{"SET", "k2", "v2", "EX", "50"})
	if len(got) != 2 {
		t.Fatalf("SET..EX: got %d commands %v, want 2", len(got), got)
	}
	if strings.Join(got[0], " ") != "SET k2 v2" {
		t.Errorf("SET..EX first command: got %v, want [SET k2 v2]", got[0])
	}
	if got[1][0] != "PEXPIREAT" || got[1][1] != "k2" {
		t.Errorf("SET..EX second command: got %v, want PEXPIREAT k2", got[1])
	}
	assertPlausibleDeadline(t, got[1][2], 50*time.Second)
}

func assertPlausibleDeadline(t *testing.T, msStr string, ttl time.Duration) {
	t.Helper()
	ms, err := strconv.ParseInt(msStr, 10, 64)
	if err != nil {
		t.Fatalf("deadline %q is not an integer", msStr)
	}
	now := time.Now().UnixMilli()
	if ms <= now || ms > now+ttl.Milliseconds()+2000 {
		t.Errorf("deadline %d not within (%d, %d]", ms, now, now+ttl.Milliseconds()+2000)
	}
}

// ---------- dumpCommands round trip ----------

func TestDumpRoundTrip(t *testing.T) {
	storeA := runScript(t, fullScript)

	// dump A, encode as an AOF, replay into a fresh store B
	data := encodeCmds(dumpCommands(storeA))
	storeB := make(map[string]entry)
	replayFrom(bytes.NewReader(data), storeB)

	assertSameState(t, storeA, storeB)

	// the TTL must survive as the same absolute deadline, not reset
	entA, _ := lookup(storeA, "ttlkey")
	entB, ok := lookup(storeB, "ttlkey")
	if !ok {
		t.Fatal("ttlkey missing after round trip")
	}
	if entA.expiresAt.UnixMilli() != entB.expiresAt.UnixMilli() {
		t.Errorf("deadline changed across round trip: %d != %d",
			entA.expiresAt.UnixMilli(), entB.expiresAt.UnixMilli())
	}
}

// ---------- load ----------

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.aof")
	if err := os.WriteFile(path, encodeCmds([][]string{{"SET", "k", "v"}}), 0644); err != nil {
		t.Fatal(err)
	}

	// load only needs the path; no writer goroutine required
	aof := &AOF{path: path}
	store := make(map[string]entry)
	aof.load(store)

	assertBulkString(t, cmdGet(store, []string{"GET", "k"}), "v")
}

func TestLoadMissingFile(t *testing.T) {
	aof := &AOF{path: filepath.Join(t.TempDir(), "never-created.aof")}
	store := make(map[string]entry)
	aof.load(store) // first boot: must be silent, not fatal
	if len(store) != 0 {
		t.Errorf("load of missing file produced %d keys", len(store))
	}
}

// ---------- rewrite ----------

// rewriteWithTimeout guards against the handshake deadlocking: a hung rewrite
// fails the test in 5s instead of hanging the whole run.
func rewriteWithTimeout(t *testing.T, aof *AOF, store map[string]entry) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- aof.rewrite(store) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("rewrite failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("rewrite deadlocked (no handshake partner or ack never arrived)")
	}
}

func TestRewriteAlways(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.aof")
	aof, err := NewAOF(path, Always)
	if err != nil {
		t.Fatal(err)
	}

	// build state and log it the way the commander would: full history,
	// including the redundant INCR steps a rewrite exists to collapse
	store := make(map[string]entry)
	for _, cmd := range fullScript {
		if v := dispatch(store, cmd); v.Kind == resp.Error {
			t.Fatalf("script command %v failed: %s", cmd, v.Str)
		}
		for _, logged := range logForm(store, cmd) {
			v := resp.Command(logged)
			if err := aof.Append(v.Encode()); err != nil {
				t.Fatal(err)
			}
		}
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(before, []byte("INCR")) {
		t.Fatal("precondition: history should contain INCR before rewrite")
	}

	rewriteWithTimeout(t, aof, store)

	// the rewritten file holds state, not history
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(after, []byte("INCR")) {
		t.Error("rewritten file still contains INCR history")
	}

	// and it replays to the same state
	replayed := make(map[string]entry)
	replayFrom(bytes.NewReader(after), replayed)
	assertSameState(t, store, replayed)

	// appends after the rewrite must land in the new file (handle swapped)
	dispatch(store, []string{"SET", "post", "rewrite"})
	postCmd := resp.Command([]string{"SET", "post", "rewrite"})
	if err := aof.Append(postCmd.Encode()); err != nil {
		t.Fatalf("append after rewrite: %v", err)
	}
	final, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	replayed = make(map[string]entry)
	replayFrom(bytes.NewReader(final), replayed)
	assertSameState(t, store, replayed)
}

func TestRewriteDrainsPendingWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.aof")
	aof, err := NewAOF(path, Everysec)
	if err != nil {
		t.Fatal(err)
	}

	// dispatch first, then queue the appends — exactly the commander's order.
	// The queue is deliberately left undrained: these bytes are "pending"
	// when the rewrite starts, and their effects are already in the store.
	script := [][]string{
		{"RPUSH", "list", "a", "b", "c"},
		{"SET", "k", "v"},
	}
	store := make(map[string]entry)
	for _, cmd := range script {
		dispatch(store, cmd)
		v := resp.Command(cmd)
		aof.writes <- v.Encode()
	}

	rewriteWithTimeout(t, aof, store)

	// the ack means the drain finished before rewrite returned
	if n := len(aof.writes); n != 0 {
		t.Errorf("writes channel still holds %d messages after rewrite", n)
	}

	// shut the writer down so everything it would ever write is flushed,
	// then prove the file holds each command's effect exactly once — a
	// missed drain would append RPUSH duplicates after the dump
	close(aof.writes)
	time.Sleep(300 * time.Millisecond)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	replayed := make(map[string]entry)
	replayFrom(bytes.NewReader(data), replayed)

	ent, ok := lookup(replayed, "list")
	if !ok || len(ent.List) != 3 {
		t.Fatalf("list after rewrite+replay: got %v, want exactly [a b c] — duplicates mean pending writes reached the new file", ent.List)
	}
	assertSameState(t, store, replayed)
}
