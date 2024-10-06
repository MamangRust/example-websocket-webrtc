package main

import (
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "strings"

    "github.com/go-chi/chi/v5"
    "github.com/go-chi/chi/v5/middleware"
    "github.com/gorilla/websocket"
)

const (
    MESSAGE_NEW_USER    = "New User"
    MESSAGE_CHAT        = "Chat"
    MESSAGE_LEAVE       = "Leave"
    MESSAGE_WEBRTC      = "webrtc_signal"
    MESSAGE_START_CALL  = "start_room_call"
    MESSAGE_END_CALL    = "end_room_call"
)

type SocketPayload struct {
    Type    string      `json:"type"`
    Message string      `json:"message"`
    To      string      `json:"to"`
    Signal  interface{} `json:"signal,omitempty"`
}

type SocketResponse struct {
    From    string      `json:"from"`
    Type    string      `json:"type"`
    Message string      `json:"message,omitempty"`
    Signal  interface{} `json:"signal,omitempty"`
}

type WebSocketConnection struct {
    *websocket.Conn
    Username string
}

var connections = make(map[string]*WebSocketConnection)
var rooms = make(map[string][]*WebSocketConnection)

var upgrader = websocket.Upgrader{
    ReadBufferSize:  1024,
    WriteBufferSize: 1024,
    CheckOrigin:     func(r *http.Request) bool { return true },
}

func main() {
    r := chi.NewRouter()
    r.Use(middleware.Logger)
    r.Use(middleware.Recoverer)

    r.Get("/", serveHomePage)
    r.Get("/ws", serveWebSocket)
    r.Get("/search", handleSearch)
    r.Post("/create", handleCreate)

    fmt.Println("Server starting at :8080")
    http.ListenAndServe(":8080", r)
}

func serveHomePage(w http.ResponseWriter, r *http.Request) {
    http.ServeFile(w, r, "index.html")
}

func serveWebSocket(w http.ResponseWriter, r *http.Request) {
    conn, err := upgrader.Upgrade(w, r, nil)
    if err != nil {
        log.Println(err)
        return
    }

    username := r.URL.Query().Get("username")
    if username == "" {
        log.Println("Username is required")
        return
    }

    currentConn := &WebSocketConnection{Conn: conn, Username: username}
    connections[username] = currentConn

    go handleIO(currentConn)
}

func handleIO(conn *WebSocketConnection) {
    defer func() {
        conn.Close()
        delete(connections, conn.Username)

        for roomName, roomMembers := range rooms {
            for i, member := range roomMembers {
                if member == conn {
                    rooms[roomName] = append(roomMembers[:i], roomMembers[i+1:]...)
                    break
                }
            }
        }
    }()

    broadcastMessage(MESSAGE_NEW_USER, fmt.Sprintf("%s has joined the chat", conn.Username))

    for {
        payload := SocketPayload{}
        err := conn.ReadJSON(&payload)
        if err != nil {
            if strings.Contains(err.Error(), "websocket: close") {
                return
            }
            log.Println("ERROR", err.Error())
            continue
        }

        switch payload.Type {
        case "direct":
            sendDirectMessage(conn, payload.To, payload.Message)
        case "room":
            sendRoomMessage(conn, payload.To, payload.Message)
        case "join":
            joinRoom(conn, payload.To)
        case "leave":
            leaveRoom(conn, payload.To)
        case MESSAGE_WEBRTC:
            handleWebRTCSignal(conn, payload)
        case MESSAGE_START_CALL:
            startRoomCall(conn, payload.To)
        case MESSAGE_END_CALL:
            endRoomCall(conn, payload.To)
        }
    }
}

func broadcastMessage(messageType, message string) {
    for _, conn := range connections {
        conn.WriteJSON(SocketResponse{
            From:    "System",
            Type:    messageType,
            Message: message,
        })
    }
}

func sendDirectMessage(from *WebSocketConnection, to, message string) {
    if toConn, ok := connections[to]; ok {
        toConn.WriteJSON(SocketResponse{
            From:    from.Username,
            Type:    MESSAGE_CHAT,
            Message: message,
        })
    }
}

