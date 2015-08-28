// Copyright (c) 2013 Joshua Elliott
// Released under the MIT License
// http://opensource.org/licenses/MIT

package turnpike

import (
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	log "github.com/ivahaev/go-logger"
	"github.com/nu7hatch/gouuid"
	"io"
	"net"
	"regexp"
	"sync"
	"time"
)

var (
	// The amount of messages to buffer before sending to client.
	serverBacklog = 20
)

const (
	clientConnTimeout = 6
	clientMaxFailures = 3
)

// Server represents a WAMP server that handles RPC and pub/sub.
type Server struct {
	clients              map[string]chan string
	prefixes             map[string]prefixMap
	rpcHandlers          map[string]RPCHandler
	rgxRpcHandlers       map[string]RgxRPCHandler
	subHandlers          map[string]SubHandler
	pubHandlers          map[string]PubHandler
	subscriptions        map[string]listenerMap
	subLock              *sync.Mutex
	sessionOpenCallback  func(string, interface{})
	sessionCloseCallback func(string)
	disconnectChannels   map[string]chan bool
}

// RPCHandler is an interface that handlers to RPC calls should implement.
// The first parameter is the call ID, the second is the proc URI. Last comes
// all optional arguments to the RPC call. The return can be of any type that
// can be marshaled to JSON, or a error (preferably RPCError but any error works.)
// NOTE: this may be broken in v2 if multiple-return is implemented
type RPCHandler func(clientID string, topicURI string, args ...interface{}) (interface{}, error)

type RgxRPCHandler struct {
	Rgx     *regexp.Regexp
	Handler RPCHandler
}

// RPCError represents a call error and is the recommended way to return an
// error from a RPC handler.
type RPCError struct {
	URI         string
	Description string
	Details     interface{}
}

// Error returns an error description.
func (e RPCError) Error() string {
	return fmt.Sprintf("turnpike: RPC error with URI %s: %s", e.URI, e.Description)
}

// SubHandler is an interface that handlers for subscriptions should implement to
// control with subscriptions are valid. A subscription is allowed by returning
// true or denied by returning false.
type SubHandler func(clientID string, topicURI string) bool

// PubHandler is an interface that handlers for publishes should implement to
// get notified on a client publish with the possibility to modify the event.
// The event that will be published should be returned.
type PubHandler func(topicURI string, event interface{}) interface{}

// NewServer creates a new WAMP server.
// Can receive bool params for debug. If true, will log debug messages.
func NewServer(d ...bool) *Server {
	s := &Server{
		clients:            make(map[string]chan string),
		prefixes:           make(map[string]prefixMap),
		rpcHandlers:        make(map[string]RPCHandler),
		rgxRpcHandlers:     make(map[string]RgxRPCHandler),
		subHandlers:        make(map[string]SubHandler),
		pubHandlers:        make(map[string]PubHandler),
		subscriptions:      make(map[string]listenerMap),
		subLock:            new(sync.Mutex),
		disconnectChannels: make(map[string]chan bool),
	}
	if len(d) > 0 {
		debug = d[0]
	}
	return s
}

// SetSessionCloseCallback adds a callback function that is run when a session ends.
// The callback function must accept a string argument that is the session ID.
func (t *Server) DisconnectClient(client string) {
	log.Debug("Disconnect request", client)
	ch, ok := t.disconnectChannels[client]
	if !ok {
		log.Error("Can't find client", client)
	}
	ch <- true
}

// SetSessionCloseCallback adds a callback function that is run when a session ends.
// The callback function must accept a string argument that is the session ID.
func (t *Server) SetSessionCloseCallback(f func(string)) {
	t.sessionCloseCallback = f
}

// SetSessionOpenCallback adds a callback function that is run when a new session begins.
// The callback function must accept a string argument that is the session ID
// and the interface{} argument with additional info
func (t *Server) SetSessionOpenCallback(f func(string, interface{})) {
	t.sessionOpenCallback = f
}

