package main

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
)

type RedisType int

const (
	SimpleString RedisType = iota
	Error
	Integer
	BulkString
	Null
	Array
)

// One payload is meaningful at a time, selected by Kind
type Value struct {
	Kind RedisType
	Str string
	Num int64
	Elems []Value
}

func NewError(msg string) Value {
	return Value{Kind: Error, Str: msg}
}

func NewSimpleString(msg string) Value {
	return Value{Kind: SimpleString, Str: msg}
}

func NewBulkString(msg string) Value {
	return Value{Kind: BulkString, Str: msg}
}

func (val *Value) Encode() []byte {
	switch val.Kind {
	case SimpleString:
		encoded := make([]byte, 0, 1 + len(val.Str) + 2)
		encoded = append(encoded, '+')
		encoded = append(encoded, val.Str...)
		encoded = append(encoded, '\r', '\n')
		return encoded

	case Error:
		encoded := make([]byte, 0, 1 + len(val.Str) + 2)
		encoded = append(encoded, '-')
		encoded = append(encoded, val.Str...)
		encoded = append(encoded, '\r', '\n')
		return encoded

	case Integer:
		encoded := []byte{':'}
		encoded = strconv.AppendInt(encoded, val.Num, 10)
		encoded = append(encoded, '\r', '\n')
		return encoded

	case BulkString:
		encoded := []byte{'$'}
		encoded = strconv.AppendInt(encoded, int64(len(val.Str)), 10)
		encoded = append(encoded, '\r', '\n')
		encoded = append(encoded, val.Str...)
		encoded = append(encoded, '\r', '\n')
		return encoded	

	case Null:
		return []byte{'$', '-','1', '\r', '\n'}
	
	case Array:
		encoded := []byte{'*'}
		encoded = strconv.AppendInt(encoded, int64(len(val.Elems)), 10)
		encoded = append(encoded, '\r', '\n')

		for _, e := range val.Elems {
			cur_encoding := e.Encode()
			encoded = append(encoded, cur_encoding...)
		}
		return encoded	

	default:
		panic("unreachable")
	}
}

func readLine(buf *bufio.Reader) (string, error) {
	data, err := buf.ReadString('\n')
	if err != nil {
		return "", err
	}
	if len(data) < 2 {
		return "", fmt.Errorf("line does not end with CRLF")
	}
	if data[len(data) - 2:] != "\r\n" {
		return "", fmt.Errorf("line does not end with CRLF")
	}
	return data[:len(data) - 2], nil
}

func Decode(buf *bufio.Reader) (Value, error) {
	marker, err := buf.ReadByte()
	if err != nil {
		return Value{}, err
	}

	switch marker {
	case '+':
		data, err := readLine(buf)
		if err != nil {
			return Value{}, err
		}	
		return Value{Kind: SimpleString, Str: data}, nil
	
	case '-':
		data, err := readLine(buf)
		if err != nil {
			return Value{}, err
		}	
		return Value{Kind: Error, Str: data}, nil

	case ':':
		data, err := readLine(buf)
		if err != nil {
			return Value{}, err
		}
		
		num, err := strconv.ParseInt(data, 10, 64)
		if err != nil {
			return Value{}, err
		}
		return Value{Kind: Integer, Num: num}, nil
	
	case '$':
		length, err := readLine(buf)
		if err != nil {
			return Value{}, err
		}

		int_length, err := strconv.Atoi(length)
		if err != nil {
			return Value{}, err
		}

		if int_length == -1 {
			return Value{Kind: Null}, nil
		}
		data := make([]byte, int_length)
		io.ReadFull(buf, data)
		
		left_over, err := readLine(buf)
		if err != nil {
			return Value{}, err
		}
		if left_over != "" {
			return Value{}, fmt.Errorf("payload longer than encoded length")
		}
		return Value{Kind: BulkString, Str: string(data)}, nil
	
	case '*':
		arr_len, err := readLine(buf)
		if err != nil {
			return Value{}, err
		}

		int_arr_len, err := strconv.Atoi(arr_len)
		if err != nil {
			return Value{}, err
		}

		elems := []Value{}
		for  i := 0; i < int_arr_len; i++ {
			cur_val, err := Decode(buf)
			if err != nil {
				return Value{}, err
			}
			elems = append(elems, cur_val)
		}
		return Value{Kind: Array, Elems: elems}, nil
	
	default:
		return Value{}, fmt.Errorf("unknown byte read; conn will be closed.")
	}
}
