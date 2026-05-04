package telemetry

import (
	"net/url"
	"strings"
)

const (
	OTLPTransportHTTP = "http"
	OTLPTransportGRPC = "grpc"
)

func NormalizeOTLPTransport(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "http", "http/protobuf", "otlp/http", "otlphttp":
		return OTLPTransportHTTP
	case "grpc", "otlp/grpc", "otlpgrpc":
		return OTLPTransportGRPC
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func IsOTLPGRPCInsecure(endpoint string) bool {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return false
	}
	u, err := url.Parse(endpoint)
	if err == nil && u.Scheme != "" {
		return u.Scheme != "https"
	}
	return true
}

func OTLPGRPCEndpoint(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return ""
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" {
		return strings.TrimRight(endpoint, "/")
	}
	if u.Host != "" {
		return u.Host
	}
	return strings.TrimRight(strings.TrimPrefix(endpoint, u.Scheme+"://"), "/")
}
