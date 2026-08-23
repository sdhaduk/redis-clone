package resp

import (
	"bufio"
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// ---------- Encode ----------

func TestEncode(t *testing.T) {
	cases := []struct {
		name string
		val  Value
		want string // exact wire bytes per RESP2 spec
	}{
		{"simple string", Value{Kind: SimpleString, Str: "OK"}, "+OK\r\n"},
		{"error", Value{Kind: Error, Str: "ERR boom"}, "-ERR boom\r\n"},
		{"integer positive", Value{Kind: Integer, Num: 42}, ":42\r\n"},
		{"integer zero", Value{Kind: Integer, Num: 0}, ":0\r\n"},
		{"integer negative", Value{Kind: Integer, Num: -5}, ":-5\r\n"},
		{"bulk string", Value{Kind: BulkString, Str: "hello"}, "$5\r\nhello\r\n"},
		{"bulk string empty", Value{Kind: BulkString, Str: ""}, "$0\r\n\r\n"},
		// bulk strings are binary-safe: payload may itself contain \r\n
		{"bulk string binary", Value{Kind: BulkString, Str: "a\r\nb"}, "$4\r\na\r\nb\r\n"},
		{"null", Value{Kind: Null}, "$-1\r\n"},
		{
			"array",
			Value{Kind: Array, Elems: []Value{
				{Kind: BulkString, Str: "GET"},
				{Kind: BulkString, Str: "foo"},
			}},
			"*2\r\n$3\r\nGET\r\n$3\r\nfoo\r\n",
		},
		{"array empty", Value{Kind: Array, Elems: []Value{}}, "*0\r\n"},
		{
			"array nested",
			Value{Kind: Array, Elems: []Value{
				{Kind: Integer, Num: 1},
				{Kind: Array, Elems: []Value{{Kind: SimpleString, Str: "OK"}}},
			}},
			"*2\r\n:1\r\n*1\r\n+OK\r\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.val.Encode()
			if string(got) != tc.want {
				t.Errorf("Encode() = %q, want %q", got, tc.want)
			}
		})
	}
}

// ---------- Decode ----------

func TestDecode(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  Value
	}{
		{"simple string", "+OK\r\n", Value{Kind: SimpleString, Str: "OK"}},
		{"error", "-ERR boom\r\n", Value{Kind: Error, Str: "ERR boom"}},
		{"integer", ":42\r\n", Value{Kind: Integer, Num: 42}},
		{"integer negative", ":-5\r\n", Value{Kind: Integer, Num: -5}},
		{"bulk string", "$5\r\nhello\r\n", Value{Kind: BulkString, Str: "hello"}},
		{"bulk string empty", "$0\r\n\r\n", Value{Kind: BulkString, Str: ""}},
		// binary-safe: payload containing \r\n must survive intact
		{"bulk string binary", "$4\r\na\r\nb\r\n", Value{Kind: BulkString, Str: "a\r\nb"}},
		{"null bulk string", "$-1\r\n", Value{Kind: Null}},
		{
			"array",
			"*2\r\n$3\r\nGET\r\n$3\r\nfoo\r\n",
			Value{Kind: Array, Elems: []Value{
				{Kind: BulkString, Str: "GET"},
				{Kind: BulkString, Str: "foo"},
			}},
		},
		{"array empty", "*0\r\n", Value{Kind: Array, Elems: []Value{}}},
		{
			"array nested",
			"*2\r\n*1\r\n:1\r\n$3\r\nfoo\r\n",
			Value{Kind: Array, Elems: []Value{
				{Kind: Array, Elems: []Value{{Kind: Integer, Num: 1}}},
				{Kind: BulkString, Str: "foo"},
			}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Decode(bufio.NewReader(strings.NewReader(tc.input)))
			if err != nil {
				t.Fatalf("Decode(%q) returned error: %v", tc.input, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Decode(%q) = %+v, want %+v", tc.input, got, tc.want)
			}
		})
	}
}

// ---------- Round trip: Decode(Encode(v)) == v ----------

func TestRoundTrip(t *testing.T) {
	values := []Value{
		{Kind: SimpleString, Str: "PONG"},
		{Kind: Error, Str: "WRONGTYPE Operation against a key holding the wrong kind of value"},
		{Kind: Integer, Num: 1000},
		{Kind: Integer, Num: -1000},
		{Kind: Integer, Num: 0},
		{Kind: BulkString, Str: "hello"},
		{Kind: BulkString, Str: ""},
		{Kind: BulkString, Str: "with\r\nnewlines\nand\ttabs"},
		{Kind: Null},
		{Kind: Array, Elems: []Value{
			{Kind: BulkString, Str: "SET"},
			{Kind: BulkString, Str: "name"},
			{Kind: BulkString, Str: "sagar"},
		}},
		{Kind: Array, Elems: []Value{}},
	}

	for _, v := range values {
		encoded := v.Encode()
		got, err := Decode(bufio.NewReader(bytes.NewReader(encoded)))
		if err != nil {
			t.Errorf("round trip %+v: Decode(%q) returned error: %v", v, encoded, err)
			continue
		}
		if !reflect.DeepEqual(got, v) {
			t.Errorf("round trip failed: started with %+v, encoded to %q, decoded to %+v", v, encoded, got)
		}
	}
}

// ---------- Streaming: consecutive values off one reader ----------

// Decode must consume exactly one value and not a byte more, so the next
// call picks up cleanly. This is how a real connection behaves: many
// commands arrive back-to-back on the same reader.
func TestDecodeSequential(t *testing.T) {
	input := "+OK\r\n:7\r\n$3\r\nfoo\r\n"
	r := bufio.NewReader(strings.NewReader(input))

	want := []Value{
		{Kind: SimpleString, Str: "OK"},
		{Kind: Integer, Num: 7},
		{Kind: BulkString, Str: "foo"},
	}

	for i, w := range want {
		got, err := Decode(r)
		if err != nil {
			t.Fatalf("value #%d: unexpected error: %v", i+1, err)
		}
		if !reflect.DeepEqual(got, w) {
			t.Fatalf("value #%d: got %+v, want %+v", i+1, got, w)
		}
	}
}

// ---------- Malformed / evil inputs: all must return an error ----------

func TestDecodeErrors(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"empty input", ""},
		{"unknown type marker", "?wat\r\n"},
		{"integer not a number", ":abc\r\n"},
		{"bulk length not a number", "$abc\r\n"},
		{"bulk payload truncated", "$5\r\nhel"},
		{"bulk length lies (payload longer)", "$3\r\nhello\r\n"},
		{"bulk missing trailing crlf", "$5\r\nhello"},
		{"array count not a number", "*x\r\n"},
		{"array missing elements", "*2\r\n$3\r\nGET\r\n"},
		{"line ends with bare LF", "+OK\n"},
		{"lone LF line", "+\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Decode(bufio.NewReader(strings.NewReader(tc.input)))
			if err == nil {
				t.Errorf("Decode(%q) = %+v, want an error", tc.input, got)
			}
		})
	}
}
