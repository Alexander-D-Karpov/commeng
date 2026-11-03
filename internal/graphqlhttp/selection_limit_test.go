package graphqlhttp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGraphQLRejectsExponentialFragmentExpansion(t *testing.T) {
	var query strings.Builder
	query.WriteString(`query { post(id:"1") { ...F25 } } fragment F0 on Post { id }`)
	for i := 1; i <= 25; i++ {
		_, _ = fmt.Fprintf(&query, " fragment F%d on Post { ...F%d ...F%d }", i, i-1, i-1)
	}
	body, err := json.Marshal(map[string]string{"query": query.String()})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	testHandler(t).Handler().ServeHTTP(response, request)
	if !strings.Contains(response.Body.String(), "too many expanded GraphQL selections") {
		t.Fatalf("fragment expansion was not rejected: status=%d body=%s", response.Code, response.Body.String())
	}
}
