package graphqlhttp

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Alexander-D-Karpov/commeng/internal/live"
	"github.com/Alexander-D-Karpov/commeng/internal/repository/memory"
	"github.com/Alexander-D-Karpov/commeng/internal/service"
)

func testHandler(t *testing.T) *Server {
	t.Helper()
	store := memory.New()
	svc := service.New(store, live.New(), "https://comment.akarpov.ru")
	handler, err := New(svc, Config{
		BaseURL:        "https://comment.akarpov.ru",
		SigningSecret:  make([]byte, 32),
		AllowedOrigins: []string{"https://blog.example.org"},
	})
	if err != nil {
		t.Fatalf("new HTTP server: %v", err)
	}
	return handler
}

func TestHTTPRouteRegistration(t *testing.T) {
	handler := testHandler(t).Handler()
	cases := []struct {
		name   string
		method string
		path   string
		want   int
	}{
		{name: "exact root", method: http.MethodGet, path: "/", want: http.StatusOK},
		{name: "schema", method: http.MethodGet, path: "/schema.graphql", want: http.StatusOK},
		{name: "liveness", method: http.MethodGet, path: "/healthz", want: http.StatusOK},
		{name: "readiness", method: http.MethodGet, path: "/readyz", want: http.StatusOK},
		{name: "GraphQL requires POST", method: http.MethodGet, path: "/graphql", want: http.StatusMethodNotAllowed},
		{name: "no admin frontend", method: http.MethodGet, path: "/admin", want: http.StatusNotFound},
		{name: "documentation redirect", method: http.MethodGet, path: "/docs", want: http.StatusPermanentRedirect},
		{name: "documentation home", method: http.MethodGet, path: "/docs/", want: http.StatusOK},
		{name: "documentation CSS", method: http.MethodGet, path: "/docs/styles.css", want: http.StatusOK},
		{name: "documentation JS", method: http.MethodGet, path: "/docs/app.js", want: http.StatusOK},
		{name: "root is GET-only", method: http.MethodPost, path: "/", want: http.StatusMethodNotAllowed},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(tt.method, tt.path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tt.want {
				t.Fatalf(
					"%s %s returned %d, want %d: %s",
					tt.method,
					tt.path,
					response.Code,
					tt.want,
					response.Body.String(),
				)
			}
		})
	}
}

func TestCORSAllowlist(t *testing.T) {
	handler := testHandler(t).Handler()
	cases := []struct {
		name   string
		origin string
		status int
	}{
		{name: "listed origin", origin: "https://blog.example.org", status: http.StatusNoContent},
		{name: "application origin", origin: "https://comment.akarpov.ru", status: http.StatusNoContent},
		{name: "unlisted origin", origin: "https://attacker.invalid", status: http.StatusForbidden},
		{name: "missing origin", origin: "", status: http.StatusNoContent},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodOptions, "/graphql", nil)
			if tt.origin != "" {
				request.Header.Set("Origin", tt.origin)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tt.status {
				t.Fatalf("status = %d, want %d, body=%s", response.Code, tt.status, response.Body.String())
			}
			if tt.status == http.StatusNoContent && tt.origin != "" &&
				response.Header().Get("Access-Control-Allow-Origin") != tt.origin {
				t.Fatalf("CORS origin = %q, want %q", response.Header().Get("Access-Control-Allow-Origin"), tt.origin)
			}
		})
	}
}