func sendRoomMessage(from *WebSocketConnection, roomName, message string) {
    if roomMembers, ok := rooms[roomName]; ok {
        for _, member := range roomMembers {
            if member != from {
                member.WriteJSON(SocketResponse{
                    From:    from.Username,
                    Type:    MESSAGE_CHAT,
                    Message: message,
                })
            }
        }
    }
}

func joinRoom(conn *WebSocketConnection, roomName string) {
    rooms[roomName] = append(rooms[roomName], conn)
    for _, member := range rooms[roomName] {
        if member != conn {
            member.WriteJSON(SocketResponse{
                From:    conn.Username,
                Type:    MESSAGE_NEW_USER,
                Message: fmt.Sprintf("%s joined the room", conn.Username),
            })
        }
    }
}

func leaveRoom(conn *WebSocketConnection, roomName string) {
    if roomMembers, ok := rooms[roomName]; ok {
        for i, member := range roomMembers {
            if member == conn {
                rooms[roomName] = append(roomMembers[:i], roomMembers[i+1:]...)
                break
            }
        }
        for _, member := range rooms[roomName] {
            member.WriteJSON(SocketResponse{
                From:    conn.Username,
                Type:    MESSAGE_LEAVE,
                Message: fmt.Sprintf("%s left the room", conn.Username),
            })
        }
    }
}

func handleWebRTCSignal(from *WebSocketConnection, payload SocketPayload) {
    if toConn, ok := connections[payload.To]; ok {
        toConn.WriteJSON(SocketResponse{
            From:   from.Username,
            Type:   MESSAGE_WEBRTC,
            Signal: payload.Signal,
        })
    }
}

func startRoomCall(from *WebSocketConnection, roomName string) {
    if roomMembers, ok := rooms[roomName]; ok {
        for _, member := range roomMembers {
            if member != from {
                member.WriteJSON(SocketResponse{
                    From:    from.Username,
                    Type:    MESSAGE_START_CALL,
                    Message: fmt.Sprintf("%s started a call in the room", from.Username),
                })
            }
        }
    }
}

func endRoomCall(from *WebSocketConnection, roomName string) {
    if roomMembers, ok := rooms[roomName]; ok {
        for _, member := range roomMembers {
            if member != from {
                member.WriteJSON(SocketResponse{
                    From:    from.Username,
                    Type:    MESSAGE_END_CALL,
                    Message: fmt.Sprintf("%s ended the call in the room", from.Username),
                })
            }
        }
    }
}

func handleSearch(w http.ResponseWriter, r *http.Request) {
    query := r.URL.Query().Get("q")
    searchType := r.URL.Query().Get("type")

    var results []string

    if searchType == "user" {
        for username := range connections {
            if strings.Contains(strings.ToLower(username), strings.ToLower(query)) {
                results = append(results, username)
            }
        }
    } else if searchType == "room" {
        for roomName := range rooms {
            if strings.Contains(strings.ToLower(roomName), strings.ToLower(query)) {
                results = append(results, roomName)
            }
        }
    }

    json.NewEncoder(w).Encode(results)
}

func handleCreate(w http.ResponseWriter, r *http.Request) {
    var data struct {
        Type string `json:"type"`
        Name string `json:"name"`
    }
    json.NewDecoder(r.Body).Decode(&data)

    if data.Type == "room" {
        if _, exists := rooms[data.Name]; !exists {
            rooms[data.Name] = []*WebSocketConnection{}
            w.WriteHeader(http.StatusCreated)
            json.NewEncoder(w).Encode(map[string]string{"message": "Room created successfully"})
        } else {
            w.WriteHeader(http.StatusConflict)
            json.NewEncoder(w).Encode(map[string]string{"error": "Room already exists"})
        }
    } else {
        w.WriteHeader(http.StatusBadRequest)
        json.NewEncoder(w).Encode(map[string]string{"error": "Invalid create type"})
    }
}
