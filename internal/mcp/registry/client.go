// Package registry implements the read-only MCP Registry REST client used by
// Hecate's operator discovery API.
package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultBaseURL = "https://registry.modelcontextprotocol.io"

	apiVersion       = "v0.1"
	maxResponseBytes = 8 << 20
)

// Client is a small read-only client for the official MCP Registry API.
type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
}

// NewClient builds a registry client rooted at baseURL. An empty baseURL uses
// the official production registry.
func NewClient(baseURL string, httpClient *http.Client) (*Client, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("mcp registry: parse base URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("mcp registry: base URL scheme must be http or https")
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("mcp registry: base URL host is required")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("mcp registry: base URL must not include query or fragment")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{baseURL: parsed, httpClient: httpClient}, nil
}

func (c *Client) BaseURL() string {
	return strings.TrimRight(c.baseURL.String(), "/")
}

// ListServers returns the registry server catalog page matching q.
func (c *Client) ListServers(ctx context.Context, q ListServersQuery) (ServerList, error) {
	endpoint, err := c.endpoint(apiVersion, "servers")
	if err != nil {
		return ServerList{}, err
	}
	values := url.Values{}
	if q.Cursor != "" {
		values.Set("cursor", q.Cursor)
	}
	if q.Limit > 0 {
		values.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.Search != "" {
		values.Set("search", q.Search)
	}
	if q.UpdatedSince != "" {
		values.Set("updated_since", q.UpdatedSince)
	}
	if q.Version != "" {
		values.Set("version", q.Version)
	}
	if q.IncludeDeleted {
		values.Set("include_deleted", "true")
	}
	endpoint.RawQuery = values.Encode()

	var out ServerList
	if err := c.getJSON(ctx, endpoint, &out); err != nil {
		return ServerList{}, err
	}
	return out, nil
}

func (c *Client) endpoint(parts ...string) (*url.URL, error) {
	joined, err := url.JoinPath(c.baseURL.String(), parts...)
	if err != nil {
		return nil, fmt.Errorf("mcp registry: build endpoint: %w", err)
	}
	parsed, err := url.Parse(joined)
	if err != nil {
		return nil, fmt.Errorf("mcp registry: parse endpoint: %w", err)
	}
	return parsed, nil
}

func (c *Client) getJSON(ctx context.Context, endpoint *url.URL, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("mcp registry: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "hecate-mcp-registry")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("mcp registry: get %s: %w", endpoint.Redacted(), err)
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, maxResponseBytes)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		excerpt, _ := io.ReadAll(io.LimitReader(limited, 512))
		return fmt.Errorf("mcp registry: server returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(excerpt)))
	}
	dec := json.NewDecoder(limited)
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("mcp registry: decode response: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("mcp registry: decode response: trailing data")
	}
	return nil
}

type ListServersQuery struct {
	Cursor         string
	Limit          int
	Search         string
	UpdatedSince   string
	Version        string
	IncludeDeleted bool
}

type ServerList struct {
	Servers  []ServerResponse `json:"servers"`
	Metadata ListMetadata     `json:"metadata"`
}

type ListMetadata struct {
	NextCursor string `json:"nextCursor,omitempty"`
	Count      int    `json:"count,omitempty"`
}

type ServerResponse struct {
	Server ServerDetail    `json:"server"`
	Meta   json.RawMessage `json:"_meta,omitempty"`
}

// UnmarshalJSON accepts both the current nested `{server:{...}, _meta:{...}}`
// shape and older flat server-list items returned by early registry previews.
func (r *ServerResponse) UnmarshalJSON(data []byte) error {
	type nested ServerResponse
	var probe struct {
		Server json.RawMessage `json:"server"`
		Meta   json.RawMessage `json:"_meta"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	if len(probe.Server) > 0 && string(probe.Server) != "null" {
		var out nested
		if err := json.Unmarshal(data, &out); err != nil {
			return err
		}
		*r = ServerResponse(out)
		return nil
	}
	var server ServerDetail
	if err := json.Unmarshal(data, &server); err != nil {
		return err
	}
	r.Server = server
	r.Meta = probe.Meta
	return nil
}

type ServerDetail struct {
	Schema      string          `json:"$schema,omitempty"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Title       string          `json:"title,omitempty"`
	Version     string          `json:"version,omitempty"`
	WebsiteURL  string          `json:"websiteUrl,omitempty"`
	Repository  *Repository     `json:"repository,omitempty"`
	Icons       []Icon          `json:"icons,omitempty"`
	Packages    []Package       `json:"packages,omitempty"`
	Remotes     []Remote        `json:"remotes,omitempty"`
	Meta        json.RawMessage `json:"_meta,omitempty"`
}

type Repository struct {
	URL       string `json:"url,omitempty"`
	Source    string `json:"source,omitempty"`
	ID        string `json:"id,omitempty"`
	Subfolder string `json:"subfolder,omitempty"`
}

type Icon struct {
	Src      string   `json:"src,omitempty"`
	MIMEType string   `json:"mimeType,omitempty"`
	Sizes    []string `json:"sizes,omitempty"`
	Theme    string   `json:"theme,omitempty"`
}

type Package struct {
	RegistryType         string      `json:"registryType,omitempty"`
	RegistryBaseURL      string      `json:"registryBaseUrl,omitempty"`
	Identifier           string      `json:"identifier,omitempty"`
	Version              string      `json:"version,omitempty"`
	FileSHA256           string      `json:"fileSha256,omitempty"`
	RuntimeHint          string      `json:"runtimeHint,omitempty"`
	Transport            Transport   `json:"transport,omitempty"`
	RuntimeArguments     []Argument  `json:"runtimeArguments,omitempty"`
	PackageArguments     []Argument  `json:"packageArguments,omitempty"`
	EnvironmentVariables []InputSpec `json:"environmentVariables,omitempty"`
}

type Remote struct {
	Type      string               `json:"type,omitempty"`
	URL       string               `json:"url,omitempty"`
	Headers   []InputSpec          `json:"headers,omitempty"`
	Variables map[string]InputSpec `json:"variables,omitempty"`
}

type Transport struct {
	Type    string      `json:"type,omitempty"`
	URL     string      `json:"url,omitempty"`
	Headers []InputSpec `json:"headers,omitempty"`
}

type Argument struct {
	Type        string               `json:"type,omitempty"`
	Name        string               `json:"name,omitempty"`
	Value       string               `json:"value,omitempty"`
	ValueHint   string               `json:"valueHint,omitempty"`
	Description string               `json:"description,omitempty"`
	IsRequired  bool                 `json:"isRequired,omitempty"`
	IsRepeated  bool                 `json:"isRepeated,omitempty"`
	IsSecret    bool                 `json:"isSecret,omitempty"`
	Default     string               `json:"default,omitempty"`
	Choices     []string             `json:"choices,omitempty"`
	Variables   map[string]InputSpec `json:"variables,omitempty"`
}

type InputSpec struct {
	Name        string               `json:"name,omitempty"`
	Description string               `json:"description,omitempty"`
	Format      string               `json:"format,omitempty"`
	Value       string               `json:"value,omitempty"`
	ValueHint   string               `json:"valueHint,omitempty"`
	Default     string               `json:"default,omitempty"`
	Placeholder string               `json:"placeholder,omitempty"`
	IsRequired  bool                 `json:"isRequired,omitempty"`
	IsSecret    bool                 `json:"isSecret,omitempty"`
	Choices     []string             `json:"choices,omitempty"`
	Variables   map[string]InputSpec `json:"variables,omitempty"`
}
