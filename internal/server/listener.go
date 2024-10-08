package server

import (
	"fmt"
	"github.com/arpitpandey992/go-mpd/internal/database"
	"io"
	"log"
	"net"
	"strings"
)

// TODO: move these constants to config.yml
const (
	DEFAULT_SERVER_PROTOCOL = "tcp"
	DEFAULT_SERVER_ADDRESS  = "127.0.0.1:6600"
	DEFAULT_DELIMITER       = "\n"
)

type Handlers struct {
	audioRequestHandler *AudioRequestsHandler
	dbRequestsHandler   *DbRequestsHandler
}

type Server struct {
	Protocol  string
	Address   string
	Delimiter string // keeping it as string since we can go from string to byte but not the other way around if we want to support multiple character delimiters
	listener  net.Listener
}

func CreateAndStartServer(db *database.AudioMeilisearchClient) *Server {
	// TODO: make sure to have a close function which will release all resources. Keep a handler ready for managing go routines
	listener := getListener(DEFAULT_SERVER_PROTOCOL, DEFAULT_SERVER_ADDRESS)
	server := &Server{
		Address:   DEFAULT_SERVER_ADDRESS,
		Protocol:  DEFAULT_SERVER_PROTOCOL,
		Delimiter: DEFAULT_DELIMITER,
		listener:  listener,
	}
	go server.handleIncomingConnections(db)
	return server
}

func (server *Server) Close() {
	server.listener.Close()
}

func (server *Server) handleIncomingConnections(db *database.AudioMeilisearchClient) {
	for {
		conn, err := server.listener.Accept()
		if err != nil {
			if isListenerClosedError(err) {
				log.Print("listener closed, stopping connection handling goroutine")
				break
			}
			log.Printf("failed to accept incoming connection request, error: %v", err)
			continue
		}
		log.Print("successfully connected with incoming client")
		handlers := &Handlers{audioRequestHandler: getNewAudioRequestsHandler(), dbRequestsHandler: getNewDbRequestsHandler(db)}
		server.sendWelcomeMessageToConnectionClient(conn)
		go server.handleConnection(conn, handlers)
	}
}

func getListener(protocol string, server_address string) net.Listener {
	listener, err := net.Listen(protocol, server_address)
	if err != nil {
		log.Fatalf("cannot start the server at: %s\n%v", server_address, err)
	}
	log.Printf("listening on: %v", listener.Addr().String())
	return listener
}

func (server *Server) handleConnection(conn net.Conn, handlers *Handlers) {
	defer conn.Close()
	buf := make([]byte, 2500)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			if err != io.EOF {
				log.Print(err)
			}
			return
		}
		log.Printf("server received: %q", buf[:n])
		err = server.handleIncomingRequest(string(buf[:n]), conn, handlers)
		if err != nil {
			log.Print("error: ", err)
			_ = server.sendMessageToConnectionClient("error: "+err.Error(), conn)
		}
	}
}

func (server *Server) handleIncomingRequest(command string, conn net.Conn, handlers *Handlers) error {
	commands := server.breakIncomingCommandToMultipleCommands(command)
	for _, splitCommand := range commands {
		chunks := server.breakCommandIntoChunks(splitCommand)
		log.Printf("debug: commands: %s", strings.Join(chunks, ", "))
		for i, chunk := range chunks {
			chunks[i] = strings.TrimSpace(chunk)
		}
		if len(chunks) == 0 {
			return nil
		}
		requestType := chunks[0]
		switch requestType {
		case "ping":
			err := server.sendMessageToConnectionClient("pong", conn)
			if err != nil {
				return fmt.Errorf("error while responding to incoming ping. error: %s", err.Error())
			}
		case "audio":
			if len(chunks) < 2 {
				return fmt.Errorf("audio command expects at least one argument")
			}
			returnMessage, err := handlers.audioRequestHandler.HandleAudioRequest(chunks[1:])
			if err != nil {
				return err
			}
			if returnMessage != "" {
				_ = server.sendMessageToConnectionClient(returnMessage, conn)
			}
		case "db":
			if len(chunks) < 2 {
				return fmt.Errorf("database command expects at least one argument")
			}
			returnMessage, err := handlers.dbRequestsHandler.HandleDbRequest(chunks[1:])
			if err != nil {
				return err
			}
			if returnMessage != "" {
				_ = server.sendMessageToConnectionClient(returnMessage, conn)
			}

		default:
			return fmt.Errorf("invalid request type: %s", requestType)
		}
	}
	return nil
}

func (server *Server) sendWelcomeMessageToConnectionClient(conn net.Conn) {
	welcomeMessage := "Welcome to Go-MPD!"
	err := server.sendMessageToConnectionClient(welcomeMessage, conn)
	if err != nil {
		log.Printf("error: could not send welcome message to %s, error: %s", conn.RemoteAddr(), err)
	}
}

func (server *Server) sendMessageToConnectionClient(message string, conn net.Conn) error {
	_, err := conn.Write([]byte(message + DEFAULT_DELIMITER))
	return err
}

func (server *Server) breakCommandIntoChunks(command string) []string {
	command = strings.TrimSpace(command)
	chunks := []string{}
	i, n := 0, len(command)
	for i < n {
		j := i + 1
		if command[i] == '"' {
			for j < n && command[j] != '"' {
				j++
			}
			chunks = append(chunks, command[i+1:j])
			i = j + 1
		} else {
			for j < n && command[j] != ' ' {
				j++
			}
			chunks = append(chunks, command[i:j])
			i = j + 1
		}
	}
	return chunks
}

func (server *Server) breakIncomingCommandToMultipleCommands(command string) []string {
	command = strings.TrimSpace(command)
	return strings.Split(command, server.Delimiter)
}

func isListenerClosedError(err error) bool {
	if opErr, ok := err.(*net.OpError); ok {
		if opErr.Err.Error() == "use of closed network connection" {
			return true
		}
	}
	return false
}
