package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	ProviderBrave            = "brave"
	ProviderTavily           = "tavily"
	ProviderExa              = "exa"
	DefaultBraveEndpoint     = "https://api.search.brave.com/res/v1/web/search"
	DefaultTavilyEndpoint    = "https://api.tavily.com/search"
	DefaultExaEndpoint       = "https://api.exa.ai/search"
	DefaultMaxResults        = 5
	DefaultExtraSnippetLimit = 3
	BraveMaxResults          = 20
	TavilyMaxResults         = 20
	ExaMaxResults            = 20
	DefaultTimeout           = 15 * time.Second
)

type Config struct {
	Provider   string
	APIKey     string
	Endpoint   string
	Timeout    time.Duration
	MaxResults int
	SafeSearch string
	Country    string
	SearchLang string
}

func (c Config) Enabled() bool {
	switch c.NormalizedProvider() {
	case ProviderBrave, ProviderTavily, ProviderExa:
		return strings.TrimSpace(c.APIKey) != ""
	default:
		return false
	}
}

func (c Config) NormalizedProvider() string {
	return strings.ToLower(strings.TrimSpace(c.Provider))
}

type Query struct {
	Query      string
	Count      int
	Offset     int
	Freshness  string
	SafeSearch string
	Country    string
	SearchLang string
}

type Response struct {
	Provider             string
	Query                string
	MoreResultsAvailable bool
	Results              []Result
}

type Result struct {
	Title         string
	URL           string
	Description   string
	ExtraSnippets []string
	Age           string
	Language      string
}

type Client interface {
	Search(ctx context.Context, query Query) (Response, error)
}

func NewClient(cfg Config) (Client, error) {
	switch cfg.NormalizedProvider() {
	case "":
		return nil, nil
	case ProviderBrave:
		if strings.TrimSpace(cfg.APIKey) == "" {
			return nil, fmt.Errorf("web search provider brave requires HECATE_TASK_WEB_SEARCH_API_KEY or provider-specific API key alias")
		}
		return NewBraveClient(cfg), nil
	case ProviderTavily:
		if strings.TrimSpace(cfg.APIKey) == "" {
			return nil, fmt.Errorf("web search provider tavily requires HECATE_TASK_WEB_SEARCH_API_KEY or provider-specific API key alias")
		}
		return NewTavilyClient(cfg), nil
	case ProviderExa:
		if strings.TrimSpace(cfg.APIKey) == "" {
			return nil, fmt.Errorf("web search provider exa requires HECATE_TASK_WEB_SEARCH_API_KEY or provider-specific API key alias")
		}
		return NewExaClient(cfg), nil
	default:
		return nil, fmt.Errorf("unsupported web search provider %q", cfg.Provider)
	}
}

type BraveClient struct {
	apiKey     string
	endpoint   string
	httpClient *http.Client
	maxResults int
	safeSearch string
	country    string
	searchLang string
}

func NewBraveClient(cfg Config) *BraveClient {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		endpoint = DefaultBraveEndpoint
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	maxResults := cfg.MaxResults
	if maxResults <= 0 {
		maxResults = DefaultMaxResults
	}
	if maxResults > BraveMaxResults {
		maxResults = BraveMaxResults
	}
	return &BraveClient{
		apiKey:     strings.TrimSpace(cfg.APIKey),
		endpoint:   endpoint,
		httpClient: &http.Client{Timeout: timeout},
		maxResults: maxResults,
		safeSearch: normalizeSafeSearch(cfg.SafeSearch),
		country:    strings.TrimSpace(cfg.Country),
		searchLang: strings.TrimSpace(cfg.SearchLang),
	}
}