func TestLoopbackConsoleOrigins(t *testing.T) {
	store := memory.New()
	api := service.New(store, live.New(), "http://localhost:8080")
	server, err := New(api, Config{
		BaseURL:       "http://localhost:8080",
		SigningSecret: make([]byte, 32),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	for _, origin := range []string{
		"http://localhost:8080",
		"http://127.0.0.1:8080",
	} {
		request := httptest.NewRequest(http.MethodOptions, "/graphql", nil)
		request.Header.Set("Origin", origin)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusNoContent {
			t.Errorf("origin %q: status %d, want %d", origin, response.Code, http.StatusNoContent)
		}
	}
	request := httptest.NewRequest(http.MethodOptions, "/graphql", nil)
	request.Header.Set("Origin", "http://evil.invalid:8080")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Errorf("unexpected origin status = %d, want 403", response.Code)
	}
}

func TestUntrustedForwardedAddressIgnored(t *testing.T) {
	server := testHandler(t)
	_, network, err := net.ParseCIDR("172.18.0.1/32")
	if err != nil {
		t.Fatal(err)
	}
	server.config.TrustedProxy = network
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "192.0.2.10:54321"
	request.Header.Set("X-Forwarded-For", "203.0.113.5")
	if got := server.clientIP(request); got != "192.0.2.10" {
		t.Fatalf("untrusted peer IP = %s", got)
	}
	request.RemoteAddr = "172.18.0.1:54321"
	request.Header.Set("X-Forwarded-For", "198.51.100.9, 203.0.113.5")
	if got := server.clientIP(request); got != "203.0.113.5" {
		t.Fatalf("trusted proxy forwarded IP = %s", got)
	}
}

func TestAPIOnlyRoutes(t *testing.T) {
	handler := testHandler(t).Handler()
	for _, route := range []string{"/admin", "/assets/something.js", "/graphql/ide"} {
		request := httptest.NewRequest(http.MethodGet, route, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d; want 404", route, response.Code)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"service":"commeng"`) {
		t.Fatalf("API index status=%d, body=%s", response.Code, response.Body.String())
	}
}

type graphqlResponse struct {
	Data   map[string]json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func callGraphQL(t *testing.T, handler http.Handler, query, bearer, visitor string) graphqlResponse {
	t.Helper()
	body, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	if visitor != "" {
		request.Header.Set("X-Visitor-Token", visitor)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GraphQL status %d, body=%s", response.Code, response.Body.String())
	}
	var result graphqlResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode GraphQL: %v, body=%s", err, response.Body.String())
	}
	return result
}

func expectObject(t *testing.T, result graphqlResponse, field string) map[string]any {
	t.Helper()
	if len(result.Errors) != 0 {
		t.Fatalf("GraphQL errors: %+v", result.Errors)
	}
	var item map[string]any
	if err := json.Unmarshal(result.Data[field], &item); err != nil {
		t.Fatalf("decode %s: %v", field, err)
	}
	return item
}

func TestGraphQLRegisterLoginPostCommentAndDisable(t *testing.T) {
	handler := testHandler(t).Handler()
	response := callGraphQL(
		t,
		handler,
		`mutation {register(name:"Alice",email:"alice@example.org",password:"strong-password-123"){id name}}`,
		"",
		"",
	)
	if got := expectObject(t, response, "register")["name"]; got != "Alice" {
		t.Fatalf("registered name: %v", got)
	}
	response = callGraphQL(
		t,
		handler,
		`mutation {login(email:"alice@example.org",password:"strong-password-123"){token user{id}}}`,
		"",
		"",
	)
	session := expectObject(t, response, "login")["token"].(string)
	response = callGraphQL(
		t,
		handler,
		`mutation {createPost(input:{title:"Hello",body:"Post body"}){id commentsEnabled treeVersion}}`,
		session,
		"",
	)
	post := expectObject(t, response, "createPost")
	if post["id"] != "1" || post["commentsEnabled"] != true {
		t.Fatalf("unexpected post: %+v", post)
	}
	response = callGraphQL(t, handler, `mutation {
		createComment(input:{postId:"1",name:"Reader",email:"reader@example.org",body:"Great post"}) {
			id body permalink
		}
	}`, "", "")
	comment := expectObject(t, response, "createComment")
	if comment["body"] != "Great post" {
		t.Fatalf("unexpected comment: %+v", comment)
	}
	response = callGraphQL(
		t,
		handler,
		`mutation {setPostCommentsEnabled(postId:"1",enabled:false){commentsEnabled}}`,
		session,
		"",
	)
	if got := expectObject(t, response, "setPostCommentsEnabled")["commentsEnabled"]; got != false {
		t.Fatalf("post comments should be disabled, got %v", got)
	}
	logout := callGraphQL(t, handler, `mutation {logout}`, session, "")
	if len(logout.Errors) != 0 || string(logout.Data["logout"]) != "true" {
		t.Fatalf("logout failed: %+v", logout)
	}
	request := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":"{viewer{id}}"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+session)
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, request)
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session status = %d, want 401", denied.Code)
	}

	response = callGraphQL(t, handler, `mutation {
		createComment(input:{postId:"1",name:"Reader",email:"reader@example.org",body:"Blocked"}) { id }
	}`, "", "")
	if len(response.Errors) == 0 {
		t.Fatal("disabled post accepted a comment")
	}
}

func TestExpensiveAliasMutationRejected(t *testing.T) {
	handler := testHandler(t).Handler()
	// Each alias of login must be charged a full password-verification cost.
	query := `mutation {
  a: login(email:"ghost@example.org",password:"bad"){token}
  b: login(email:"ghost@example.org",password:"bad"){token}
  c: login(email:"ghost@example.org",password:"bad"){token}
  d: login(email:"ghost@example.org",password:"bad"){token}
  e: login(email:"ghost@example.org",password:"bad"){token}
 }`
	response := callGraphQL(t, handler, query, "", "")
	if len(response.Errors) == 0 {
		t.Fatal("five expensive aliases were not rejected")
	}
}

func TestServerConfigValidation(t *testing.T) {
	origins := []struct {
		value string
		valid bool
	}{
		{"https://blog.example.org", true},
		{"http://localhost:3000", true},
		{"https://blog.example.org/path", false},
		{"*", false},
		{"javascript:alert(1)", false},
	}
	for _, tt := range origins {
		t.Run(tt.value, func(t *testing.T) {
			_, err := ParseOrigins(tt.value)
			if (err == nil) != tt.valid {
				t.Fatalf("ParseOrigins(%q) = %v", tt.value, err)
			}
		})
	}
	if _, err := ParseSigningKey("bad"); err == nil {
		t.Fatal("invalid signing key accepted")
	}
	if _, err := ParseSigningKey("01"); err == nil {
		t.Fatal("too-short signing key accepted")
	}
	if _, err := ParseTrustedProxy("192.0.2.12"); err == nil {
		t.Fatal("CIDR required")
	}
	if _, err := ParseTrustedProxy("192.0.2.12/32"); err != nil {
		t.Fatalf("valid CIDR: %v", err)
	}
}
