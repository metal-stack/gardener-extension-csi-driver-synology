package synology

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

type Client struct {
	baseURL    *url.URL
	username   string
	password   string
	sessionID  string
	synoToken  string
	httpClient *http.Client
}

func NewClient(base, username, password string) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("invalid base url %q: %w", base, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid base url %q: missing scheme/host", base)
	}

	return &Client{
		baseURL:  u,
		username: username,
		password: password,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				// dev-friendly; for production you should validate certs
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
	}, nil
}

func (c *Client) webapiURL(file string) string {
	u := *c.baseURL // copy
	u.Path = path.Join(u.Path, "/webapi/", file)
	return u.String()
}

type apiError struct {
	Code int `json:"code"`
}

type loginResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Account   string `json:"account"`
		SID       string `json:"sid"`
		SynoToken string `json:"synotoken"`
	} `json:"data"`
	Error *apiError `json:"error,omitempty"`
}

type simpleResult struct {
	Success bool      `json:"success"`
	Error   *apiError `json:"error,omitempty"`
}

// Login authenticates with DSM and stores SID + SynoToken.
// This matches the working script:
// - session=Core
// - format=sid
// - enable_syno_token=yes
func (c *Client) Login() error {
	u, err := url.Parse(c.webapiURL("entry.cgi"))
	if err != nil {
		return fmt.Errorf("build login url: %w", err)
	}

	q := u.Query()
	q.Set("api", "SYNO.API.Auth")
	q.Set("version", "7")
	q.Set("method", "login")
	q.Set("account", c.username)
	q.Set("passwd", c.password)
	q.Set("session", "Core")
	q.Set("format", "sid")
	q.Set("enable_syno_token", "yes")
	u.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return fmt.Errorf("build login request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("login request failed: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read login response: %w", err)
	}

	var lr loginResponse
	if err := json.Unmarshal(body, &lr); err != nil {
		return fmt.Errorf("parse login response: %w (body=%q)", err, string(body))
	}

	if !lr.Success {
		code := -1
		if lr.Error != nil {
			code = lr.Error.Code
		}
		return fmt.Errorf("login failed (code=%d, body=%s)", code, string(body))
	}

	if lr.Data.SID == "" {
		return fmt.Errorf("login succeeded but sid is empty (body=%s)", string(body))
	}
	if lr.Data.SynoToken == "" {
		return fmt.Errorf("login succeeded but synotoken is empty (body=%s)", string(body))
	}

	c.sessionID = lr.Data.SID
	c.synoToken = lr.Data.SynoToken
	return nil
}

func (c *Client) ensureLogin() error {
	if c.sessionID != "" && c.synoToken != "" {
		return nil
	}
	return c.Login()
}

func decodeResult(body []byte, out any) error {
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("failed to parse response: %w (body=%q)", err, string(body))
	}
	return nil
}

func extractCode(r *simpleResult) int {
	if r == nil || r.Error == nil {
		return -1
	}
	return r.Error.Code
}

// CreateUser creates a new user on the Synology NAS.
// Uses GET + query params (like the working script) and sends X-SYNO-TOKEN.
func (c *Client) CreateUser(username, password string) error {
	if err := c.ensureLogin(); err != nil {
		return err
	}

	u, err := url.Parse(c.webapiURL("entry.cgi"))
	if err != nil {
		return fmt.Errorf("build create user url: %w", err)
	}

	q := u.Query()
	q.Set("api", "SYNO.Core.User")
	q.Set("version", "1")
	q.Set("method", "create")
	q.Set("name", username)
	q.Set("password", password)
	q.Set("_sid", c.sessionID)
	u.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return fmt.Errorf("build create user request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-SYNO-TOKEN", c.synoToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	var result simpleResult
	if err := decodeResult(body, &result); err != nil {
		return err
	}

	if !result.Success {
		code := extractCode(&result)
		return fmt.Errorf("create user failed with error code: %d (body=%s)", code, string(body))
	}

	return nil
}

type getUserResponse struct {
	Success bool `json:"success"`
	Data    struct {
		// DSM liefert typischerweise "users": [ ... ]
		Users []User `json:"users"`
	} `json:"data"`
	Error *apiError `json:"error,omitempty"`
}

// User is a minimal representation of a DSM user record.
// (Field set can be expanded as needed.)
type User struct {
	Name string `json:"name,omitempty"`
}

// GetUser fetches a user by name using SYNO.Core.User/get.
// Returns (nil, nil) if the user does not exist (code 407).
func (c *Client) GetUser(username string) (*User, error) {
	if err := c.ensureLogin(); err != nil {
		return nil, err
	}

	u, err := url.Parse(c.webapiURL("entry.cgi"))
	if err != nil {
		return nil, fmt.Errorf("build get user url: %w", err)
	}

	q := u.Query()
	q.Set("api", "SYNO.Core.User")
	q.Set("version", "1")
	q.Set("method", "get")
	q.Set("name", username)
	q.Set("_sid", c.sessionID)
	u.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build get user request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-SYNO-TOKEN", c.synoToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get user request failed: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read get user response: %w", err)
	}

	var r getUserResponse
	if err := decodeResult(body, &r); err != nil {
		return nil, err
	}

	if !r.Success {
		code := -1
		if r.Error != nil {
			code = r.Error.Code
		}

		// user not found (consistent with your other handling)
		if code == 3106 {
			return nil, nil
		}

		return nil, fmt.Errorf("get user failed with error code: %d (body=%s)", code, string(body))
	}

	// DSM typically returns list; choose the first if present.
	if len(r.Data.Users) == 0 {
		// Defensive: success but empty result -> treat as not found
		return nil, nil
	}

	return &r.Data.Users[0], nil
}

// Logout ends the session.
// Uses session=Core and sends X-SYNO-TOKEN (safe) + _sid.
func (c *Client) Logout() error {
	if c.sessionID == "" {
		return nil
	}

	u, err := url.Parse(c.webapiURL("entry.cgi"))
	if err != nil {
		return fmt.Errorf("build logout url: %w", err)
	}

	q := u.Query()
	q.Set("api", "SYNO.API.Auth")
	q.Set("version", "7")
	q.Set("method", "logout")
	q.Set("session", "Core")
	q.Set("_sid", c.sessionID)
	u.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return fmt.Errorf("build logout request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if c.synoToken != "" {
		req.Header.Set("X-SYNO-TOKEN", c.synoToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// clear anyway
		c.sessionID, c.synoToken = "", ""
		return fmt.Errorf("logout request failed: %w", err)
	}
	_ = resp.Body.Close()

	c.sessionID = ""
	c.synoToken = ""
	return nil
}

// GenerateRandomPassword generates a random password.
func GenerateRandomPassword(length int) (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*"
	password := make([]byte, length)

	for i := range password {
		num, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", err
		}
		password[i] = charset[num.Int64()]
	}

	return string(password), nil
}

// GenerateShootUsername generates a username for a shoot cluster.
func GenerateShootUsername(shootName, shootNamespace string) string {
	// keep it simple: Synology usernames are typically restricted; avoid uppercase/specials
	s := fmt.Sprintf("gardener-%s-%s", shootNamespace, shootName)
	return strings.ToLower(s)
}
