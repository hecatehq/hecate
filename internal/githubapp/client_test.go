package githubapp

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const fixtureSHA = "0123456789012345678901234567890123456789"
const fixtureBaseSHA = "abcdefabcdefabcdefabcdefabcdefabcdefabcd"

func testClient(t *testing.T) (*Client, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "app.pem")
	if err := os.WriteFile(file, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600); err != nil {
		t.Fatal(err)
	}
	c := New(Config{ClientID: "Iv1.app", InstallationID: 12, PrivateKeyFile: file, Repositories: []string{"owner/repo"}})
	c.now = func() time.Time { return time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC) }
	return c, key
}

func response(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func tokenFixture(c *Client) string {
	b, _ := json.Marshal(map[string]any{"token": "secret-installation-token", "expires_at": c.now().Add(time.Hour), "repositories": []map[string]string{{"full_name": "owner/repo"}}, "permissions": map[string]string{"checks": "read", "pull_requests": "read", "statuses": "read", "metadata": "read"}})
	return string(b)
}
func prFixture() string {
	return `{"number":7,"state":"open","draft":false,"merged":false,"head":{"sha":"` + fixtureSHA + `"},"base":{"sha":"` + fixtureBaseSHA + `","repo":{"full_name":"owner/repo"}}}`
}

func TestReference_RejectsUntrustedURLShapes(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"http://github.com/owner/repo/pull/7", "https://github.com.evil/owner/repo/pull/7", "https://github.com:443/owner/repo/pull/7", "https://user@github.com/owner/repo/pull/7", "https://github.com/owner/repo/pull/7?x=1", "https://github.com/owner/repo/pull/7#x", "https://github.com/owner/repo/pull/007", "https://github.com/owner/repo/pull/0", "https://github.com/owner/repo/pull/7/", "https://github.com/owner%2Frepo/x/pull/7", "https://github.com/../repo/pull/7", "https://github.com/owner/repo/pull/7?"} {
		if _, err := ParseReference(raw); !errors.Is(err, ErrInvalidReference) {
			t.Errorf("accepted %q", raw)
		}
	}
	ref, err := ParseReference("https://github.com/owner/repo/pull/7")
	if err != nil || ref.FullName() != "owner/repo" || ref.Number != 7 {
		t.Fatalf("reference=%+v error=%v", ref, err)
	}
}

func TestClient_InspectPinsScopeHeadAndCredentials(t *testing.T) {
	c, key := testClient(t)
	var paths []string
	c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "api.github.com" {
			t.Fatalf("unexpected endpoint %s", r.URL)
		}
		paths = append(paths, r.Method+" "+r.URL.RequestURI())
		if r.URL.Path == "/app/installations/12/access_tokens" {
			jwt := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			parts := strings.Split(jwt, ".")
			if len(parts) != 3 {
				t.Fatal("missing JWT")
			}
			signature, _ := base64.RawURLEncoding.DecodeString(parts[2])
			digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
			if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], signature); err != nil {
				t.Fatal(err)
			}
			payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
			var claims struct {
				Iss      string
				Iat, Exp int64
			}
			if err := json.Unmarshal(payload, &claims); err != nil {
				t.Fatal(err)
			}
			if claims.Iss != "Iv1.app" || claims.Iat != c.now().Add(-time.Minute).Unix() || claims.Exp != c.now().Add(5*time.Minute).Unix() {
				t.Fatalf("claims=%+v", claims)
			}
			var request struct {
				Repositories []string
				Permissions  map[string]string
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if len(request.Repositories) != 1 || request.Repositories[0] != "repo" || len(request.Permissions) != 4 {
				t.Fatalf("scope=%+v", request)
			}
			for _, level := range request.Permissions {
				if level != "read" {
					t.Fatal("write token requested")
				}
			}
			return response(tokenFixture(c)), nil
		}
		if r.Header.Get("Authorization") != "Bearer secret-installation-token" {
			t.Fatal("installation credential not isolated")
		}
		switch r.URL.Path {
		case "/installation/token":
			if r.Method != "DELETE" {
				t.Fatal("missing revocation")
			}
			return response(""), nil
		case "/repos/owner/repo/pulls/7":
			return response(prFixture()), nil
		case "/repos/owner/repo/commits/" + fixtureSHA + "/check-runs":
			return response(`{"total_count":1,"check_runs":[{"name":"test","status":"completed","conclusion":"success","head_sha":"` + fixtureSHA + `"}]}`), nil
		case "/repos/owner/repo/commits/" + fixtureSHA + "/status":
			return response(`{"sha":"` + fixtureSHA + `","total_count":1,"statuses":[{"context":"deploy","state":"success"}]}`), nil
		default:
			t.Fatalf("unexpected request %s", r.URL)
			return nil, nil
		}
	})
	got, err := c.Inspect(t.Context(), "https://github.com/owner/repo/pull/7")
	if err != nil {
		t.Fatal(err)
	}
	if got.ChecksState != "success" || !got.Complete || len(got.Checks) != 2 || got.HeadSHA != fixtureSHA {
		t.Fatalf("inspection=%+v", got)
	}
	if len(paths) != 6 || paths[len(paths)-1] != "DELETE /installation/token" {
		t.Fatalf("requests=%v", paths)
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), c.config.PrivateKeyFile) {
		t.Fatal("secret in public result")
	}
}

