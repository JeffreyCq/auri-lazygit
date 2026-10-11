package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

// fakeServer es un servidor MCP falso para testing.
// Lee requests JSON-RPC desde stdin y envía responses hardcodeadas a stdout.
func fakeServer() {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var req JSONRPCRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}

		var resp JSONRPCResponse
		resp.JSONRPC = "2.0"
		resp.ID = req.ID

		switch req.Method {
		case "initialize":
			resp.Result = InitializeResponse{
				ProtocolVersion: "2024-11-05",
				ServerInfo: ServerInfo{
					Name:    "fake-mcp-server",
					Version: "1.0.0",
				},
			}
		case "tools/list":
			resp.Result = ToolsListResponse{
				Tools: []Tool{
					{
						Name:        "analyze",
						Description: "Analyze code for vulnerabilities",
						InputSchema: InputSchema{
							Type: "object",
							Properties: map[string]interface{}{
								"code": map[string]interface{}{
									"type":        "string",
									"description": "Code to analyze",
								},
							},
							Required: []string{"code"},
						},
					},
				},
			}
		case "tools/call":
			if req.Params != nil {
				params := req.Params.(map[string]interface{})
				toolName, _ := params["name"].(string)
				if toolName == "analyze" {
					resp.Result = ToolResult{
						Content: []ContentBlock{{Type: "text", Text: "Analysis complete: found 3 issues"}},
					}
				}
			}
		default:
			resp.Error = &JSONRPCErr{
				Code:    -32601,
				Message: "Method not found",
			}
		}

		data, _ := json.Marshal(resp)
		fmt.Printf("%s\n", string(data))
	}
}

// TestClientInitialize prueba la inicialización.
func TestClientInitialize(t *testing.T) {
	if os.Getenv("FAKE_SERVER") == "1" {
		fakeServer()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestClientInitialize")
	cmd.Env = append(os.Environ(), "FAKE_SERVER=1")

	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	cmd.Start()

	c := &Client{
		cmd:       cmd,
		stdin:     stdin,
		stdout:    bufio.NewReader(stdout),
		stderr:    bufio.NewReader(stderr),
		responses: make(map[int]chan JSONRPCResponse),
	}
	defer c.Close()

	go c.readResponses()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := c.Initialize(ctx)
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	if !c.isInitialized() {
		t.Fatal("expected initialized=true")
	}
}

// TestClientListTools prueba listar herramientas.
func TestClientListTools(t *testing.T) {
	if os.Getenv("FAKE_SERVER") == "1" {
		fakeServer()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestClientListTools")
	cmd.Env = append(os.Environ(), "FAKE_SERVER=1")

	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	cmd.Start()

	c := &Client{
		cmd:         cmd,
		stdin:       stdin,
		stdout:      bufio.NewReader(stdout),
		stderr:      bufio.NewReader(stderr),
		responses:   make(map[int]chan JSONRPCResponse),
		initialized: true,
	}
	defer c.Close()

	go c.readResponses()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}

	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	if tools[0].Name != "analyze" {
		t.Fatalf("expected tool name 'analyze', got %q", tools[0].Name)
	}
}