// RegisterRPC adds a handler for the RPC named uri.
func (t *Server) RegisterRPC(uri string, f RPCHandler) {
	if f != nil {
		t.rpcHandlers[uri] = f
	}
}

// RegisterRgxRPC adds a handler for the RPC in regexp uri.
func (t *Server) RegisterRgxRPC(rgx *regexp.Regexp, f RPCHandler) {
	if f != nil {
		t.rgxRpcHandlers[rgx.String()] = RgxRPCHandler{rgx, f}
	}
}

// UnregisterRPC removes a handler for the RPC named uri.
func (t *Server) UnregisterRPC(uri string) {
	delete(t.rpcHandlers, uri)
}

// UnregisterRgxRPC removes a handler for the RPC in regexp uri.
func (t *Server) UnregisterRgxRPC(rgx *regexp.Regexp) {
	delete(t.rgxRpcHandlers, rgx.String())
}

// RegisterSubHandler adds a handler called when a client subscribes to URI.
// The subscription can be canceled in the handler by returning false, or
// approved by returning true.
func (t *Server) RegisterSubHandler(uri string, f SubHandler) {
	if f != nil {
		t.subHandlers[uri] = f
	}
}

// UnregisterSubHandler removes a subscription handler for the URI.
func (t *Server) UnregisterSubHandler(uri string) {
	delete(t.subHandlers, uri)
}

// RegisterPubHandler adds a handler called when a client publishes to URI.
// The event can be modified in the handler and the returned event is what is
// published to the other clients.
func (t *Server) RegisterPubHandler(uri string, f PubHandler) {
	if f != nil {
		t.pubHandlers[uri] = f
	}
}

// Subscribers returns a slice of the ids of all subscribed to uri clients
func (t *Server) Subscribers(uri string) []string {
	clientIDs := []string{}
	lm, ok := t.subscriptions[uri]
	if !ok {
		return clientIDs
	}
	for id, _ := range lm {
		clientIDs = append(clientIDs, id)
	}
	return clientIDs
}

// UnregisterPubHandler removes a publish handler for the URI.
func (t *Server) UnregisterPubHandler(uri string) {
	delete(t.pubHandlers, uri)
}

// SendEvent sends an event with topic directly (not via Client.Publish())
func (t *Server) SendEvent(topic string, event interface{}, ids ...[]string) {
	var eligibleList = []string{}
	var excludeList = []string{}
	if len(ids) > 0 && ids[0] != nil {
		eligibleList = ids[0]
	}
	if len(ids) > 1 && ids[1] != nil {
		excludeList = ids[1]
	}
	t.handlePublish(topic, publishMsg{
		TopicURI:     topic,
		Event:        event,
		EligibleList: eligibleList,
		ExcludeList:  excludeList,
	})
}

// ConnectedClients returns a slice of the ids of all connected clients
func (t *Server) ConnectedClients() []string {
	clientIDs := []string{}
	for id, _ := range t.clients {
		clientIDs = append(clientIDs, id)
	}
	return clientIDs
}

