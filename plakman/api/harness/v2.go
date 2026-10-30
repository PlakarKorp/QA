// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// V2 speaks the v2 API over HTTP as one badge. There is no pcpctl for v2 yet,
// so the v2 scenarios drive the wire directly and assert the documented
// envelope: a 2xx with the resource, or {code, message, details}.
type V2 struct {
	Base  string
	Token string
	HTTP  *http.Client
}

// ErrorEnvelope is the one error shape every v2 endpoint answers with.
type ErrorEnvelope struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// The v2 error codes a scenario asserts, with the status each one carries.
const (
	CodeInvalidArgument  = "invalid_argument"  // 400
	CodeUnauthenticated  = "unauthenticated"   // 401
	CodePermissionDenied = "permission_denied" // 403
	CodeNotFound         = "not_found"         // 404
	CodeConflict         = "conflict"          // 409
	CodeGroupInUse       = "group_in_use"      // 409
)

var codeStatus = map[string]int{
	CodeInvalidArgument:  http.StatusBadRequest,
	CodeUnauthenticated:  http.StatusUnauthorized,
	CodePermissionDenied: http.StatusForbidden,
	CodeNotFound:         http.StatusNotFound,
	CodeConflict:         http.StatusConflict,
	CodeGroupInUse:       http.StatusConflict,
}

// LoginV2 trades the instance's API key for a v2 badge, the way an automation
// would.
func LoginV2(inst *Instance) (*V2, error) {
	c := &V2{Base: strings.TrimRight(inst.PlakmanURL, "/"), HTTP: &http.Client{Timeout: 30 * time.Second}}
	status, raw, err := c.do(http.MethodPost, "/api/v2/auth/login/apikey", map[string]string{"api_key": inst.APIKey})
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("v2 apikey login answered %d: %s", status, raw)
	}
	var token struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &token); err != nil || token.Token == "" {
		return nil, fmt.Errorf("v2 apikey login returned no token: %s", raw)
	}
	c.Token = token.Token
	return c, nil
}

// As returns a client holding another badge, for a scenario that re-scopes.
func (c *V2) As(token string) *V2 {
	return &V2{Base: c.Base, Token: token, HTTP: c.HTTP}
}

func (c *V2) do(method, path string, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, c.Base+path, reader)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return 0, nil, fmt.Errorf("%s %s: read: %w", method, path, err)
	}
	return resp.StatusCode, raw, nil
}

// Do performs one request and hands back the status and body; the caller
// decides what it expected.
func (c *V2) Do(t testing.TB, method, path string, body any) (int, []byte) {
	t.Helper()
	status, raw, err := c.do(method, path, body)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s %s -> %d", method, path, status)
	return status, raw
}

// OK performs a request that must answer 2xx and decodes the body into out
// when out is not nil.
func (c *V2) OK(t testing.TB, method, path string, body, out any) {
	t.Helper()
	status, raw := c.Do(t, method, path, body)
	if status < 200 || status >= 300 {
		t.Fatalf("%s %s: expected success, got %d: %s", method, path, status, raw)
	}
	if out == nil {
		return
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("%s %s: body is not the JSON expected: %v\n%s", method, path, err, raw)
	}
}

// Refused performs a request that must be refused with exactly this code, and
// with the status the code is documented to carry. It returns the envelope so
// a scenario can look at the details.
func (c *V2) Refused(t testing.TB, method, path string, body any, code string) ErrorEnvelope {
	t.Helper()
	status, raw := c.Do(t, method, path, body)
	var envelope ErrorEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Code == "" {
		t.Fatalf("%s %s: expected the v2 error envelope with code %q, got %d: %s", method, path, code, status, raw)
	}
	if envelope.Code != code {
		t.Fatalf("%s %s: expected code %q, got %q (%d): %s", method, path, code, envelope.Code, status, envelope.Message)
	}
	if want, known := codeStatus[code]; known && status != want {
		t.Fatalf("%s %s: code %q must carry %d, got %d", method, path, code, want, status)
	}
	return envelope
}

// Page is the v2 list envelope.
type Page[T any] struct {
	Total int `json:"total"`
	Items []T `json:"items"`
}

// List performs a GET that must answer a page of T.
func List[T any](t testing.TB, c *V2, path string) Page[T] {
	t.Helper()
	var page Page[T]
	c.OK(t, http.MethodGet, path, nil, &page)
	if page.Items == nil {
		t.Fatalf("GET %s: a list answers items: [], never null", path)
	}
	return page
}
