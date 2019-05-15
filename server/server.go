package main

import (
	"fmt"
	"log"
	"math/rand"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	game "github.com/golang-game/game"
)

//There are the settings of the server
const (
	connPort    = "8088"
	connType    = "tcp"
	connAddress = "localhost"
)

//Counter of connected clients
var clientCounter int32

//Game state of this server game
var gameXO game.GameState

//Map of client IDs to simplify free ID search
var clientIDs map[int]bool

//Main, what else is here to say?
func main() {
	l := startListening()
	defer l.Close()
	fmt.Println("Successfully started listening on", connAddress+":"+connPort)
	acceptClients(l)
}

//Start listening socket for connections from clients. Return new listener object or crashes the server, if error occured.
func startListening() *net.TCPListener {

	//Convert IP and port strings to TCP socket
	address, err := net.ResolveTCPAddr(connType, connAddress+":"+connPort)
	if err != nil {
		fmt.Println("Error starting server:", err)
		os.Exit(1)
	}

	//Start listening on socket, made in previous block
	l, err := net.ListenTCP(connType, address)
	if err != nil {
		fmt.Println("Error starting server:", err)
		os.Exit(1)
	}
	return l
}

//Accept connection from clients and declare syncronization tools, that will be required for playing.
func acceptClients(l *net.TCPListener) {

	//Initialize server-side syncronization tools and game
	var mutex sync.Mutex
	var servSync sync.WaitGroup
	gameConcluded := make(chan int, 1)
	gameXO = game.New()
	clientIDs = map[int]bool{1: false, 2: false}

	for true {
		//Adding just one, cause there should be always 2 clients, one client disconnecting is enough to start searching for new ones
		servSync.Add(1)

		for clientCounter < 2 {
			//Search for new connections
			conn, err := l.Accept()
			if err != nil {
				fmt.Println("Error accepting: ", err.Error())
				os.Exit(1)
			}
			atomic.AddInt32(&clientCounter, 1)

			//Find id, that is not taken already, so we can distinguish what each player is playing with
			newID := findFreeID()
			fmt.Println("Client", newID, "connected")

			//Start handling this particular client
			go handleClient(conn, newID, &mutex, &servSync, gameConcluded)
		}

		//Wait for disconnect
		servSync.Wait()
	}
}

//Function for finding free id in id map. Returns int representing new ID of a client or -1, if there are no free IDs.
func findFreeID() int {
	var newID = -1
	for k, v := range clientIDs {
		if !v {
			clientIDs[k] = true
			newID = k
			break
		}
	}
	return newID
}

//Function to handle clients connections.
func handleClient(conn net.Conn, id int, mutex *sync.Mutex, servSync *sync.WaitGroup, gameConcluded chan int) {

	//Acknowledging that client has disconnected
	defer servSync.Done()
	defer atomic.AddInt32(&clientCounter, -1)
	defer fmt.Println("Client", id, "disconnected")
	defer gameXO.ResetGame()
	defer func() {
		clientIDs[id] = false
	}()

	for true {

		//Wait for second player to connect
		for clientCounter < 2 {
			_, err := conn.Write([]byte{0})
			if err != nil {
				return
			}
			time.Sleep(time.Second)
		}

		//Send player information about symbol he is playing with and notifying him about game start
		_, err := conn.Write([]byte{byte(id)})
		if err != nil {
			if clientCounter == 2 {
				gameConcluded <- -1
			}
			return
		}

		//Randomizing who is going first with this wait(actually need to think about a better one)
		if id == 2 {
			waitTime := rand.Intn(700)
			time.Sleep(time.Duration(waitTime) * time.Millisecond)
		}

		//Play the game
		continuePlaying := true
		for continuePlaying {
			time.Sleep(time.Duration(100) * time.Millisecond)
			continuePlaying = clientMakesOneTurn(conn, id, mutex, gameConcluded)
		}

		//Write client the final state of the game
		_, err = conn.Write(append(gameXO.PlayingField, byte(gameXO.State)))
		if err != nil {
			return
		}

		//Ask player if he wants to play again and get his answer
		atomic.AddInt32(&clientCounter, -1)
		continuation := []byte{0}
		_, err = conn.Read(continuation)
		atomic.AddInt32(&clientCounter, 1)
		if err != nil {
			return
		}

		//Just for safety measure flush game state
		gameXO.ResetGame()
	}
}

//Sends info about game state to the client. Returns true if everything went okay and client is still there.
func sendGameStateToClient(conn net.Conn, gameConcluded chan int) bool {
	_, err := conn.Write(append(gameXO.PlayingField, byte(gameXO.State)))
	if err != nil {
		if clientCounter == 2 {
			gameConcluded <- -1
		}
		return false
	}
	return true
}

//Gets info about player's choice. Returns true if everything went okay and client is still there.
func getTurnFromClient(conn net.Conn, id int, gameConcluded chan int) bool {
	turn := make([]byte, 1)
	_, err := conn.Read(turn)
	if err != nil {
		if clientCounter == 2 {
			gameConcluded <- -1
		}
		return false
	}
	gameXO.PlayingField[int(turn[0])] = byte(id)
	return true
}

//Function that describes one player turn. Returns true if everything's alright and we need to go for another turn.
func clientMakesOneTurn(conn net.Conn, id int, mutex *sync.Mutex, gameConcluded chan int) bool {
	//Second player can't go while first is making turn
	mutex.Lock()
	defer mutex.Unlock()

	// Check for message from second client, maybe he disconnected or game finished
	select {
	case exitCode := <-gameConcluded:
		if exitCode == -1 {
			gameXO.State = game.DISCONNECTED
		}
		return false

	default:
		//Just proceed with turn
		if !sendGameStateToClient(conn, gameConcluded) {
			return false
		}
		if !getTurnFromClient(conn, id, gameConcluded) {
			return false
		}

		//Check game state, maybe game is already in finished condition
		gameXO.CheckState()
		if gameXO.State != game.GOINGON {
			if clientCounter == 2 {
				gameConcluded <- 1
			}
			return false
		}
	}
	return true
}

//This function is here just as preventive measure if we will need to actually get our server IP address.
func getOutboundIP() net.IP {
	conn, err := net.Dial("udp", "8.8.8.8:4040")
	if err != nil {
		log.Fatal(err)
		fmt.Println("Error while retrieving local ip address")
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)

	return localAddr.IP
}
