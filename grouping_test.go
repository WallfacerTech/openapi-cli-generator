package main

import "testing"

// A cross-section of a real account-scoped API: collections, nested
// collections, a multi-parent resource (`logs`), and singletons both directly
// under the account (`handbook`, `search`, `audit`, `connections`) and nested
// deeper (`commands`).
var testPaths = []string{
	"/v1/accounts",
	"/v1/accounts/{account_id}",
	"/v1/accounts/{account_id}/audit",
	"/v1/accounts/{account_id}/audit/policy",
	"/v1/accounts/{account_id}/connections",
	"/v1/accounts/{account_id}/handbook",
	"/v1/accounts/{account_id}/pages",
	"/v1/accounts/{account_id}/pages/{page_id}",
	"/v1/accounts/{account_id}/search",
	"/v1/accounts/{account_id}/vms",
	"/v1/accounts/{account_id}/vms/{vm_id}",
	"/v1/accounts/{account_id}/vms/{vm_id}/commands",
	"/v1/accounts/{account_id}/vms/{vm_id}/logs",
	"/v1/accounts/{account_id}/vms/{vm_id}/logs/{log}",
	"/v1/accounts/{account_id}/tasks/{task_id}/logs",
	"/v1/accounts/{account_id}/tasks/{task_id}/logs/{log}",
	"/v1/accounts/{account_id}/tasks",
	"/v1/accounts/{account_id}/tasks/{task_id}",
	"/v1/accounts/{account_id}/tasks/{task_id}/audit",
}

func TestPrecomputeResources(t *testing.T) {
	collections, multiParent, singletons := precomputeResources(testPaths)

	for _, seg := range []string{"accounts", "pages", "vms", "tasks", "logs"} {
		if !collections[seg] {
			t.Errorf("expected %q to be a collection resource", seg)
		}
	}
	for _, seg := range []string{"handbook", "search", "audit", "connections", "commands", "policy"} {
		if collections[seg] {
			t.Errorf("expected %q not to be a collection resource", seg)
		}
	}

	if !multiParent["logs"] {
		t.Error("expected logs to be a multi-parent resource")
	}

	for _, seg := range []string{"handbook", "search", "audit", "connections"} {
		if !singletons[seg] {
			t.Errorf("expected %q to be a singleton resource", seg)
		}
	}
	// Nested singletons stay actions on their parent resource.
	if singletons["commands"] {
		t.Error("expected commands not to be a singleton resource")
	}
	// Sub-paths of a singleton are actions on it, not groups of their own.
	if singletons["policy"] {
		t.Error("expected policy not to be a singleton resource")
	}
}

func TestDeriveGroupAndAction(t *testing.T) {
	collections, multiParent, singletons := precomputeResources(testPaths)

	cases := []struct {
		path   string
		method string
		group  string
		action string
	}{
		// Singleton resources become their own group, and their actions come
		// from the HTTP method so two methods on one path cannot collide.
		{"/v1/accounts/{account_id}/handbook", "GET", "handbook", "get"},
		{"/v1/accounts/{account_id}/handbook", "PATCH", "handbook", "update"},
		{"/v1/accounts/{account_id}/search", "GET", "search", "get"},
		{"/v1/accounts/{account_id}/audit", "GET", "audit", "get"},
		{"/v1/accounts/{account_id}/audit/policy", "GET", "audit", "policy"},
		// A plural singleton reads as a collection.
		{"/v1/accounts/{account_id}/connections", "GET", "connections", "list"},
		{"/v1/accounts/{account_id}/connections", "POST", "connections", "create"},

		// Collections are unchanged.
		{"/v1/accounts", "GET", "accounts", "list"},
		{"/v1/accounts", "POST", "accounts", "create"},
		{"/v1/accounts/{account_id}", "GET", "accounts", "get"},
		{"/v1/accounts/{account_id}/pages", "GET", "pages", "list"},
		{"/v1/accounts/{account_id}/pages/{page_id}", "DELETE", "pages", "delete"},
		{"/v1/accounts/{account_id}/tasks/{task_id}", "PATCH", "tasks", "update"},
		// The same noun deeper in a path stays an action on its parent, so
		// task audit does not collide with account audit.
		{"/v1/accounts/{account_id}/tasks/{task_id}/audit", "GET", "tasks", "audit"},
		// Nested singletons stay actions on the parent resource.
		{"/v1/accounts/{account_id}/vms/{vm_id}/commands", "POST", "vms", "commands"},
		// Multi-parent resources fall back to their parent group.
		{"/v1/accounts/{account_id}/vms/{vm_id}/logs", "GET", "vms", "logs"},
		{"/v1/accounts/{account_id}/vms/{vm_id}/logs/{log}", "GET", "vms", "log"},
	}

	for _, c := range cases {
		group, action := deriveGroupAndAction(c.path, c.method, collections, multiParent, singletons)
		if group != c.group || action != c.action {
			t.Errorf("%s %s: got %q %q, want %q %q", c.method, c.path, group, action, c.group, c.action)
		}
	}
}