// HandleWebsocket implements the go.net/websocket.Handler interface.
func (t *Server) HandleWebsocket(conn *websocket.Conn, additionalData interface{}) {
	defer conn.Close()

	if debug {
		log.Info("turnpike: received websocket connection")
	}

	tid, err := uuid.NewV4()
	if err != nil {
		if debug {
			log.Info("turnpike: could not create unique id, refusing client connection")
		}
		return
	}
	id := tid.String()
	if debug {
		log.Info("turnpike: client connected:", id)
	}

	t.disconnectChannels[id] = make(chan bool)

	arr, err := createWelcome(id, turnpikeServerIdent)
	if err != nil {
		if debug {
			log.Info("turnpike: error encoding welcome message")
		}
		return
	}
	if debug {
		log.Info("turnpike: sending welcome message:", arr)
	}
	err = conn.WriteMessage(websocket.TextMessage, []byte(arr))
	if err != nil {
		if debug {
			log.Info("turnpike: error sending welcome message, aborting connection:", err)
		}
		return
	}

	c := make(chan string, serverBacklog)
	t.clients[id] = c

	if t.sessionOpenCallback != nil {
		t.sessionOpenCallback(id, additionalData)
	}
	failures := 0
	go func() {
	Loop:
		for {
			select {
			case msg := <-c:
				if debug {
					log.Info("turnpike: sending message:", msg)
				}
				conn.SetWriteDeadline(time.Now().Add(clientConnTimeout * time.Second))
				err := conn.WriteMessage(websocket.TextMessage, []byte(msg))
				if err != nil {
					if nErr, ok := err.(net.Error); ok && (nErr.Timeout() || nErr.Temporary()) {
						log.Info("Network error:", nErr.Error())
						failures++
						if failures > clientMaxFailures {
							break Loop
						}
					} else {
						if debug {
							log.Info("turnpike: error sending message:", err.Error())
						}
						break Loop
					}
				}

			case <-t.disconnectChannels[id]:
				if debug {
					log.Info("Need to disconnect wamp connection")
				}
				break Loop
			}
		}
		if debug {
			log.Info("Client disconnected", id)
		}
		conn.Close()
	}()

	for {
		var rec string
		_, msg, err := conn.ReadMessage()
		if err != nil {
			if err != io.EOF {
				if debug {
					log.Info("turnpike: error receiving message, aborting connection:", err.Error())
				}
			}
			break
		}
		if debug {
			log.Info("turnpike: message received:", rec)
		}
		rec = string(msg)

		data := []byte(rec)

		switch typ := parseMessageType(rec); typ {
		case msgPrefix:
			var msg prefixMsg
			err := json.Unmarshal(data, &msg)
			if err != nil {
				if debug {
					log.Info("turnpike: error unmarshalling prefix message:", err.Error())
				}
				continue
			}
			t.handlePrefix(id, msg)
		case msgCall:
			var msg callMsg
			err := json.Unmarshal(data, &msg)
			if err != nil {
				if debug {
					log.Info("turnpike: error unmarshalling call message:", err.Error())
				}
				continue
			}
			go t.handleCall(id, msg)
		case msgSubscribe:
			var msg subscribeMsg
			err := json.Unmarshal(data, &msg)
			if err != nil {
				if debug {
					log.Info("turnpike: error unmarshalling subscribe message:", err.Error())
				}
				continue
			}
			t.handleSubscribe(id, msg)
		case msgUnsubscribe:
			var msg unsubscribeMsg
			err := json.Unmarshal(data, &msg)
			if err != nil {
				if debug {
					log.Info("turnpike: error unmarshalling unsubscribe message:", err.Error())
				}
				continue
			}
			t.handleUnsubscribe(id, msg)
		case msgPublish:
			var msg publishMsg
			err := json.Unmarshal(data, &msg)
			if err != nil {
				if debug {
					log.Info("turnpike: error unmarshalling publish message", err)
				}
				continue
			}
			t.handlePublish(id, msg)
		case msgWelcome, msgCallResult, msgCallError, msgEvent:
			if debug {
				log.Info("turnpike: server -> client message received, ignored", messageTypeString(typ))
			}
		case msgHeartbeat:
			if debug {
				log.Info("turnpike: client -> server heartbeat message received")
			}
			var msg []interface{}
			err := json.Unmarshal(data, &msg)
			if err != nil {
				if debug {
					log.Info("turnpike: error unmarshalling call message:", err.Error())
				}
				continue
			}
			t.handleHeartbeat(id, msg)
		default:
			if debug {
				log.Info("turnpike: client heartbeat message: ", data)
			}
		}
	}

	delete(t.clients, id)
	close(c)
	if t.sessionCloseCallback != nil {
		t.sessionCloseCallback(id)
	}
}

