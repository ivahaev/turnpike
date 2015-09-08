// Copyright (c) 2013 Joshua Elliott
// 2015 ivahaev
// Released under the MIT License
// http://opensource.org/licenses/MIT

// Package turnpike provides a Websocket Application Messaging Protocol (WAMP) server and client
package turnpike
//
//import (
//    "code.google.com/p/gfuncsfuno.net/websocket"
//)

const (
    turnpikeVersion = "0.2.2"
    turnpikeServerIdent = "turnpike-" + turnpikeVersion
)

var debug = false
//
//// Handler is a interface to support Go1.0.
//type Handler interface {
//    HandleWebsocket(*websocket.Conn)
//}
//
//// HandleWebsocket is a Go1.0 shim for method values.
//func HandleWebsocket(t Handler) func(*websocket.Conn) {
//    return func(conn *websocket.Conn) {
//        t.HandleWebsocket(conn)
//    }
//}
