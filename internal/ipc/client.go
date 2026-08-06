package ipc

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Client talks to a running swarm.
type Client struct {
	conn net.Conn
	dec  *json.Decoder
	enc  *json.Encoder
	mu   sync.Mutex
}

// DefaultSocket returns the socket to use when none was given: $SWARM_SOCKET
// when swarm itself set it (that is the case inside an agent), otherwise the
// session socket under the state directory next to the config.
func DefaultSocket(stateDir, session string) string {
	if s := os.Getenv("SWARM_SOCKET"); s != "" {
		return s
	}
	if session == "" {
		session = "default"
	}
	return filepath.Join(stateDir, session+".sock")
}

// Dial connects to a swarm control socket.
func Dial(path string) (*Client, error) {
	conn, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("no swarm listening on %s (start one with `swarm run`): %w", path, err)
	}
	return &Client{conn: conn, dec: json.NewDecoder(conn), enc: json.NewEncoder(conn)}, nil
}

// Close releases the connection.
func (c *Client) Close() error { return c.conn.Close() }

// Send writes a request without waiting for a response. It is used to feed
// keystrokes during an attach.
func (c *Client) Send(req Request) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enc.Encode(req)
}

// Recv reads the next response frame.
func (c *Client) Recv() (Response, error) {
	var resp Response
	if err := c.dec.Decode(&resp); err != nil {
		return resp, err
	}
	return resp, nil
}

// Do performs a one-shot request and returns the final response.
func (c *Client) Do(req Request) (Response, error) {
	if err := c.Send(req); err != nil {
		return Response{}, err
	}
	resp, err := c.Recv()
	if err != nil {
		return resp, err
	}
	if !resp.OK && resp.Error != "" {
		return resp, errors.New(resp.Error)
	}
	return resp, nil
}

// Stream performs a request and calls fn for every frame until the stream ends
// or fn returns false.
func (c *Client) Stream(req Request, fn func(Response) bool) error {
	if err := c.Send(req); err != nil {
		return err
	}
	for {
		resp, err := c.Recv()
		if err != nil {
			return err
		}
		if !resp.OK && resp.Error != "" {
			return errors.New(resp.Error)
		}
		if !fn(resp) {
			return nil
		}
		if resp.Done {
			return nil
		}
	}
}

// Call is the convenience wrapper for the CLI: dial, do, close.
func Call(socket string, req Request) (Response, error) {
	c, err := Dial(socket)
	if err != nil {
		return Response{}, err
	}
	defer c.Close()
	return c.Do(req)
}