func (c *BraveClient) Search(ctx context.Context, query Query) (Response, error) {
	if c == nil {
		return Response{}, fmt.Errorf("web search client is not configured")
	}
	q := strings.TrimSpace(query.Query)
	if q == "" {
		return Response{}, fmt.Errorf("query is required")
	}
	endpoint, err := url.Parse(c.endpoint)
	if err != nil {
		return Response{}, fmt.Errorf("invalid Brave Search endpoint: %w", err)
	}
	values := endpoint.Query()
	resultLimit := c.count(query.Count)
	values.Set("q", q)
	values.Set("count", fmt.Sprintf("%d", resultLimit))
	if query.Offset > 0 {
		values.Set("offset", fmt.Sprintf("%d", query.Offset))
	}
	if freshness := strings.TrimSpace(query.Freshness); freshness != "" {
		values.Set("freshness", freshness)
	}
	if safeSearch := normalizeSafeSearch(firstNonEmpty(query.SafeSearch, c.safeSearch)); safeSearch != "" {
		values.Set("safesearch", safeSearch)
	}
	if country := strings.TrimSpace(firstNonEmpty(query.Country, c.country)); country != "" {
		values.Set("country", country)
	}
	if searchLang := strings.TrimSpace(firstNonEmpty(query.SearchLang, c.searchLang)); searchLang != "" {
		values.Set("search_lang", searchLang)
	}
	values.Set("extra_snippets", "true")
	endpoint.RawQuery = values.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Response{}, fmt.Errorf("build Brave Search request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return Response{}, fmt.Errorf("read Brave Search response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body := strings.TrimSpace(string(raw))
		if body == "" {
			body = resp.Status
		}
		return Response{}, fmt.Errorf("Brave Search returned %s: %s", resp.Status, body)
	}

	var payload braveWebSearchResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return Response{}, fmt.Errorf("decode Brave Search response: %w", err)
	}
	results := make([]Result, 0, len(payload.Web.Results))
	for _, item := range payload.Web.Results {
		if len(results) >= resultLimit {
			break
		}
		title := strings.TrimSpace(item.Title)
		urlStr := strings.TrimSpace(item.URL)
		if title == "" && urlStr == "" {
			continue
		}
		results = append(results, Result{
			Title:         title,
			URL:           urlStr,
			Description:   strings.TrimSpace(item.Description),
			ExtraSnippets: trimNonEmpty(item.ExtraSnippets, DefaultExtraSnippetLimit),
			Age:           strings.TrimSpace(item.Age),
			Language:      strings.TrimSpace(item.Language),
		})
	}
	original := strings.TrimSpace(payload.Query.Original)
	if original == "" {
		original = q
	}
	return Response{
		Provider:             ProviderBrave,
		Query:                original,
		MoreResultsAvailable: payload.Query.MoreResultsAvailable,
		Results:              results,
	}, nil
}

func (c *BraveClient) count(requested int) int {
	if requested <= 0 {
		return c.maxResults
	}
	if requested > c.maxResults {
		return c.maxResults
	}
	return requested
}

type TavilyClient struct {
	apiKey     string
	endpoint   string
	httpClient *http.Client
	maxResults int
}

func NewTavilyClient(cfg Config) *TavilyClient {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		endpoint = DefaultTavilyEndpoint
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	maxResults := cfg.MaxResults
	if maxResults <= 0 {
		maxResults = DefaultMaxResults
	}
	if maxResults > TavilyMaxResults {
		maxResults = TavilyMaxResults
	}
	return &TavilyClient{
		apiKey:     strings.TrimSpace(cfg.APIKey),
		endpoint:   endpoint,
		httpClient: &http.Client{Timeout: timeout},
		maxResults: maxResults,
	}
}

