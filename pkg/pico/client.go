package pico

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"github.com/sipeed/picoclaw/pkg/pico/protocol"
)

// DefaultReadTimeout is the maximum time to wait for a reply after sending.
// For inter-node communication, LLM responses may take longer than typical HTTP requests.
const DefaultReadTimeout = 2 * time.Minute

// Client is a lightweight, stateless Pico WebSocket client.
// Each SendRequest call dials a new connection, performs a single
// request-reply exchange, and closes the connection.
type Client struct {
	token string
}

// NewClient creates a new Pico WebSocket client.
// If token is non-empty it is sent as a Bearer token in the upgrade request.
func NewClient(token string) *Client {
	return &Client{token: token}
}

// BuildWSURL constructs the canonical Pico WebSocket URL for a given
// host address and session ID.
func BuildWSURL(addr, sessionID string) string {
	return fmt.Sprintf("ws://%s/pico/ws?session_id=%s", addr, sessionID)
}

// SendRequest dials the target Pico WebSocket endpoint, sends msg, and blocks
// until a single reply message is received (or the context / read timeout fires).
//
// The caller is responsible for constructing the outbound protocol.Message
// (including Type, ID, Payload, etc.) and for interpreting the reply.
//
// For long-running requests, the server may send TypeNodeProcessing messages
// to keep the connection alive. The client resets the read timeout on each
// processing message until the final reply is received.
func (c *Client) SendRequest(
	ctx context.Context,
	addr, sessionID string,
	msg protocol.Message,
) (protocol.Message, error) {
	wsURL := BuildWSURL(addr, sessionID)

	header := http.Header{}
	if c.token != "" {
		header.Set("Authorization", "Bearer "+c.token)
	}

	dialer := websocket.Dialer{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}

	conn, resp, err := dialer.DialContext(ctx, wsURL, header)
	if err != nil {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
			return protocol.Message{}, fmt.Errorf(
				"pico WebSocket dial failed: %s (status: %d)",
				err.Error(), resp.StatusCode,
			)
		}
		return protocol.Message{}, fmt.Errorf("pico WebSocket dial failed: %w", err)
	}
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	defer conn.Close()

	if writeErr := conn.WriteJSON(msg); writeErr != nil {
		return protocol.Message{}, fmt.Errorf("failed to send pico request: %w", writeErr)
	}

	// Read loop that handles processing heartbeat messages
	for {
		// Set read deadline before each read
		conn.SetReadDeadline(time.Now().Add(DefaultReadTimeout))

		_, rawMsg, err := conn.ReadMessage()
		if err != nil {
			return protocol.Message{}, fmt.Errorf("failed to read pico reply: %w", err)
		}

		var reply protocol.Message
		if err := json.Unmarshal(rawMsg, &reply); err != nil {
			return protocol.Message{}, fmt.Errorf("failed to parse pico reply: %w", err)
		}

		// Handle processing heartbeat - reset timeout and continue waiting
		if reply.Type == protocol.TypeNodeProcessing {
			continue
		}

		// Return on final reply or error
		return reply, nil
	}
}
