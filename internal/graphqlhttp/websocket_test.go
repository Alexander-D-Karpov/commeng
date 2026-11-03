package graphqlhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/Alexander-D-Karpov/commeng/internal/auth"
)

func TestWebsocketSubscriptionWithCrossOriginAndInitAuth(t *testing.T) {
	api := testHandler(t)
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	address := "ws" + strings.TrimPrefix(server.URL, "http") + "/graphql"
	conn, _, err := coderws.Dial(ctx, address, &coderws.DialOptions{
		Subprotocols: []string{"graphql-transport-ws"},
		HTTPHeader:   http.Header{"Origin": []string{"https://blog.example.org"}},
	})
	if err != nil {
		t.Fatalf("websocket handshake: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.Close(coderws.StatusNormalClosure, "test complete"); err != nil {
			t.Logf("close test websocket: %v", err)
		}
	})
	visitorToken, err := api.visitors.Issue()
	if err != nil {
		t.Fatalf("visitor token: %v", err)
	}
	init := map[string]any{"type": "connection_init", "payload": map[string]string{"X-Visitor-Token": visitorToken}}
	if err := wsjson.Write(ctx, conn, init); err != nil {
		t.Fatalf("connection init: %v", err)
	}
	var acknowledge map[string]any
	if err := wsjson.Read(ctx, conn, &acknowledge); err != nil {
		t.Fatalf("connection ack: %v", err)
	}
	if acknowledge["type"] != "connection_ack" {
		t.Fatalf("unexpected websocket ack: %+v", acknowledge)
	}
	registerQuery := `mutation {
		register(name:"Alice",email:"alice@example.org",password:"strong-password-123") { id }
	}`
	registered := callGraphQL(t, api.Handler(), registerQuery, "", "")
	if len(registered.Errors) != 0 {
		t.Fatalf("registration: %+v", registered.Errors)
	}
	login := callGraphQL(
		t,
		api.Handler(),
		`mutation {login(email:"alice@example.org",password:"strong-password-123"){token}}`,
		"",
		"",
	)
	token := expectObject(t, login, "login")["token"].(string)
	post := callGraphQL(t, api.Handler(), `mutation {createPost(input:{title:"Welcome",body:"Post"}){id}}`, token, "")
	postID := expectObject(t, post, "createPost")["id"].(string)
	operation := map[string]any{
		"id": "watch", "type": "subscribe",
		"payload": map[string]string{"query": `subscription {commentAdded(postId:"` + postID + `"){id body}}`},
	}
	if err := wsjson.Write(ctx, conn, operation); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	until := time.Now().Add(2 * time.Second)
	for api.service.Events.SubscriberCount(1) == 0 && time.Now().Before(until) {
		time.Sleep(5 * time.Millisecond)
	}
	if api.service.Events.SubscriberCount(1) == 0 {
		t.Fatal("subscription did not register")
	}
	inserted := callGraphQL(
		t,
		api.Handler(),
		`mutation {createComment(input:{postId:"`+postID+`",name:"Reader",email:"reader@example.org",body:"Live"}){id}}`,
		"",
		"",
	)
	if len(inserted.Errors) != 0 {
		t.Fatalf("insert: %+v", inserted.Errors)
	}
	var next map[string]json.RawMessage
	if err := wsjson.Read(ctx, conn, &next); err != nil {
		t.Fatalf("read subscription event: %v", err)
	}
	if string(next["type"]) != `"next"` {
		t.Fatalf("unexpected event: %+v", next)
	}
	if !strings.Contains(string(next["payload"]), `"Live"`) {
		t.Fatalf("missing comment: %s", next["payload"])
	}
}