func (c *TavilyClient) Search(ctx context.Context, query Query) (Response, error) {
	if c == nil {
		return Response{}, fmt.Errorf("web search client is not configured")
	}
	q := strings.TrimSpace(query.Query)
	if q == "" {
		return Response{}, fmt.Errorf("query is required")
	}
	resultLimit := c.count(query.Count)
	body := map[string]any{
		"query":          q,
		"max_results":    resultLimit,
		"search_depth":   "basic",
		"include_answer": false,
	}
	if timeRange := tavilyTimeRange(query.Freshness); timeRange != "" {
		body["time_range"] = timeRange
	}
	rawReq, err := json.Marshal(body)
	if err != nil {
		return Response{}, fmt.Errorf("encode Tavily Search request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(rawReq))
	if err != nil {
		return Response{}, fmt.Errorf("build Tavily Search request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return Response{}, fmt.Errorf("read Tavily Search response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body := strings.TrimSpace(string(raw))
		if body == "" {
			body = resp.Status
		}
		return Response{}, fmt.Errorf("Tavily Search returned %s: %s", resp.Status, body)
	}

	var payload tavilySearchResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return Response{}, fmt.Errorf("decode Tavily Search response: %w", err)
	}
	results := make([]Result, 0, len(payload.Results))
	for _, item := range payload.Results {
		if len(results) >= resultLimit {
			break
		}
		title := strings.TrimSpace(item.Title)
		urlStr := strings.TrimSpace(item.URL)
		if title == "" && urlStr == "" {
			continue
		}
		results = append(results, Result{
			Title:       title,
			URL:         urlStr,
			Description: strings.TrimSpace(item.Content),
		})
	}
	original := strings.TrimSpace(payload.Query)
	if original == "" {
		original = q
	}
	return Response{
		Provider: ProviderTavily,
		Query:    original,
		Results:  results,
	}, nil
}

func (c *TavilyClient) count(requested int) int {
	if requested <= 0 {
		return c.maxResults
	}
	if requested > c.maxResults {
		return c.maxResults
	}
	return requested
}

type tavilySearchResponse struct {
	Query   string `json:"query"`
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Content string `json:"content"`
	} `json:"results"`
}

type ExaClient struct {
	apiKey     string
	endpoint   string
	httpClient *http.Client
	maxResults int
	country    string
}

func NewExaClient(cfg Config) *ExaClient {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		endpoint = DefaultExaEndpoint
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	maxResults := cfg.MaxResults
	if maxResults <= 0 {
		maxResults = DefaultMaxResults
	}
	if maxResults > ExaMaxResults {
		maxResults = ExaMaxResults
	}
	return &ExaClient{
		apiKey:     strings.TrimSpace(cfg.APIKey),
		endpoint:   endpoint,
		httpClient: &http.Client{Timeout: timeout},
		maxResults: maxResults,
		country:    strings.TrimSpace(cfg.Country),
	}
}

func (c *ExaClient) Search(ctx context.Context, query Query) (Response, error) {
	if c == nil {
		return Response{}, fmt.Errorf("web search client is not configured")
	}
	q := strings.TrimSpace(query.Query)
	if q == "" {
		return Response{}, fmt.Errorf("query is required")
	}
	resultLimit := c.count(query.Count)
	body := map[string]any{
		"query":      q,
		"numResults": resultLimit,
		"contents": map[string]any{
			"highlights": true,
		},
	}
	if country := strings.TrimSpace(firstNonEmpty(query.Country, c.country)); country != "" {
		body["userLocation"] = strings.ToUpper(country)
	}
	rawReq, err := json.Marshal(body)
	if err != nil {
		return Response{}, fmt.Errorf("encode Exa Search request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(rawReq))
	if err != nil {
		return Response{}, fmt.Errorf("build Exa Search request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return Response{}, fmt.Errorf("read Exa Search response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body := strings.TrimSpace(string(raw))
		if body == "" {
			body = resp.Status
		}
		return Response{}, fmt.Errorf("Exa Search returned %s: %s", resp.Status, body)
	}

	var payload exaSearchResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return Response{}, fmt.Errorf("decode Exa Search response: %w", err)
	}
	results := make([]Result, 0, len(payload.Results))
	for _, item := range payload.Results {
		if len(results) >= resultLimit {
			break
		}
		title := strings.TrimSpace(item.Title)
		urlStr := strings.TrimSpace(item.URL)
		if title == "" && urlStr == "" {
			continue
		}
		results = append(results, Result{
			Title:         title,
			URL:           urlStr,
			Description:   strings.TrimSpace(firstNonEmpty(item.Summary, item.Text)),
			ExtraSnippets: trimNonEmpty(item.Highlights, DefaultExtraSnippetLimit),
			Age:           strings.TrimSpace(item.PublishedDate),
		})
	}
	return Response{
		Provider: ProviderExa,
		Query:    q,
		Results:  results,
	}, nil
}

func (c *ExaClient) count(requested int) int {
	if requested <= 0 {
		return c.maxResults
	}
	if requested > c.maxResults {
		return c.maxResults
	}
	return requested
}

type exaSearchResponse struct {
	Results []struct {
		Title         string   `json:"title"`
		URL           string   `json:"url"`
		Text          string   `json:"text"`
		Summary       string   `json:"summary"`
		Highlights    []string `json:"highlights"`
		PublishedDate string   `json:"publishedDate"`
	} `json:"results"`
}

type braveWebSearchResponse struct {
	Query struct {
		Original             string `json:"original"`
		MoreResultsAvailable bool   `json:"more_results_available"`
	} `json:"query"`
	Web struct {
		Results []struct {
			Title         string   `json:"title"`
			URL           string   `json:"url"`
			Description   string   `json:"description"`
			ExtraSnippets []string `json:"extra_snippets"`
			Age           string   `json:"age"`
			Language      string   `json:"language"`
		} `json:"results"`
	} `json:"web"`
}

func normalizeSafeSearch(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "moderate", "strict", "off":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func trimNonEmpty(values []string, limit int) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if limit > 0 && len(out) >= limit {
			break
		}
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func tavilyTimeRange(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "pd", "d", "day":
		return "day"
	case "pw", "w", "week":
		return "week"
	case "pm", "m", "month":
		return "month"
	case "py", "y", "year":
		return "year"
	default:
		return ""
	}
}
