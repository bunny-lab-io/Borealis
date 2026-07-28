package transport

import (
	"context"
	"crypto/tls"
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

type AfterAckResponse struct {
	payload any
	after   func()
}

func NewAfterAckResponse(payload any, after func()) AfterAckResponse {
	return AfterAckResponse{payload: payload, after: after}
}

func (r AfterAckResponse) AckPayload() any {
	return r.payload
}

func (r AfterAckResponse) AfterAck() {
	if r.after != nil {
		r.after()
	}
}

type Client struct {
	baseURL     string
	headers     map[string]string
	handlers    map[string]Handler
	onConnected func(context.Context) error
	onActivity  func()
	conn        *websocket.Conn
	writeMu     sync.Mutex
	handlerMu   sync.RWMutex
	stateMu     sync.RWMutex
	connected   bool
	asyncErrMu  sync.Mutex
	asyncErr    error
	ackMu       sync.Mutex
	nextAckID   int64
	pendingAcks map[string]chan ackResult
	timeout     time.Duration
	readIdle    time.Duration
	tlsConfig   *tls.Config
	dialContext func(context.Context, string, string) (net.Conn, error)
}

const (
	defaultNamespaceConnectTimeout = 45 * time.Second
	defaultReadIdleTimeout         = 60 * time.Second
)

type ackResult struct {
	payload []any
	err     error
}

type Option func(*Client)

func WithTLSConfig(tlsConfig *tls.Config) Option {
	return func(c *Client) {
		if tlsConfig != nil {
			c.tlsConfig = tlsConfig.Clone()
		}
	}
}

func WithDialContext(dialContext func(context.Context, string, string) (net.Conn, error)) Option {
	return func(c *Client) {
		if dialContext != nil {
			c.dialContext = dialContext
		}
	}
}

func NewClient(baseURL string, headers map[string]string, opts ...Option) *Client {
	copiedHeaders := map[string]string{}
	for key, value := range headers {
		copiedHeaders[key] = value
	}
	client := &Client{
		baseURL:     strings.TrimRight(baseURL, "/"),
		headers:     copiedHeaders,
		handlers:    map[string]Handler{},
		pendingAcks: map[string]chan ackResult{},
		timeout:     defaultNamespaceConnectTimeout,
		readIdle:    defaultReadIdleTimeout,
	}
	for _, opt := range opts {
		opt(client)
	}
	return client
}

func (c *Client) On(event string, handler Handler) {
	c.handlerMu.Lock()
	defer c.handlerMu.Unlock()
	c.handlers[event] = handler
}

func (c *Client) OnConnected(fn func(context.Context) error) {
	c.onConnected = fn
}

func (c *Client) OnActivity(fn func()) {
	c.onActivity = fn
}

func (c *Client) SetConnectTimeout(timeout time.Duration) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.timeout = timeout
}

func (c *Client) SetReadIdleTimeout(timeout time.Duration) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.readIdle = timeout
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
	if c.tlsConfig != nil {
		dialer.TLSClientConfig = c.tlsConfig.Clone()
	}
	if c.dialContext != nil {
		dialer.NetDialContext = c.dialContext
	}
	conn, _, err := dialer.DialContext(ctx, wsURL, header)
	if err != nil {
		return err
	}
	c.resetConnected()
	c.resetAsyncError()
	c.writeMu.Lock()
	c.conn = conn
	c.writeMu.Unlock()
	defer func() {
		c.writeMu.Lock()
		if c.conn == conn {
			c.conn = nil
		}
		c.writeMu.Unlock()
		c.failPendingAcks(fmt.Errorf("socket disconnected"))
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
			if asyncErr := c.takeAsyncError(); asyncErr != nil {
				return asyncErr
			}
			if deadlineActive && isTimeoutError(err) && !c.isConnected() {
				return fmt.Errorf("socket namespace connect timeout after %s", connectTimeout)
			}
			return err
		}
		if err := c.handleMessage(ctx, string(message)); err != nil {
			return err
		}
		c.recordActivity()
		if c.isConnected() {
			if readIdle := c.readIdleTimeout(); readIdle > 0 {
				_ = conn.SetReadDeadline(time.Now().Add(readIdle))
			} else if deadlineActive {
				_ = conn.SetReadDeadline(time.Time{})
			}
			deadlineActive = false
		}
	}
}

func (c *Client) recordActivity() {
	if c.onActivity != nil {
		c.onActivity()
	}
}

func (c *Client) Emit(event string, payload any) error {
	packet, err := encodeEventPacket(event, payload)
	if err != nil {
		return err
	}
	return c.write(packet)
}

func (c *Client) EmitWithAck(ctx context.Context, event string, payload any, timeout time.Duration) ([]any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	ackID, ackCh := c.registerPendingAck()
	packet, err := encodeEventPacketWithAck(event, payload, ackID)
	if err != nil {
		c.removePendingAck(ackID)
		return nil, err
	}
	if err := c.write(packet); err != nil {
		c.removePendingAck(ackID)
		return nil, err
	}
	select {
	case result := <-ackCh:
		return result.payload, result.err
	case <-ctx.Done():
		c.removePendingAck(ackID)
		return nil, ctx.Err()
	}
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
		c.markConnected()
		if c.onConnected != nil {
			go c.runConnectedCallback(ctx)
		}
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
		go c.dispatchEvent(ctx, handler, payload, ackID)
	case '3':
		if err := c.handleAckPacket(packet[1:]); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) runConnectedCallback(ctx context.Context) {
	if c.onConnected == nil {
		return
	}
	if err := c.onConnected(ctx); err != nil {
		c.setAsyncError(err)
		c.closeCurrent()
	}
}