func TestClient_RejectsScopeExpansionAndRevokes(t *testing.T) {
	for _, kind := range []string{"write", "other_repository", "extra_repository", "missing_permission", "expired"} {
		t.Run(kind, func(t *testing.T) {
			c, _ := testClient(t)
			revoked := false
			var credential map[string]any
			if err := json.Unmarshal([]byte(tokenFixture(c)), &credential); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "write":
				credential["permissions"].(map[string]any)["contents"] = "write"
			case "other_repository":
				credential["repositories"] = []map[string]string{{"full_name": "other/repo"}}
			case "extra_repository":
				credential["repositories"] = []map[string]string{{"full_name": "owner/repo"}, {"full_name": "owner/other"}}
			case "missing_permission":
				delete(credential["permissions"].(map[string]any), "checks")
			case "expired":
				credential["expires_at"] = c.now()
			}
			body, _ := json.Marshal(credential)
			c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method == "DELETE" {
					revoked = true
					return response(""), nil
				}
				if r.URL.Path != "/app/installations/12/access_tokens" {
					t.Fatal("used overbroad token")
				}
				return response(string(body)), nil
			})
			if _, err := c.Inspect(t.Context(), "https://github.com/owner/repo/pull/7"); !errors.Is(err, ErrScope) {
				t.Fatalf("error=%v", err)
			}
			if !revoked {
				t.Fatal("token not revoked")
			}
		})
	}
}

func TestClient_RejectsDriftAndNeverTreatsTruncationAsSuccess(t *testing.T) {
	for _, kind := range []string{"drift", "truncated", "wrong_check_sha", "unknown", "no_checks"} {
		t.Run(kind, func(t *testing.T) {
			c, _ := testClient(t)
			reads := 0
			c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				switch {
				case r.Method == "POST":
					return response(tokenFixture(c)), nil
				case r.Method == "DELETE":
					return response(""), nil
				case strings.Contains(r.URL.Path, "/pulls/"):
					reads++
					body := prFixture()
					if kind == "drift" && reads == 2 {
						body = strings.ReplaceAll(body, fixtureSHA, fixtureBaseSHA)
					}
					return response(body), nil
				case strings.HasSuffix(r.URL.Path, "/status"):
					return response(`{"sha":"` + fixtureSHA + `","total_count":0,"statuses":[]}`), nil
				default:
					body := `{"total_count":1,"check_runs":[{"name":"test","status":"completed","conclusion":"success","head_sha":"` + fixtureSHA + `"}]}`
					if kind == "truncated" {
						body = strings.Replace(body, `"total_count":1`, `"total_count":101`, 1)
					}
					if kind == "wrong_check_sha" {
						body = strings.ReplaceAll(body, fixtureSHA, fixtureBaseSHA)
					}
					if kind == "unknown" {
						body = strings.Replace(body, `"conclusion":"success"`, `"conclusion":"future"`, 1)
					}
					if kind == "no_checks" {
						body = `{"total_count":0,"check_runs":[]}`
					}
					return response(body), nil
				}
			})
			got, err := c.Inspect(t.Context(), "https://github.com/owner/repo/pull/7")
			switch kind {
			case "drift":
				if !errors.Is(err, ErrHeadChanged) {
					t.Fatalf("error=%v", err)
				}
			case "wrong_check_sha":
				if !errors.Is(err, ErrResponse) {
					t.Fatalf("error=%v", err)
				}
			default:
				if err != nil || got.ChecksState == "success" {
					t.Fatalf("inspection=%+v error=%v", got, err)
				}
			}
		})
	}
}

func TestClient_ErrorsAreBoundedAndDoNotReflectBodies(t *testing.T) {
	c, _ := testClient(t)
	for _, status := range []int{301, 401, 403, 404, 429, 500} {
		t.Run(strconvStatus(status), func(t *testing.T) {
			c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				res := response("secret echoed upstream")
				res.StatusCode = status
				res.Header.Set("Retry-After", "120")
				res.Header.Set("Location", "https://evil.example")
				return res, nil
			})
			_, err := c.Inspect(t.Context(), "https://github.com/owner/repo/pull/7")
			var upstream *HTTPError
			if !errors.As(err, &upstream) || upstream.Status != status || upstream.RetryAfter != 120 || strings.Contains(err.Error(), "secret") {
				t.Fatalf("error=%v", err)
			}
		})
	}
	c.http.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		return response(strings.Repeat("x", maxResponseBytes+1)), nil
	})
	if _, err := c.Inspect(t.Context(), "https://github.com/owner/repo/pull/7"); !errors.Is(err, ErrResponse) {
		t.Fatalf("oversize error=%v", err)
	}
}

func strconvStatus(status int) string { return http.StatusText(status) }

func TestClient_DeniedRepositoryDoesNotReadCredentialsOrConnect(t *testing.T) {
	c := New(Config{ClientID: "app", InstallationID: 12, PrivateKeyFile: filepath.Join(t.TempDir(), "private.pem"), Repositories: []string{"owner/repo"}})
	c.http.Transport = transportFunc(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected network"); return nil, nil })
	if _, err := c.Inspect(t.Context(), "https://github.com/owner/other/pull/7"); !errors.Is(err, ErrRepositoryDenied) {
		t.Fatalf("error=%v", err)
	}
}

func TestClient_ConcurrencyGateRejectsBeforeCredentialWork(t *testing.T) {
	c := New(Config{ClientID: "app", InstallationID: 12, PrivateKeyFile: filepath.Join(t.TempDir(), "private.pem"), Repositories: []string{"owner/repo"}})
	c.slots <- struct{}{}
	c.slots <- struct{}{}
	if _, err := c.Inspect(context.Background(), "https://github.com/owner/repo/pull/7"); !errors.Is(err, ErrBusy) {
		t.Fatalf("error=%v", err)
	}
}

func TestClient_RejectsRelativeCredentialPath(t *testing.T) {
	c, _ := testClient(t)
	c.config.PrivateKeyFile = "app.pem"
	if !errors.Is(c.config.Validate(), ErrInvalidConfiguration) {
		t.Fatal("relative credential path accepted")
	}
}
