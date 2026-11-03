package graphqlhttp

import (
	"encoding/json"
	"testing"
)

func readGraphQLList(t *testing.T, response graphqlResponse, field string) []any {
	t.Helper()
	connection := expectObject(t, response, field)
	edges, ok := connection["edges"].([]any)
	if !ok {
		t.Fatalf("%s has no edges: %+v", field, connection)
	}
	return edges
}

// End-to-end GraphQL fixture exercises edits, permission checks, indexed-search
// API, private notifications and durable per-post replay on the memory backend.
func TestGraphQLExtendedDiscussionLifecycle(t *testing.T) {
	api := testHandler(t).Handler()
	for _, actor := range []struct{ name, email string }{
		{"Alice", "alice-ext@example.org"}, {"Bob", "bob-ext@example.org"},
	} {
		response := callGraphQL(
			t,
			api,
			`mutation { register(name:"`+actor.name+`",email:"`+actor.email+`",password:"strong-password-123"){id} }`,
			"",
			"",
		)
		expectObject(t, response, "register")
	}
	authenticate := func(email string) string {
		t.Helper()
		response := callGraphQL(
			t,
			api,
			`mutation {login(email:"`+email+`",password:"strong-password-123"){token}}`,
			"",
			"",
		)
		return expectObject(t, response, "login")["token"].(string)
	}
	alice := authenticate("alice-ext@example.org")
	bob := authenticate("bob-ext@example.org")
	post := expectObject(t, callGraphQL(
		t,
		api,
		`mutation {createPost(input:{title:"Discussion",body:"A test"}){id updatedAt deleted}}`,
		alice,
		"",
	), "createPost")
	if post["id"] != "1" || post["updatedAt"] == "" || post["deleted"] != false {
		t.Fatalf("invalid post: %+v", post)
	}
	denied := callGraphQL(t, api, `mutation {editPost(input:{id:"1",title:"No",body:"No"}){id}}`, bob, "")
	if len(denied.Errors) == 0 {
		t.Fatal("non-owner edited post")
	}
	updated := expectObject(t, callGraphQL(
		t,
		api,
		`mutation {editPost(input:{id:"1",title:"Gopher update",body:"API search content"}){id title}}`,
		alice,
		"",
	), "editPost")
	if updated["title"] != "Gopher update" {
		t.Fatalf("post not edited: %+v", updated)
	}
	foundPosts := readGraphQLList(t, callGraphQL(t, api,
		`{searchPosts(query:"Gopher",first:10){edges{node{id title}}pageInfo{hasNextPage}}}`, "", ""), "searchPosts")
	if len(foundPosts) != 1 {
		t.Fatalf("searchPosts: %+v", foundPosts)
	}
	root := expectObject(t, callGraphQL(
		t,
		api,
		`mutation {createComment(input:{postId:"1",body:"Original searchable comment"}){id body}}`,
		alice,
		"",
	), "createComment")
	if root["id"] != "1" {
		t.Fatalf("expected root comment: %+v", root)
	}
	if len(callGraphQL(t, api, `mutation {editComment(id:"1",body:"No"){id}}`, bob, "").Errors) == 0 {
		t.Fatal("non-owner edited comment")
	}
	changed := expectObject(t, callGraphQL(t, api,
		`mutation {editComment(id:"1",body:"Edited searchable comment"){id editedAt}}`, alice, ""), "editComment")
	if changed["editedAt"] == nil {
		t.Fatalf("missing editedAt: %+v", changed)
	}
	reply := expectObject(t, callGraphQL(
		t,
		api,
		`mutation {createComment(input:{postId:"1",parentId:"1",body:"A reply"}){id parentId}}`,
		bob,
		"",
	), "createComment")
	if reply["parentId"] != "1" {
		t.Fatalf("wrong reply parent: %+v", reply)
	}
	foundComments := readGraphQLList(t, callGraphQL(t, api,
		`{searchComments(query:"searchable",postId:"1",first:10){edges{node{id body}}}}`, "", ""), "searchComments")
	if len(foundComments) != 1 {
		t.Fatalf("searchComments: %+v", foundComments)
	}
	notifications := readGraphQLList(t, callGraphQL(t, api,
		`{notifications(unreadOnly:true){edges{node{id postId commentId kind}}}}`, alice, ""), "notifications")
	if len(notifications) != 1 {
		t.Fatalf("notifications: %+v", notifications)
	}
	firstNotification := notifications[0].(map[string]any)["node"].(map[string]any)
	if firstNotification["commentId"] != "2" {
		t.Fatalf("unexpected notification: %+v", firstNotification)
	}
	if len(callGraphQL(t, api, `mutation {markNotificationsRead(ids:["1"])}`, bob, "").Errors) != 0 {
		t.Fatal("unrelated user's markNotificationsRead should be idempotent")
	}
	read := callGraphQL(t, api, `mutation {markNotificationsRead(ids:["1"])}`, alice, "")
	if string(read.Data["markNotificationsRead"]) != "1" || len(read.Errors) > 0 {
		t.Fatalf("mark read: %+v", read)
	}
	empty := readGraphQLList(t, callGraphQL(t, api,
		`{notifications(unreadOnly:true){edges{node{id}}}}`, alice, ""), "notifications")
	if len(empty) != 0 {
		t.Fatalf("read notification remains unread: %+v", empty)
	}
	eventResult := callGraphQL(t, api,
		`{commentEvents(postId:"1",first:10){edges{node{sequence comment{id}}}pageInfo{hasNextPage}}}`, "", "")
	events := readGraphQLList(t, eventResult, "commentEvents")
	if len(events) != 2 || events[1].(map[string]any)["node"].(map[string]any)["sequence"] != "2" {
		t.Fatalf("event replay: %+v", events)
	}
	deleteResult := expectObject(t, callGraphQL(t, api,
		`mutation {deleteComment(id:"1"){state body}}`, alice, ""), "deleteComment")
	if deleteResult["state"] != "DELETED" || deleteResult["body"] != nil {
		t.Fatalf("comment not redacted: %+v", deleteResult)
	}
	postResult := expectObject(t, callGraphQL(t, api,
		`mutation {deletePost(id:"1"){deleted commentsEnabled}}`, alice, ""), "deletePost")
	if postResult["deleted"] != true || postResult["commentsEnabled"] != false {
		t.Fatalf("post not tombstoned: %+v", postResult)
	}
	remaining := readGraphQLList(t, callGraphQL(t, api,
		`{searchPosts(query:"Gopher"){edges{node{id}}}}`, "", ""), "searchPosts")
	if len(remaining) != 0 {
		t.Fatalf("deleted post appeared in search: %+v", remaining)
	}
	var page map[string]any
	if err := json.Unmarshal(callGraphQL(t, api, `{post(id:"1"){id deleted}}`, "", "").Data["post"], &page); err != nil ||
		page["deleted"] != true {
		t.Fatalf("deleted post permalink unreadable: %+v err=%v", page, err)
	}
}
