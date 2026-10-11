package mcp

import (
	"encoding/json"
	"errors"
	"strings"
)

// JSONRPCRequest es un request JSON-RPC 2.0.
type JSONRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
	ID      interface{} `json:"id,omitempty"`
}

// JSONRPCResponse es una response JSON-RPC 2.0.
type JSONRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	Result  interface{} `json:"result,omitempty"`
	Error   *JSONRPCErr `json:"error,omitempty"`
	ID      interface{} `json:"id,omitempty"`
}

// JSONRPCErr es el error en una response JSON-RPC 2.0.
type JSONRPCErr struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// InitializeRequest es el params de initialize.
type InitializeRequest struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Capabilities    map[string]bool `json:"capabilities,omitempty"`
	ClientInfo      ClientInfo      `json:"clientInfo"`
	Roots           []Root          `json:"roots,omitempty"`
}

// ClientInfo es la información del cliente.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Root es una raíz del cliente.
type Root struct {
	URI `json:"uri"`
}

// URI es una URI de recurso.
type URI struct {
	Scheme   string `json:"scheme"`
	Path     string `json:"path,omitempty"`
	Server   string `json:"server,omitempty"`
	Database string `json:"database,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Hostname string `json:"hostname,omitempty"`
	Port     string `json:"port,omitempty"`
	Query    string `json:"query,omitempty"`
	Fragment string `json:"fragment,omitempty"`
}

// InitializeResponse es el resultado de initialize.
type InitializeResponse struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Capabilities    map[string]bool `json:"capabilities,omitempty"`
	ServerInfo      ServerInfo      `json:"serverInfo"`
}

// ServerInfo es la información del servidor.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Tool es una herramienta disponible en el servidor.
type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema InputSchema `json:"inputSchema"`
}

// InputSchema es el esquema de input de una herramienta.
type InputSchema struct {
	Type       string                 `json:"type"`
	Properties map[string]interface{} `json:"properties,omitempty"`
	Required   []string               `json:"required,omitempty"`
}

// ToolsListResponse es el resultado de tools/list.
type ToolsListResponse struct {
	Tools []Tool `json:"tools"`
}

// ToolCallRequest es el params para tools/call.
type ToolCallRequest struct {
	Name      string      `json:"name"`
	Arguments interface{} `json:"arguments"`
}

// ContentBlock es un bloque de contenido dentro de un ToolResult (formato MCP estándar).
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// ToolResult es el resultado de una llamada a herramienta. El servidor MCP real
// devuelve el texto en Content (uno o más bloques) y, cuando el tool define un
// output schema, también StructuredContent con el mismo dato ya parseado.
type ToolResult struct {
	Content           []ContentBlock  `json:"content,omitempty"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	IsError           bool            `json:"isError,omitempty"`
}

// Text concatena el texto de todos los bloques de contenido.
func (r *ToolResult) Text() string {
	if r == nil {
		return ""
	}
	var b strings.Builder
	for i, c := range r.Content {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(c.Text)
	}
	return b.String()
}

// DecodeStructured deserializa StructuredContent en dst. Si el servidor no
// envió structuredContent (algunos tools solo devuelven texto), intenta
// parsear el primer bloque de texto como JSON.
func (r *ToolResult) DecodeStructured(dst interface{}) error {
	if r == nil {
		return errors.New("nil tool result")
	}
	if len(r.StructuredContent) > 0 {
		return json.Unmarshal(r.StructuredContent, dst)
	}
	if t := r.Text(); t != "" {
		return json.Unmarshal([]byte(t), dst)
	}
	return errors.New("tool result has no content to decode")
}
