package clusterremote

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

type mappedDialer struct{ address string }

func (dialer mappedDialer) DialContext(ctx context.Context, network, _ string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, dialer.address)
}

type fakeSSH struct {
	transport Transport
	target    Target
	key       HostKey
	password  []byte
	clientKey ed25519.PrivateKey
	authCalls atomic.Int64
	execCalls atomic.Int64
}

func newFakeSSH(t *testing.T, mode string) *fakeSSH {
	t.Helper()
	_, hostPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPrivate)
	if err != nil {
		t.Fatal(err)
	}
	_, clientPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clientSigner, err := ssh.NewSignerFromKey(clientPrivate)
	if err != nil {
		t.Fatal(err)
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	server := &fakeSSH{target: Target{Address: "192.168.50.10", Port: 22},
		password: []byte(base64.RawURLEncoding.EncodeToString(random)), clientKey: clientPrivate,
		key: HostKey{Algorithm: hostSigner.PublicKey().Type(), Fingerprint: ssh.FingerprintSHA256(hostSigner.PublicKey()), PublicKey: hostSigner.PublicKey().Marshal()}}
	config := &ssh.ServerConfig{
		PasswordCallback: func(metadata ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			server.authCalls.Add(1)
			if metadata.User() == "operator" && hmac.Equal(password, server.password) {
				return nil, nil
			}
			return nil, errors.New("denied")
		},
		PublicKeyCallback: func(metadata ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			server.authCalls.Add(1)
			if metadata.User() == "operator" && bytes.Equal(key.Marshal(), clientSigner.PublicKey().Marshal()) {
				return nil, nil
			}
			return nil, errors.New("denied")
		},
	}
	config.AddHostKey(hostSigner)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server.transport.Dialer = mappedDialer{address: listener.Addr().String()}
	var wait sync.WaitGroup
	var mu sync.Mutex
	var connections []net.Conn
	wait.Add(1)
	go func() {
		defer wait.Done()
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			connections = append(connections, connection)
			mu.Unlock()
			wait.Add(1)
			go func() {
				defer wait.Done()
				defer connection.Close()
				sshConnection, channels, requests, err := ssh.NewServerConn(connection, config)
				if err != nil {
					return
				}
				defer sshConnection.Close()
				go ssh.DiscardRequests(requests)
				for incoming := range channels {
					if incoming.ChannelType() != "session" {
						incoming.Reject(ssh.UnknownChannelType, "unsupported")
						continue
					}
					channel, requests, err := incoming.Accept()
					if err != nil {
						return
					}
					for request := range requests {
						if request.Type != "exec" {
							request.Reply(false, nil)
							continue
						}
						var command struct{ Command string }
						if ssh.Unmarshal(request.Payload, &command) != nil || !strings.Contains(command.Command, "uname -s") || strings.Contains(command.Command, string(server.password)) {
							request.Reply(false, nil)
							channel.Close()
							continue
						}
						server.execCalls.Add(1)
						request.Reply(true, nil)
						if mode == "hang" {
							continue
						}
						status := uint32(0)
						switch mode {
						case "stdout overflow":
							channel.Write(bytes.Repeat([]byte("x"), MaxOutputBytes+1))
						case "stderr overflow":
							channel.Stderr().Write(bytes.Repeat([]byte("x"), MaxOutputBytes+1))
						case "secret error":
							channel.Stderr().Write(server.password)
							status = 1
						case "malformed":
							channel.Write([]byte("credential=" + string(server.password) + "\n"))
						default:
							channel.Write([]byte("kernel=Linux\narchitecture=x86_64\nuid=1000\nhostname=new-engine\n"))
						}
						channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
						channel.Close()
					}
				}
			}()
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		mu.Lock()
		for _, connection := range connections {
			connection.Close()
		}
		mu.Unlock()
		wait.Wait()
	})
	return server
}

func TestProbeAndChangedPinNeverSendAuthentication(t *testing.T) {
	server := newFakeSSH(t, "normal")
	observed, err := server.transport.ProbeHostKey(context.Background(), server.target)
	if err != nil || observed.Fingerprint != server.key.Fingerprint || !bytes.Equal(observed.PublicKey, server.key.PublicKey) {
		t.Fatalf("host key discovery failed: %v", err)
	}
	if server.authCalls.Load() != 0 {
		t.Fatal("host key discovery authenticated")
	}
	other := newFakeSSH(t, "normal")
	credential, err := PasswordCredential("operator", server.password)
	if err != nil {
		t.Fatal(err)
	}
	defer credential.Destroy()
	client, err := server.transport.Connect(context.Background(), server.target, other.key, credential)
	if client != nil {
		client.Close()
		t.Fatal("changed host key connected")
	}
	if !errors.Is(err, ErrHostKeyChanged) || server.authCalls.Load() != 0 {
		t.Fatalf("changed pin reached authentication: err=%v calls=%d", err, server.authCalls.Load())
	}
}

