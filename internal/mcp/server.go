package mcp

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/portainer/portainer-mcp/pkg/portainer/client"
	"github.com/portainer/portainer-mcp/pkg/portainer/models"
	"github.com/portainer/portainer-mcp/pkg/toolgen"
	"github.com/rs/zerolog"
)

const (
	// MinimumToolsVersion is the minimum supported version of the tools.yaml file
	MinimumToolsVersion = "1.0"
	// SupportedPortainerVersion is the version of Portainer that is supported by this tool
	SupportedPortainerVersion = "2.31.2"
)

// PortainerClient defines the interface for the wrapper client used by the MCP server
type PortainerClient interface {
	// Tag methods
	GetEnvironmentTags() ([]models.EnvironmentTag, error)
	CreateEnvironmentTag(name string) (int, error)

	// Environment methods
	GetEnvironments() ([]models.Environment, error)
	UpdateEnvironmentTags(id int, tagIds []int) error
	UpdateEnvironmentUserAccesses(id int, userAccesses map[int]string) error
	UpdateEnvironmentTeamAccesses(id int, teamAccesses map[int]string) error

	// Environment Group methods
	GetEnvironmentGroups() ([]models.Group, error)
	CreateEnvironmentGroup(name string, environmentIds []int) (int, error)
	UpdateEnvironmentGroupName(id int, name string) error
	UpdateEnvironmentGroupEnvironments(id int, environmentIds []int) error
	UpdateEnvironmentGroupTags(id int, tagIds []int) error

	// Access Group methods
	GetAccessGroups() ([]models.AccessGroup, error)
	CreateAccessGroup(name string, environmentIds []int) (int, error)
	UpdateAccessGroupName(id int, name string) error
	UpdateAccessGroupUserAccesses(id int, userAccesses map[int]string) error
	UpdateAccessGroupTeamAccesses(id int, teamAccesses map[int]string) error
	AddEnvironmentToAccessGroup(id int, environmentId int) error
	RemoveEnvironmentFromAccessGroup(id int, environmentId int) error

	// Stack methods
	GetStacks() ([]models.Stack, error)
	GetStackFile(id int) (string, error)
	CreateStack(name string, file string, environmentGroupIds []int) (int, error)
	UpdateStack(id int, file string, environmentGroupIds []int) error

	// Team methods
	CreateTeam(name string) (int, error)
	GetTeams() ([]models.Team, error)
	UpdateTeamName(id int, name string) error
	UpdateTeamMembers(id int, userIds []int) error

	// User methods
	GetUsers() ([]models.User, error)
	UpdateUserRole(id int, role string) error

	// Settings methods
	GetSettings() (models.PortainerSettings, error)

	// Version methods
	GetVersion() (string, error)

	// Docker Proxy methods
	ProxyDockerRequest(opts models.DockerProxyRequestOptions) (*http.Response, error)

	// Kubernetes Proxy methods
	ProxyKubernetesRequest(opts models.KubernetesProxyRequestOptions) (*http.Response, error)
}

// PortainerMCPServer is the main server that handles MCP protocol communication
// with AI assistants and translates them into Portainer API calls.
type PortainerMCPServer struct {
	srv      *server.MCPServer
	cli      PortainerClient
	tools    map[string]mcp.Tool
	readOnly bool
}

// ServerOption is a function that configures the server
type ServerOption func(*serverOptions)

// serverOptions contains all configurable options for the server
type serverOptions struct {
	client              PortainerClient
	readOnly            bool
	disableVersionCheck bool
}

// WithClient sets a custom client for the server.
// This is primarily used for testing to inject mock clients.
func WithClient(client PortainerClient) ServerOption {
	return func(opts *serverOptions) {
		opts.client = client
	}
}

// WithReadOnly sets the server to read-only mode.
// This will prevent the server from registering write tools.
func WithReadOnly(readOnly bool) ServerOption {
	return func(opts *serverOptions) {
		opts.readOnly = readOnly
	}
}

