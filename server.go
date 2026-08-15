package main
	
import (
    "bufio"
    "fmt"
    "log"
    "net"
    "strings"
)

func main() {
	listener, err := net.Listen("tcp", ":6379")
	if err != nil {
		log.Fatal("Error starting server:", err)
	}

	defer listener.Close()

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Println("Error accepting conn:", err)
			continue
		}

		go handleConnection(conn)
	}
}

func handleConnection(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	
	for {
		message, err := reader.ReadString('\n')
		if err != nil {
			log.Println("Read err:", err)
			return
		}
		ackMsg := strings.ToUpper(strings.TrimSpace(message))
		response := fmt.Sprintf("ACK %s\n", ackMsg)
		_, err = conn.Write([]byte(response))
		if err != nil {
			log.Println("Server write error:", err)
		}
	}
}