func TestWebSocketOperationsLimitedAfterHandshake(t *testing.T) {
	api := testHandler(t)
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/graphql", &coderws.DialOptions{
		Subprotocols: []string{"graphql-transport-ws"},
	})
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	defer func() { _ = conn.Close(coderws.StatusNormalClosure, "done") }()
	if err := wsjson.Write(ctx, conn, map[string]any{"type": "connection_init"}); err != nil {
		t.Fatal(err)
	}
	var ack struct {
		Type string `json:"type"`
	}
	if err := wsjson.Read(ctx, conn, &ack); err != nil || ack.Type != "connection_ack" {
		t.Fatalf("initialization: %+v %v", ack, err)
	}
	for i := 0; i <= maxWebSocketOperationsMinute; i++ {
		id := fmt.Sprintf("op-%d", i)
		if err := wsjson.Write(ctx, conn, map[string]any{
			"id": id, "type": "subscribe",
			"payload": map[string]string{"query": `query { posts(first:1) { edges { node { id } } } }`},
		}); err != nil {
			t.Fatal(err)
		}
		var response struct {
			ID      string          `json:"id"`
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := wsjson.Read(ctx, conn, &response); err != nil {
			t.Fatal(err)
		}
		if response.ID != id {
			t.Fatalf("response %d: %+v", i, response)
		}
		if i == maxWebSocketOperationsMinute {
			if (response.Type != "next" && response.Type != "error") ||
				!strings.Contains(string(response.Payload), "websocket operation limit") {
				t.Fatalf("did not rate-limit established WebSocket operation: %+v", response)
			}
		} else if response.Type != "next" || strings.Contains(string(response.Payload), `"errors"`) {
			t.Fatalf("allowed operation %d rejected: %+v", i, response)
		}
		if response.Type == "next" {
			var complete struct {
				ID   string `json:"id"`
				Type string `json:"type"`
			}
			if err := wsjson.Read(ctx, conn, &complete); err != nil {
				t.Fatal(err)
			}
			if complete.ID != id || complete.Type != "complete" {
				t.Fatalf("unexpected completion: %+v", complete)
			}
		}

	}
}

func TestWebSocketRejectsRevokedSession(t *testing.T) {
	api, token, _, _ := discussionFixture(t)
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/graphql", &coderws.DialOptions{
		Subprotocols: []string{"graphql-transport-ws"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(coderws.StatusNormalClosure, "done") }()
	if err := wsjson.Write(ctx, conn, map[string]any{
		"type": "connection_init", "payload": map[string]string{"Authorization": "Bearer " + token},
	}); err != nil {
		t.Fatal(err)
	}
	var ack struct {
		Type string `json:"type"`
	}
	if err := wsjson.Read(ctx, conn, &ack); err != nil || ack.Type != "connection_ack" {
		t.Fatalf("connection ack: %+v, %v", ack, err)
	}
	if err := wsjson.Write(ctx, conn, map[string]any{
		"id": "valid", "type": "subscribe", "payload": map[string]string{"query": `{viewer{id name}}`},
	}); err != nil {
		t.Fatal(err)
	}
	var valid struct {
		ID      string          `json:"id"`
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := wsjson.Read(ctx, conn, &valid); err != nil || valid.ID != "valid" || valid.Type != "next" ||
		!strings.Contains(string(valid.Payload), `"name":"Owner"`) {
		t.Fatalf("valid session rejected: %+v, %v", valid, err)
	}
	var complete struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	if err := wsjson.Read(ctx, conn, &complete); err != nil || complete.ID != "valid" || complete.Type != "complete" {
		t.Fatalf("valid operation completion: %+v, %v", complete, err)
	}
	if err := api.service.Store.DeleteSession(ctx, auth.TokenHash(token)); err != nil {
		t.Fatal(err)
	}
	if err := wsjson.Write(ctx, conn, map[string]any{
		"id": "revoked", "type": "subscribe", "payload": map[string]string{
			"query": `mutation {createPost(input:{title:"Forbidden",body:"Revoked session"}){id}}`,
		},
	}); err != nil {
		t.Fatal(err)
	}
	var response struct {
		ID      string          `json:"id"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := wsjson.Read(ctx, conn, &response); err != nil {
		t.Fatal(err)
	}
	if response.ID != "revoked" || !strings.Contains(string(response.Payload), "UNAUTHENTICATED") {
		t.Fatalf("revoked session accepted: %+v", response)
	}
}
