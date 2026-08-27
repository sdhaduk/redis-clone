package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"redis-clone/resp"
	"redis-clone/store"
	"strings"
)

func main() {
	var appendOnly bool
	var appendFSync string
	var path string = "./appendonly.aof"
	var aof *store.AOF

	flag.BoolVar(&appendOnly, "appendonly", false, "keep an AOF")
	flag.StringVar(&appendFSync, "appendfsync", "everysec", "level of consistency")
	flag.Parse()

	policy, err := store.ParsePolicy(appendFSync)
	if err != nil {
		log.Fatal(err)
	}

	if appendOnly {
		aof, err = store.NewAOF(path, policy)
		if err != nil {
			log.Fatal(err)
		}
	}

	listener, err := net.Listen("tcp", ":6379")
	if err != nil {
		log.Fatal("Error starting server:", err)
	}
	defer listener.Close()

	requests := make(chan store.Message)
	go store.RunCommander(requests, aof)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Println("Error accepting conn:", err)
			continue
		}

		go handleConnection(conn, requests)
	}
}

func handleConnection(conn net.Conn, requests chan store.Message) {
	defer conn.Close()
	reader := bufio.NewReader(conn)

	for {
		val, err := resp.Decode(reader)
		if err == io.EOF {
			return
		}
		if err != nil {
			response := resp.Value{Kind: resp.Error, Str: fmt.Sprintf("ERR %v", err)}
			_, err = conn.Write(response.Encode())
			if err != nil {
				log.Println("Server write error:", err)
				return
			}
			continue
		}
		
		args, err := val.Args()
		if err != nil {
			response := resp.Value{Kind: resp.Error, Str: err.Error()}
			_, err := conn.Write(response.Encode())
			if err != nil {
				log.Println("Server write error:", err)
				return
			}
			continue
		}

		if strings.ToUpper(args[0]) == "PING" {
			response := resp.Value{Kind: resp.SimpleString, Str: "PONG"}
			_, err := conn.Write(response.Encode())
			if err != nil {
				log.Println("Server write error:", err)
				return
			}
			continue
		}

		if strings.ToUpper(args[0]) == "ECHO" {
			if len(args) < 2 {
				response := resp.Value{Kind: resp.Error, Str: "ERR ECHO requires one argument"}
				_, err := conn.Write(response.Encode())
				if err != nil {
					log.Println("Server write error:", err)
					return
				}
				continue
			}
			response := resp.Value{Kind: resp.BulkString, Str: args[1]}
			_, err := conn.Write(response.Encode())
			if err != nil {
				log.Println("Server write error:", err)
				return
			}
			continue
		}

		reply := make(chan resp.Value, 1)
		msg := store.Message{Args: args, Reply: reply}
		requests <- msg
		response := <-reply
		_, err = conn.Write(response.Encode())
		if err != nil {
			log.Println("Server write error:", err)
			return
		}
	}
}
