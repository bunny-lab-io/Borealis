package agentruntime

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bunny-lab-io/borealis/go-agent/internal/localui"
)

type uiBrokerOptions struct {
	StateDir        string
	Logger          *log.Logger
	Status          func() localui.StatusSnapshot
	StartUpdate     func() error
	RestartAgent    func() error
	DiagnosticsText func() string
}

type uiBroker struct {
	listener net.Listener
	server   *http.Server
	token    string
	url      string
	closed   sync.Once
}

func startUIBroker(ctx context.Context, options uiBrokerOptions) (*uiBroker, error) {
	token, err := randomToken(32)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	broker := &uiBroker{
		listener: listener,
		token:    token,
		url:      "http://" + listener.Addr().String(),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/command", broker.handleCommand(options))
	broker.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	if err := localui.WriteBrokerState(options.StateDir, localui.BrokerState{
		URL:       broker.url,
		Token:     broker.token,
		UpdatedAt: time.Now().Unix(),
	}); err != nil {
		_ = listener.Close()
		return nil, err
	}
	go func() {
		if err := broker.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) && options.Logger != nil {
			options.Logger.Printf("local UI broker stopped: %v", err)
		}
	}()
	go func() {
		<-ctx.Done()
		broker.Close()
	}()
	return broker, nil
}

func (b *uiBroker) Close() {
	if b == nil {
		return
	}
	b.closed.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if b.server != nil {
			_ = b.server.Shutdown(ctx)
		}
		if b.listener != nil {
			_ = b.listener.Close()
		}
	})
}

func (b *uiBroker) handleCommand(options uiBrokerOptions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			writeUIBrokerResponse(w, http.StatusMethodNotAllowed, localui.CommandResponse{Status: "error", Detail: "method not allowed"})
			return
		}
		if !b.authorized(r.Header.Get(localui.TokenHeader)) {
			writeUIBrokerResponse(w, http.StatusUnauthorized, localui.CommandResponse{Status: "error", Detail: "unauthorized"})
			return
		}
		var request localui.CommandRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeUIBrokerResponse(w, http.StatusBadRequest, localui.CommandResponse{Status: "error", Detail: "invalid JSON request"})
			return
		}
		switch strings.TrimSpace(request.Command) {
		case localui.CommandStatusGet:
			snapshot := localui.StatusSnapshot{}
			if options.Status != nil {
				snapshot = options.Status()
			}
			writeUIBrokerResponse(w, http.StatusOK, localui.CommandResponse{Status: "ok", Data: snapshot})
		case localui.CommandAgentUpdate:
			if options.StartUpdate == nil {
				writeUIBrokerResponse(w, http.StatusServiceUnavailable, localui.CommandResponse{Status: "error", Detail: "update action unavailable"})
				return
			}
			if err := options.StartUpdate(); err != nil {
				writeUIBrokerResponse(w, http.StatusInternalServerError, localui.CommandResponse{Status: "error", Detail: err.Error()})
				return
			}
			writeUIBrokerResponse(w, http.StatusOK, localui.CommandResponse{Status: "ok", Detail: "Update check started."})
		case localui.CommandAgentRestart:
			if options.RestartAgent == nil {
				writeUIBrokerResponse(w, http.StatusServiceUnavailable, localui.CommandResponse{Status: "error", Detail: "restart action unavailable"})
				return
			}
			if err := options.RestartAgent(); err != nil {
				writeUIBrokerResponse(w, http.StatusInternalServerError, localui.CommandResponse{Status: "error", Detail: err.Error()})
				return
			}
			writeUIBrokerResponse(w, http.StatusOK, localui.CommandResponse{Status: "ok", Detail: "Agent restart requested."})
		case localui.CommandDiagnosticsCopy:
			text := ""
			if options.DiagnosticsText != nil {
				text = options.DiagnosticsText()
			}
			writeUIBrokerResponse(w, http.StatusOK, localui.CommandResponse{Status: "ok", Data: map[string]any{"diagnostics_text": text}})
		default:
			writeUIBrokerResponse(w, http.StatusBadRequest, localui.CommandResponse{Status: "error", Detail: "unknown command"})
		}
	}
}

func (b *uiBroker) authorized(value string) bool {
	expected := strings.TrimSpace(b.token)
	actual := strings.TrimSpace(value)
	if expected == "" || actual == "" {
		return false
	}
	if len(expected) != len(actual) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) == 1
}

func writeUIBrokerResponse(w http.ResponseWriter, status int, response localui.CommandResponse) {
	if response.Status == "" {
		response.Status = "ok"
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}

func randomToken(size int) (string, error) {
	if size <= 0 {
		size = 32
	}
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}
