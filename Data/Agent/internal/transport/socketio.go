package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Handler func(context.Context, any) (any, error)

type Client struct {
	baseURL     string
	headers     map[string]string
	handlers    map[string]Handler
	onConnected func(context.Context) error
	conn        *websocket.Conn
	writeMu     sync.Mutex
	handlerMu   sync.RWMutex
	stateMu     sync.RWMutex
	connected   bool
	timeout     time.Duration
}

const defaultNamespaceConnectTimeout = 45 * time.Second

func NewClient(baseURL string, headers map[string]string) *Client {
	copiedHeaders := map[string]string{}
	for key, value := range headers {
		copiedHeaders[key] = value
	}
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		headers:  copiedHeaders,
		handlers: map[string]Handler{},
		timeout:  defaultNamespaceConnectTimeout,
	}
}

func (c *Client) On(event string, handler Handler) {
	c.handlerMu.Lock()
	defer c.handlerMu.Unlock()
	c.handlers[event] = handler
}

func (c *Client) OnConnected(fn func(context.Context) error) {
	c.onConnected = fn
}

func (c *Client) SetConnectTimeout(timeout time.Duration) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.timeout = timeout
}

func (c *Client) Connect(ctx context.Context) error {
	wsURL, err := socketURL(c.baseURL)
	if err != nil {
		return err
	}
	header := http.Header{}
	for key, value := range c.headers {
		if strings.TrimSpace(value) != "" {
			header.Set(key, value)
		}
	}
	dialer := websocket.Dialer{HandshakeTimeout: 30 * time.Second}
	conn, _, err := dialer.DialContext(ctx, wsURL, header)
	if err != nil {
		return err
	}
	c.resetConnected()
	c.writeMu.Lock()
	c.conn = conn
	c.writeMu.Unlock()
	defer func() {
		c.writeMu.Lock()
		if c.conn == conn {
			c.conn = nil
		}
		c.writeMu.Unlock()
		_ = conn.Close()
	}()
	connectTimeout := c.connectTimeout()
	deadlineActive := false
	if connectTimeout > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(connectTimeout))
		deadlineActive = true
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		_, message, err := conn.ReadMessage()
		if err != nil {
			if deadlineActive && isTimeoutError(err) && !c.isConnected() {
				return fmt.Errorf("socket namespace connect timeout after %s", connectTimeout)
			}
			return err
		}
		if err := c.handleMessage(ctx, string(message)); err != nil {
			return err
		}
		if deadlineActive && c.isConnected() {
			_ = conn.SetReadDeadline(time.Time{})
			deadlineActive = false
		}
	}
}

func (c *Client) Emit(event string, payload any) error {
	packet, err := encodeEventPacket(event, payload)
	if err != nil {
		return err
	}
	return c.write(packet)
}

func (c *Client) write(message string) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.conn == nil {
		return fmt.Errorf("socket not connected")
	}
	return c.conn.WriteMessage(websocket.TextMessage, []byte(message))
}

func (c *Client) handleMessage(ctx context.Context, message string) error {
	if message == "" {
		return nil
	}
	switch message[0] {
	case '0':
		return c.write("40")
	case '2':
		return c.write("3")
	case '4':
		return c.handleSocketPacket(ctx, message[1:])
	default:
		return nil
	}
}

func (c *Client) handleSocketPacket(ctx context.Context, packet string) error {
	if packet == "" {
		return nil
	}
	switch packet[0] {
	case '0':
		if c.onConnected != nil {
			if err := c.onConnected(ctx); err != nil {
				return err
			}
		}
		c.markConnected()
	case '2':
		eventName, payload, ackID, err := parseEventPacket(packet[1:])
		if err != nil {
			return err
		}
		c.handlerMu.RLock()
		handler := c.handlers[eventName]
		c.handlerMu.RUnlock()
		if handler == nil {
			if ackID != "" {
				return c.write("43" + ackID + `[{"error":"unsupported_event"}]`)
			}
			return nil
		}
		response, handlerErr := handler(ctx, payload)
		if ackID != "" {
			if handlerErr != nil {
				response = map[string]any{"error": "handler_error", "message": handlerErr.Error()}
			}
			ackPayload, err := json.Marshal([]any{response})
			if err != nil {
				return err
			}
			return c.write("43" + ackID + string(ackPayload))
		}
		if handlerErr != nil {
			return handlerErr
		}
	}
	return nil
}

func (c *Client) resetConnected() {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.connected = false
}

func (c *Client) markConnected() {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.connected = true
}

func (c *Client) isConnected() bool {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.connected
}

func (c *Client) connectTimeout() time.Duration {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.timeout
}

func isTimeoutError(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func socketURL(baseURL string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported socket scheme %q", parsed.Scheme)
	}
	parsed.Path = "/socket.io/"
	parsed.RawQuery = "EIO=4&transport=websocket"
	return parsed.String(), nil
}

func encodeEventPacket(event string, payload any) (string, error) {
	body, err := json.Marshal([]any{event, payload})
	if err != nil {
		return "", err
	}
	return "42" + string(body), nil
}

func parseEventPacket(rest string) (string, any, string, error) {
	if strings.HasPrefix(rest, "/") {
		comma := strings.Index(rest, ",")
		if comma < 0 {
			return "", nil, "", fmt.Errorf("socket namespace packet missing comma")
		}
		rest = rest[comma+1:]
	}
	i := 0
	for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
		i++
	}
	ackID := rest[:i]
	jsonText := rest[i:]
	var values []any
	if err := json.Unmarshal([]byte(jsonText), &values); err != nil {
		return "", nil, "", err
	}
	if len(values) == 0 {
		return "", nil, ackID, fmt.Errorf("event packet missing event name")
	}
	eventName, ok := values[0].(string)
	if !ok || eventName == "" {
		return "", nil, ackID, fmt.Errorf("event name missing")
	}
	if len(values) == 1 {
		return eventName, nil, ackID, nil
	}
	return eventName, values[1], ackID, nil
}

func AckIDFromInt(value int64) string {
	if value <= 0 {
		return ""
	}
	return strconv.FormatInt(value, 10)
}