// Every (group, action) pair a spec produces has to be unique, or cobra keeps
// only the first-registered command and silently shadows the rest. The pairs
// below are the operations that actually exist on these paths.
func TestNoGroupActionCollisions(t *testing.T) {
	collections, multiParent, singletons := precomputeResources(testPaths)

	operations := []struct {
		path   string
		method string
	}{
		{"/v1/accounts", "GET"},
		{"/v1/accounts", "POST"},
		{"/v1/accounts/{account_id}", "GET"},
		{"/v1/accounts/{account_id}", "PATCH"},
		{"/v1/accounts/{account_id}", "DELETE"},
		{"/v1/accounts/{account_id}/audit", "GET"},
		{"/v1/accounts/{account_id}/audit/policy", "GET"},
		{"/v1/accounts/{account_id}/connections", "GET"},
		{"/v1/accounts/{account_id}/handbook", "GET"},
		{"/v1/accounts/{account_id}/handbook", "PATCH"},
		{"/v1/accounts/{account_id}/pages", "GET"},
		{"/v1/accounts/{account_id}/pages", "POST"},
		{"/v1/accounts/{account_id}/pages/{page_id}", "GET"},
		{"/v1/accounts/{account_id}/pages/{page_id}", "PATCH"},
		{"/v1/accounts/{account_id}/pages/{page_id}", "DELETE"},
		{"/v1/accounts/{account_id}/search", "GET"},
		{"/v1/accounts/{account_id}/vms", "GET"},
		{"/v1/accounts/{account_id}/vms", "POST"},
		{"/v1/accounts/{account_id}/vms/{vm_id}", "GET"},
		{"/v1/accounts/{account_id}/vms/{vm_id}", "DELETE"},
		{"/v1/accounts/{account_id}/vms/{vm_id}/commands", "POST"},
		{"/v1/accounts/{account_id}/vms/{vm_id}/logs", "GET"},
		{"/v1/accounts/{account_id}/vms/{vm_id}/logs/{log}", "GET"},
		{"/v1/accounts/{account_id}/tasks", "GET"},
		{"/v1/accounts/{account_id}/tasks", "POST"},
		{"/v1/accounts/{account_id}/tasks/{task_id}", "GET"},
		{"/v1/accounts/{account_id}/tasks/{task_id}", "PATCH"},
		{"/v1/accounts/{account_id}/tasks/{task_id}/audit", "GET"},
	}

	seen := map[string]string{}
	for _, op := range operations {
		group, action := deriveGroupAndAction(op.path, op.method, collections, multiParent, singletons)
		key := group + " " + action
		if prev, ok := seen[key]; ok {
			t.Errorf("collision on %q: %s and %s %s", key, prev, op.method, op.path)
			continue
		}
		seen[key] = op.method + " " + op.path
	}
}
