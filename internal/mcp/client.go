package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sync"
)

// Client es un cliente MCP que se comunica con un servidor vía stdio.
type Client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr *bufio.Reader

	writeMu sync.Mutex // serializa escrituras a stdin para llamadas concurrentes

	mu          sync.Mutex
	counter     int
	responses   map[int]chan JSONRPCResponse
	initialized bool
}

// NewClient crea un nuevo cliente MCP que ejecuta el comando dado.
// Los argumentos del comando deben incluir cómo ejecutar el servidor MCP.
// Ejemplo: "npx", "-y", "endorctl", "ai-tools", "mcp-server"
func NewClient(name string, args ...string) (*Client, error) {
	cmd := newCommand(name, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start command: %w", err)
	}

	c := &Client{
		cmd:       cmd,
		stdin:     stdin,
		stdout:    bufio.NewReader(stdout),
		stderr:    bufio.NewReader(stderr),
		responses: make(map[int]chan JSONRPCResponse),
	}

	// Leer responses en background
	go c.readResponses()

	return c, nil
}

// newCommand construye el exec.Cmd para name/args. En Windows, "npx" suele
// resolver a un shim .cmd (un script por lotes), y CreateProcess no puede
// ejecutar un .cmd directamente sin pasar por el intérprete de comandos —
// exec.Command("npx", ...) falla ahí con "%1 is not a valid Win32
// application". Pasándolo siempre por "cmd /C" en Windows evita depender de
// si resuelve a .cmd o a un .exe real.
func newCommand(name string, args ...string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", append([]string{"/C", name}, args...)...)
	}
	return exec.Command(name, args...)
}

// Initialize inicializa la sesión MCP.
func (c *Client) Initialize(ctx context.Context) error {
	c.mu.Lock()
	if c.initialized {
		c.mu.Unlock()
		return errors.New("already initialized")
	}
	c.mu.Unlock()

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "initialize",
		ID:      1,
		Params: InitializeRequest{
			ProtocolVersion: "2024-11-05",
			ClientInfo: ClientInfo{
				Name:    "auri-tui",
				Version: "0.1.0",
			},
		},
	}

	resp, err := c.sendRequest(ctx, req)
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("initialize error: %s", resp.Error.Message)
	}

	c.mu.Lock()
	c.initialized = true
	c.mu.Unlock()

	return nil
}

// ListTools lista las herramientas disponibles en el servidor.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	if !c.isInitialized() {
		return nil, errors.New("not initialized, call Initialize first")
	}

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "tools/list",
		ID:      c.nextID(),
	}

	resp, err := c.sendRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("tools/list error: %s", resp.Error.Message)
	}

	var result ToolsListResponse
	data, err := json.Marshal(resp.Result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tools: %w", err)
	}

	return result.Tools, nil
}

// CallTool invoca una herramienta del servidor.
func (c *Client) CallTool(ctx context.Context, name string, args interface{}) (*ToolResult, error) {
	if !c.isInitialized() {
		return nil, errors.New("not initialized, call Initialize first")
	}

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "tools/call",
		ID:      c.nextID(),
		Params: ToolCallRequest{
			Name:      name,
			Arguments: args,
		},
	}

	resp, err := c.sendRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("tools/call error: %s", resp.Error.Message)
	}

	var result ToolResult
	data, err := json.Marshal(resp.Result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tool result: %w", err)
	}

	return &result, nil
}

// Close cierra la conexión y termina el proceso.
func (c *Client) Close() error {
	c.stdin.Close()
	return c.cmd.Wait()
}

// sendRequest envía un request JSON-RPC 2.0 y espera la respuesta.
func (c *Client) sendRequest(ctx context.Context, req JSONRPCRequest) (JSONRPCResponse, error) {
	// Registrar canal para la respuesta
	id, ok := req.ID.(int)
	if !ok {
		return JSONRPCResponse{}, errors.New("request ID must be an int")
	}

	respChan := make(chan JSONRPCResponse, 1)
	c.mu.Lock()
	c.responses[id] = respChan
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.responses, id)
		c.mu.Unlock()
	}()

	// Serializar y enviar request
	data, err := json.Marshal(req)
	if err != nil {
		return JSONRPCResponse{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	c.writeMu.Lock()
	_, err = c.stdin.Write(append(data, '\n'))
	c.writeMu.Unlock()
	if err != nil {
		return JSONRPCResponse{}, fmt.Errorf("failed to write request: %w", err)
	}

	// Esperar respuesta: el único límite de tiempo es el ctx del llamador
	// (algunas tools, como "scan", pueden tardar minutos legítimamente).
	select {
	case resp := <-respChan:
		return resp, nil
	case <-ctx.Done():
		return JSONRPCResponse{}, ctx.Err()
	}
}

// readResponses lee responses JSON-RPC 2.0 desde stdout del proceso.
func (c *Client) readResponses() {
	for {
		line, err := c.stdout.ReadBytes('\n')
		if err != nil {
			if err != io.EOF {
				fmt.Fprintf(os.Stderr, "mcp: failed to read response: %v\n", err)
			}
			return
		}

		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		var resp JSONRPCResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			fmt.Fprintf(os.Stderr, "mcp: failed to unmarshal response: %v\n", err)
			continue
		}

		// Enviar respuesta al canal correspondiente
		id, ok := resp.ID.(float64)
		if !ok {
			continue
		}

		c.mu.Lock()
		ch, ok := c.responses[int(id)]
		c.mu.Unlock()

		if ok {
			select {
			case ch <- resp:
			default:
				fmt.Fprintf(os.Stderr, "mcp: response channel full for ID %d\n", int(id))
			}
		}
	}
}

// nextID devuelve el siguiente ID para un request.
func (c *Client) nextID() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counter++
	return c.counter
}

// isInitialized verifica si el cliente está inicializado.
func (c *Client) isInitialized() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.initialized
}