func (t *Server) handleHeartbeat(tid string, msg []interface{}) {
	counter := int(msg[1].(float64))
	out, err := createHeartbeatEvent(counter)
	if err != nil {
		if debug {
			log.Info("turnpike: error creating hearbeat message:", err.Error())
		}
		return
	}
	if client, ok := t.clients[tid]; ok {
		if len(client) == cap(client) {
			<-client
		}
		client <- string(out)
	}
}

func (t *Server) handlePrefix(id string, msg prefixMsg) {
	if debug {
		log.Info("turnpike: handling prefix message")
	}
	if _, ok := t.prefixes[id]; !ok {
		t.prefixes[id] = make(prefixMap)
	}
	if err := t.prefixes[id].registerPrefix(msg.Prefix, msg.URI); err != nil {
		if debug {
			log.Info("turnpike: error registering prefix:", err.Error())
		}
	}
	if debug {
		log.Info("turnpike: client registered prefix  for URI:", id, msg.Prefix, msg.URI)
	}
}

func (t *Server) handleCall(id string, msg callMsg) {
	if debug {
		log.Info("turnpike: handling call message", msg)
	}

	var out string
	var err error

	if f, ok := t.rpcHandlers[msg.ProcURI]; ok && f != nil {
		var res interface{}
		res, err = f(id, msg.ProcURI, msg.CallArgs...)
		if err != nil {
			var errorURI, desc string
			var details interface{}
			if er, ok := err.(RPCError); ok {
				errorURI = er.URI
				desc = er.Description
				details = er.Details
			} else {
				errorURI = msg.ProcURI + "#generic-error"
				desc = err.Error()
			}

			if details != nil {
				out, err = createCallError(msg.CallID, errorURI, desc, details)
			} else {
				out, err = createCallError(msg.CallID, errorURI, desc)
			}
		} else {
			out, err = createCallResult(msg.CallID, res)
		}
	} else {
		var found bool
		for _, rgxHandler := range t.rgxRpcHandlers {
			if rgxHandler.Rgx.MatchString(msg.ProcURI) {
				found = true
				var res interface{}
				res, err = rgxHandler.Handler(id, msg.ProcURI, msg.CallArgs...)
				if err != nil {
					var errorURI, desc string
					var details interface{}
					if er, ok := err.(RPCError); ok {
						errorURI = er.URI
						desc = er.Description
						details = er.Details
					} else {
						errorURI = msg.ProcURI + "#generic-error"
						desc = err.Error()
					}

					if details != nil {
						out, err = createCallError(msg.CallID, errorURI, desc, details)
					} else {
						out, err = createCallError(msg.CallID, errorURI, desc)
					}
				} else {
					out, err = createCallResult(msg.CallID, res)
				}
				break
			}
		}
		if !found {
			if debug {
				log.Info("turnpike: RPC call not registered:", msg.ProcURI)
			}
			out, err = createCallError(msg.CallID, "error:notimplemented", "RPC call not implemented", msg.ProcURI)
		}
	}

	if err != nil {
		// whatever, let the client hang...
		if debug {
			log.Info("turnpike: error creating callError message:", err.Error())
		}
		return
	}
	if client, ok := t.clients[id]; ok {
		client <- out
	}
}

func (t *Server) handleSubscribe(id string, msg subscribeMsg) {
	if debug {
		log.Info("turnpike: handling subscribe message")
	}

	uri := checkCurie(t.prefixes[id], msg.TopicURI)
	h := t.getSubHandler(uri)
	if h != nil && !h(id, uri) {
		if debug {
			log.Info("turnpike: client denied subscription of topic:", id, uri)
		}
		if out, err := createWAMPMessage(msgSubscribeError, msg.TopicURI); err == nil {
			if client, ok := t.clients[id]; ok {
				client <- out
			}
		}
		return
	}

	t.subLock.Lock()
	defer t.subLock.Unlock()
	if _, ok := t.subscriptions[uri]; !ok {
		t.subscriptions[uri] = make(map[string]bool)
	}
	t.subscriptions[uri].add(id)
	if debug {
		log.Info("turnpike: client subscribed to topic:", id, uri)
	}
	if out, err := createWAMPMessage(msgSubscribed, msg.TopicURI); err == nil {
		if client, ok := t.clients[id]; ok {
			client <- out
		}
	}
}