func TestPinnedPasswordAndKeyAuthenticationAndFixedInspection(t *testing.T) {
	for _, method := range []string{"password", "private key", "encrypted private key"} {
		t.Run(method, func(t *testing.T) {
			server := newFakeSSH(t, "normal")
			var credential *Credential
			var err error
			if method == "password" {
				credential, err = PasswordCredential("operator", server.password)
			} else {
				var block *pem.Block
				var passphrase []byte
				if method == "encrypted private key" {
					passphrase = server.password
					block, err = ssh.MarshalPrivateKeyWithPassphrase(server.clientKey, "fixture", passphrase)
				} else {
					block, err = ssh.MarshalPrivateKey(server.clientKey, "fixture")
				}
				if err != nil {
					t.Fatal(err)
				}
				credential, err = KeyCredential("operator", pem.EncodeToMemory(block), passphrase)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer credential.Destroy()
			client, err := server.transport.Connect(context.Background(), server.target, server.key, credential)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			facts, err := client.Inspect(context.Background())
			if err != nil || facts.Kernel != "Linux" || facts.Architecture != "x86_64" || facts.UID != 1000 || facts.Hostname != "new-engine" {
				t.Fatalf("fixed inspection failed: %v", err)
			}
			if server.authCalls.Load() == 0 || server.execCalls.Load() != 1 {
				t.Fatal("actual SSH authentication and exec required")
			}
		})
	}
}

func TestInspectionCancellationOutputBoundsAndRedaction(t *testing.T) {
	for _, mode := range []string{"hang", "stdout overflow", "stderr overflow", "secret error", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			server := newFakeSSH(t, mode)
			credential, err := PasswordCredential("operator", server.password)
			if err != nil {
				t.Fatal(err)
			}
			defer credential.Destroy()
			client, err := server.transport.Connect(context.Background(), server.target, server.key, credential)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
			defer cancel()
			facts, err := client.Inspect(ctx)
			if err == nil || facts != (HostFacts{}) || strings.Contains(err.Error(), string(server.password)) {
				t.Fatal("inspection must fail without disclosing remote output")
			}
			if mode == "hang" && !errors.Is(err, ErrCancelled) {
				t.Fatalf("cancellation not classified: %v", err)
			}
			if strings.Contains(mode, "overflow") && !errors.Is(err, ErrOutputLimit) {
				t.Fatalf("output overflow not classified: %v", err)
			}
		})
	}
}

func TestInvalidTargetsCredentialsAndPublicPins(t *testing.T) {
	server := newFakeSSH(t, "normal")
	credential, err := PasswordCredential("operator", server.password)
	if err != nil {
		t.Fatal(err)
	}
	defer credential.Destroy()
	for _, target := range []Target{{Address: "127.0.0.1", Port: 22}, {Address: "8.8.8.8", Port: 22}, {Address: "::1", Port: 22},
		{Address: "example.com", Port: 22}, {Address: "192.168.50.10", Port: 0}, {Address: "192.168.50.10", Port: 65536}} {
		if _, err := server.transport.Connect(context.Background(), target, server.key, credential); !errors.Is(err, ErrInvalidTarget) {
			t.Fatal("invalid target accepted")
		}
	}
	badPin := server.key
	badPin.Fingerprint = "unapproved"
	if _, err := server.transport.Connect(context.Background(), server.target, badPin, credential); !errors.Is(err, ErrHostKeyChanged) {
		t.Fatal("invalid approved pin accepted")
	}
	for _, password := range [][]byte{nil, {0}, bytes.Repeat([]byte("x"), MaxPasswordBytes+1), {0xff}} {
		if _, err := PasswordCredential("operator", password); err == nil {
			t.Fatal("invalid password shape accepted")
		}
	}
	if _, err := PasswordCredential("operator\nremote-command", server.password); err == nil {
		t.Fatal("invalid username accepted")
	}
	if _, err := KeyCredential("operator", []byte("invalid private material"), nil); err == nil {
		t.Fatal("invalid private key accepted")
	}
	for _, formatted := range []string{fmt.Sprint(*credential), fmt.Sprintf("%#v", credential)} {
		if strings.Contains(formatted, string(server.password)) || !strings.Contains(formatted, "redacted") {
			t.Fatal("credential formatting disclosed secret")
		}
	}
	credential.Destroy()
	if _, err := credential.authMethod(); !errors.Is(err, ErrInvalidAuth) {
		t.Fatal("destroyed credential reused")
	}
	if server.authCalls.Load() != 0 {
		t.Fatal("invalid inputs reached authentication")
	}
}

type stalledDialer struct{}

func (stalledDialer) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	client, server := net.Pipe()
	go func() { <-ctx.Done(); server.Close() }()
	return client, nil
}

func TestHandshakeCancellationIsBounded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := (Transport{Dialer: stalledDialer{}}).ProbeHostKey(ctx, Target{Address: "192.168.50.10", Port: 22})
	if err == nil || time.Since(started) > time.Second {
		t.Fatal("SSH handshake cancellation unbounded")
	}
}
