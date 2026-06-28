package websearch

import (
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
	ProviderBrave        = "brave"
	DefaultBraveEndpoint = "https://api.search.brave.com/res/v1/web/search"
	DefaultMaxResults    = 5
	BraveMaxResults      = 20
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
	return strings.EqualFold(strings.TrimSpace(c.Provider), ProviderBrave) && strings.TrimSpace(c.APIKey) != ""
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
			return nil, fmt.Errorf("web search provider brave requires HECATE_TASK_WEB_SEARCH_API_KEY or BRAVE_SEARCH_API_KEY")
		}
		return NewBraveClient(cfg), nil
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
		timeout = 15 * time.Second
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
	values.Set("q", q)
	values.Set("count", fmt.Sprintf("%d", c.count(query.Count)))
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
		title := strings.TrimSpace(item.Title)
		urlStr := strings.TrimSpace(item.URL)
		if title == "" && urlStr == "" {
			continue
		}
		results = append(results, Result{
			Title:         title,
			URL:           urlStr,
			Description:   strings.TrimSpace(item.Description),
			ExtraSnippets: trimNonEmpty(item.ExtraSnippets),
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
		return strings.ToLower(strings.TrimSpace(value))
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

func trimNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
