package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"borealis/api-backend/internal/wireguardcontrol"
)

const defaultSocketPath = "/opt/Borealis/Engine/Services/wireguard-tunnel/run/control.sock"

func main() {
	command := "status"
	if len(os.Args) > 1 && strings.TrimSpace(os.Args[1]) != "" {
		command = strings.TrimSpace(os.Args[1])
	}
	socketPath := strings.TrimSpace(os.Getenv("BOREALIS_WIREGUARD_CONTROL_SOCKET"))
	if socketPath == "" {
		socketPath = defaultSocketPath
	}
	connection, err := net.DialTimeout("unix", socketPath, 10*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(10 * time.Second))
	payload, _ := json.Marshal(map[string]string{"command": command})
	if _, err := connection.Write(append(payload, '\n')); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	raw, err := io.ReadAll(io.LimitReader(connection, 1<<20))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_, _ = os.Stdout.Write(raw)
	var result wireguardcontrol.Result
	if err := json.Unmarshal(raw, &result); err != nil {
		os.Exit(1)
	}
	os.Exit(result.ReturnCode)
}