func (t *Server) handleUnsubscribe(id string, msg unsubscribeMsg) {
	if debug {
		log.Info("turnpike: handling unsubscribe message")
	}
	t.subLock.Lock()
	uri := checkCurie(t.prefixes[id], msg.TopicURI)
	if lm, ok := t.subscriptions[uri]; ok {
		lm.remove(id)
	}
	t.subLock.Unlock()
	if debug {
		log.Info("turnpike: client unsubscribed from topic:", id, uri)
	}
}

func (t *Server) handlePublish(id string, msg publishMsg) {
	if debug {
		log.Info("turnpike: handling publish message")
	}
	uri := checkCurie(t.prefixes[id], msg.TopicURI)

	h := t.getPubHandler(uri)
	event := msg.Event
	if h != nil {
		event = h(uri, event)
	}

	lm, ok := t.subscriptions[uri]
	if !ok {
		return
	}

	out, err := createEvent(uri, event)
	if err != nil {
		if debug {
			log.Info("turnpike: error creating event message:", err.Error())
		}
		return
	}

	var sendTo []string
	if len(msg.ExcludeList) > 0 || len(msg.EligibleList) > 0 {
		// this is super ugly, but I couldn't think of a better way...
		_sendTo := []string{}
		if len(msg.EligibleList) == 0 {
			for _tid := range lm {
				_sendTo = append(_sendTo, _tid)
			}
		}
		for _, tid := range msg.EligibleList {
			for _tid := range lm {
				if _tid == tid {
					_sendTo = append(_sendTo, tid)
					break
				}
			}
		}

		if len(msg.ExcludeList) == 0 {
			sendTo = _sendTo
		} else {
			for _, tid := range _sendTo {
				var exclude bool
				for _, _tid := range msg.ExcludeList {
					if tid == _tid {
						exclude = true
						break
					}
				}
				if !exclude {
					sendTo = append(sendTo, tid)
				}
			}
		}
	} else {
		for tid := range lm {
			if tid == id && msg.ExcludeMe {
				continue
			}
			sendTo = append(sendTo, tid)
		}
	}

	for _, tid := range sendTo {
		// we're not locking anything, so we need
		// to make sure the client didn't disconnecct in the
		// last few nanoseconds...
		if client, ok := t.clients[tid]; ok {
			if len(client) == cap(client) {
				<-client
			}
			client <- string(out)
		}
	}
}

func (t *Server) getSubHandler(uri string) SubHandler {
	for i := len(uri); i >= 0; i-- {
		u := uri[:i]
		if h, ok := t.subHandlers[u]; ok {
			return h
		}
	}
	return nil
}

func (t *Server) getPubHandler(uri string) PubHandler {
	for i := len(uri); i >= 0; i-- {
		u := uri[:i]
		if h, ok := t.pubHandlers[u]; ok {
			return h
		}
	}
	return nil
}

type listenerMap map[string]bool

func (lm listenerMap) add(id string) {
	lm[id] = true
}
func (lm listenerMap) contains(id string) bool {
	return lm[id]
}
func (lm listenerMap) remove(id string) {
	delete(lm, id)
}

//func checkWAMPHandshake(config *websocket.Config, req *http.Request) error {
//	for _, protocol := range config.Protocol {
//		if protocol == "wamp" {
//			config.Protocol = []string{protocol}
//			return nil
//		}
//	}
//	return websocket.ErrBadWebSocketProtocol
//}