func (c *Client) dispatchEvent(ctx context.Context, handler Handler, payload any, ackID string) {
	var response any
	var handlerErr error
	defer func() {
		if recovered := recover(); recovered != nil && ackID != "" {
			_ = c.write("43" + ackID + `[{"error":"handler_panic"}]`)
		}
	}()
	response, handlerErr = handler(ctx, payload)
	afterAck := afterAckCallback(response)
	response = ackPayload(response)
	if ackID == "" {
		if handlerErr == nil && afterAck != nil {
			go afterAck()
		}
		return
	}
	if handlerErr != nil {
		response = map[string]any{"error": "handler_error", "message": handlerErr.Error()}
		afterAck = nil
	}
	ackPayload, err := json.Marshal([]any{response})
	if err != nil {
		ackPayload = []byte(`[{"error":"handler_ack_encode_failed"}]`)
	}
	if err := c.write("43" + ackID + string(ackPayload)); err != nil {
		return
	}
	if afterAck != nil {
		go afterAck()
	}
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

func (c *Client) readIdleTimeout() time.Duration {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.readIdle
}

func (c *Client) resetAsyncError() {
	c.asyncErrMu.Lock()
	defer c.asyncErrMu.Unlock()
	c.asyncErr = nil
}

func (c *Client) setAsyncError(err error) {
	if err == nil {
		return
	}
	c.asyncErrMu.Lock()
	defer c.asyncErrMu.Unlock()
	c.asyncErr = err
}

func (c *Client) takeAsyncError() error {
	c.asyncErrMu.Lock()
	defer c.asyncErrMu.Unlock()
	err := c.asyncErr
	c.asyncErr = nil
	return err
}

func (c *Client) closeCurrent() {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.conn != nil {
		_ = c.conn.Close()
	}
}

func (c *Client) registerPendingAck() (string, chan ackResult) {
	c.ackMu.Lock()
	defer c.ackMu.Unlock()
	c.nextAckID++
	ackID := strconv.FormatInt(c.nextAckID, 10)
	ackCh := make(chan ackResult, 1)
	if c.pendingAcks == nil {
		c.pendingAcks = map[string]chan ackResult{}
	}
	c.pendingAcks[ackID] = ackCh
	return ackID, ackCh
}

func (c *Client) removePendingAck(ackID string) {
	c.ackMu.Lock()
	defer c.ackMu.Unlock()
	delete(c.pendingAcks, ackID)
}

func (c *Client) resolvePendingAck(ackID string, payload []any, err error) {
	c.ackMu.Lock()
	ackCh := c.pendingAcks[ackID]
	delete(c.pendingAcks, ackID)
	c.ackMu.Unlock()
	if ackCh != nil {
		ackCh <- ackResult{payload: payload, err: err}
	}
}

func (c *Client) failPendingAcks(err error) {
	c.ackMu.Lock()
	pending := c.pendingAcks
	c.pendingAcks = map[string]chan ackResult{}
	c.ackMu.Unlock()
	for _, ackCh := range pending {
		ackCh <- ackResult{err: err}
	}
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
	basePath := strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(basePath, "/socket.io") {
		parsed.Path = basePath + "/"
	} else if basePath == "" {
		parsed.Path = "/socket.io/"
	} else {
		parsed.Path = basePath + "/socket.io/"
	}
	parsed.RawQuery = "EIO=4&transport=websocket"
	return parsed.String(), nil
}

type afterAckResponder interface {
	AckPayload() any
	AfterAck()
}

func ackPayload(response any) any {
	if wrapped, ok := response.(afterAckResponder); ok {
		return wrapped.AckPayload()
	}
	return response
}

func afterAckCallback(response any) func() {
	if wrapped, ok := response.(afterAckResponder); ok {
		return wrapped.AfterAck
	}
	return nil
}

func encodeEventPacket(event string, payload any) (string, error) {
	return encodeEventPacketWithAck(event, payload, "")
}

func encodeEventPacketWithAck(event string, payload any, ackID string) (string, error) {
	body, err := json.Marshal([]any{event, payload})
	if err != nil {
		return "", err
	}
	return "42" + strings.TrimSpace(ackID) + string(body), nil
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

func (c *Client) handleAckPacket(rest string) error {
	ackID, jsonText := splitPacketID(rest)
	if ackID == "" {
		return fmt.Errorf("ack packet missing id")
	}
	if jsonText == "" {
		c.resolvePendingAck(ackID, nil, nil)
		return nil
	}
	var values []any
	if err := json.Unmarshal([]byte(jsonText), &values); err != nil {
		c.resolvePendingAck(ackID, nil, err)
		return err
	}
	c.resolvePendingAck(ackID, values, nil)
	return nil
}

func splitPacketID(rest string) (string, string) {
	i := 0
	for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
		i++
	}
	return rest[:i], rest[i:]
}

func AckIDFromInt(value int64) string {
	if value <= 0 {
		return ""
	}
	return strconv.FormatInt(value, 10)
}
