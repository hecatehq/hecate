package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const hecateRuntimeFile = "hecate.runtime.json"

type gatewayRuntimeState struct {
	BaseURL     string `json:"base_url"`
	ListenAddr  string `json:"listen_addr"`
	PID         int    `json:"pid"`
	UpdatedUnix int64  `json:"updated_unix"`
}

func writeGatewayRuntimeState(dataDir, listenAddr, publicURL string) (string, error) {
	baseURL, err := gatewayBaseURL(listenAddr, publicURL)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", fmt.Errorf("create data dir: %w", err)
	}
	path := filepath.Join(dataDir, hecateRuntimeFile)
	payload, err := json.MarshalIndent(gatewayRuntimeState{
		BaseURL:     baseURL,
		ListenAddr:  listenAddr,
		PID:         os.Getpid(),
		UpdatedUnix: time.Now().Unix(),
	}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode hecate runtime state: %w", err)
	}
	payload = append(payload, '\n')
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		return "", fmt.Errorf("write hecate runtime state: %w", err)
	}
	return path, nil
}

func removeGatewayRuntimeState(path string) {
	if strings.TrimSpace(path) == "" {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "hecate: failed to remove runtime state %s: %v\n", path, err)
	}
}

func gatewayBaseURL(listenAddr, publicURL string) (string, error) {
	if strings.TrimSpace(publicURL) != "" {
		return normalizeGatewayURL(publicURL)
	}
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		if strings.HasPrefix(listenAddr, ":") {
			host = "127.0.0.1"
			port = strings.TrimPrefix(listenAddr, ":")
		} else {
			return "", fmt.Errorf("parse listen address %q: %w", listenAddr, err)
		}
	}
	host = strings.Trim(host, "[]")
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return normalizeGatewayURL("http://" + net.JoinHostPort(host, port))
}

func normalizeGatewayURL(raw string) (string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(raw), "/")
	u, err := url.ParseRequestURI(baseURL)
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("gateway public URL must use http or https")
	}
	if u.Host == "" {
		return "", fmt.Errorf("gateway public URL is missing host")
	}
	return baseURL, nil
}