// WithDisableVersionCheck disables the Portainer server version check.
// This allows connecting to unsupported Portainer versions.
func WithDisableVersionCheck(disable bool) ServerOption {
	return func(opts *serverOptions) {
		opts.disableVersionCheck = disable
	}
}

// NewPortainerMCPServer creates a new Portainer MCP server.
//
// This server provides an implementation of the MCP protocol for Portainer,
// allowing AI assistants to interact with Portainer through a structured API.
//
// Parameters:
//   - serverURL: The base URL of the Portainer server (e.g., "https://portainer.example.com")
//   - token: The API token for authenticating with the Portainer server
//   - toolsPath: Path to the tools.yaml file that defines the available MCP tools
//   - options: Optional functional options for customizing server behavior (e.g., WithClient)
//
// Returns:
//   - A configured PortainerMCPServer instance ready to be started
//   - An error if initialization fails
//
// Possible errors:
//   - Failed to load tools from the specified path
//   - Failed to communicate with the Portainer server
//   - Incompatible Portainer server version
func NewPortainerMCPServer(serverURL, token, toolsPath string, options ...ServerOption) (*PortainerMCPServer, error) {
	opts := &serverOptions{}

	for _, option := range options {
		option(opts)
	}

	tools, err := toolgen.LoadToolsFromYAML(toolsPath, MinimumToolsVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to load tools: %w", err)
	}

	var portainerClient PortainerClient
	if opts.client != nil {
		portainerClient = opts.client
	} else {
		portainerClient = client.NewPortainerClient(serverURL, token, client.WithSkipTLSVerify(true))
	}

	if !opts.disableVersionCheck {
		version, err := portainerClient.GetVersion()
		if err != nil {
			return nil, fmt.Errorf("failed to get Portainer server version: %w", err)
		}

		if version != SupportedPortainerVersion {
			return nil, fmt.Errorf("unsupported Portainer server version: %s, only version %s is supported", version, SupportedPortainerVersion)
		}
	}

	return &PortainerMCPServer{
		srv: server.NewMCPServer(
			"Portainer MCP Server",
			"0.5.1",
			server.WithToolCapabilities(true),
			server.WithLogging(),
		),
		cli:      portainerClient,
		tools:    tools,
		readOnly: opts.readOnly,
	}, nil
}

// Start begins listening for MCP protocol messages on standard input/output.
// This is a blocking call that will run until the connection is closed.
func (s *PortainerMCPServer) Start() error {
	return server.ServeStdio(s.srv)
}

// StartHTTP starts the MCP server with HTTP/SSE transport.
// This allows remote clients to connect via HTTP instead of stdio.
func (s *PortainerMCPServer) StartHTTP(port int, endpoint string) error {
	addr := fmt.Sprintf(":%d", port)

	// Create a zerolog-compatible logger for the HTTP server
	logger := zerolog.New(zerolog.NewConsoleWriter()).With().Timestamp().Logger()

	httpServer := server.NewStreamableHTTPServer(
		s.srv,
		server.WithEndpointPath(endpoint),
		server.WithHeartbeatInterval(30*time.Second),
	)

	log.Printf("Starting HTTP/SSE server on %s%s", addr, endpoint)

	mux := http.NewServeMux()
	mux.Handle(endpoint, httpServer)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	logger.Info().Str("addr", addr).Str("endpoint", endpoint).Msg("HTTP/SSE server started")

	return srv.ListenAndServe()
}

// addToolIfExists adds a tool to the server if it exists in the tools map
func (s *PortainerMCPServer) addToolIfExists(toolName string, handler server.ToolHandlerFunc) {
	if tool, exists := s.tools[toolName]; exists {
		s.srv.AddTool(tool, handler)
	} else {
		log.Printf("Tool %s not found, will not be registered for MCP usage", toolName)
	}
}
