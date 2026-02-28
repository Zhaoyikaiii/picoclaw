// Package pico provides the Pico Protocol WebSocket channel implementation.
// This file contains the local PicoMessage type wrapper for compatibility with
// the canonical protocol definitions in pkg/pico/protocol.
package pico

import (
	"time"

	picoproto "github.com/sipeed/picoclaw/pkg/pico/protocol"
)

// nodeHeartbeatInterval is the interval between processing heartbeats sent
// during long-running node requests to keep the connection alive.
const nodeHeartbeatInterval = 15 * time.Second

// PicoMessage is the wire format for all Pico Protocol messages.
// This is a local type alias for the canonical protocol.Message type.
type PicoMessage = picoproto.Message

// newMessage creates a PicoMessage with the given type and payload.
func newMessage(msgType string, payload map[string]any) PicoMessage {
	return picoproto.NewMessage(msgType, payload)
}

// newError creates an error PicoMessage.
func newError(code, message string) PicoMessage {
	return picoproto.NewError(code, message)
}

// Message type constants re-exported from pkg/pico/protocol for convenience.
// The canonical definitions are in pkg/pico/protocol/protocol.go.
const (
	TypeMessageSend    = picoproto.TypeMessageSend
	TypeMediaSend      = picoproto.TypeMediaSend
	TypePing           = picoproto.TypePing
	TypeMessageCreate  = picoproto.TypeMessageCreate
	TypeMessageUpdate  = picoproto.TypeMessageUpdate
	TypeMediaCreate    = picoproto.TypeMediaCreate
	TypeTypingStart    = picoproto.TypeTypingStart
	TypeTypingStop     = picoproto.TypeTypingStop
	TypeError          = picoproto.TypeError
	TypePong           = picoproto.TypePong
	TypeNodeRequest    = picoproto.TypeNodeRequest
	TypeNodeReply      = picoproto.TypeNodeReply
	TypeNodeProcessing = picoproto.TypeNodeProcessing
)

// Ensure PicoMessage matches protocol.Message at compile time.
var _ = func() struct{} {
	// Type alias compatibility check
	type _ = picoproto.Message
	type _ = PicoMessage
	return struct{}{}
}()

// PicoMessageTimestamp returns the current timestamp for Pico messages.
func PicoMessageTimestamp() int64 {
	return time.Now().UnixMilli()
}
