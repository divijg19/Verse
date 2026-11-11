package push

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The push package holds the two credentials that reach outside the authoring environment, so its
// tests are about the shape of the requests it makes rather than about the publishing itself.
//
// What matters is that each request carries the right token, addresses the right URL, and carries
// the right body -- and that a failure is reported as a failure rather than swallowed. A push that
// silently did nothing would leave the public site showing the previous published set, with
// nothing in any log to say why.

// testConfig returns a configuration pointed at a test server.
func testConfig(t *testing.T, server *httptest.Server) Config {
	t.Helper()

	return Config{
		AccountID:   "test-account",
		NamespaceID: "test-namespace",
		Key:         "verse-public.json",
		APIToken:    "write-token",
		Repository:  "owner/repo",
		GitHubToken: "dispatch-token",
		APIBase:     server.URL,
		GitHubBase:  server.URL,
	}
}

// TestTheWriteStoresTheArtifactUnderTheConfiguredKey covers the request the store receives.
func TestTheWriteStoresTheArtifactUnderTheConfiguredKey(t *testing.T) {
	var gotPath, gotAuth, gotMethod, gotBody string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotMethod = r.Method

		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := testConfig(t, server)

	if err := cfg.Write(context.Background(), []byte(`{"published":true}`)); err != nil {
		t.Fatalf("write: %v", err)
	}

	if gotMethod != http.MethodPut {
		t.Errorf("the store was asked with %s, want PUT", gotMethod)
	}
	if !strings.HasSuffix(gotPath, "/values/verse-public.json") {
		t.Errorf("the store was asked for %q, want the artifact's key at the end of the path", gotPath)
	}
	if !strings.Contains(gotPath, "test-namespace") {
		t.Errorf("the store was asked for %q, want the namespace in the path", gotPath)
	}
	if gotAuth != "Bearer write-token" {
		t.Errorf("the store was asked with %q, want the configured token", gotAuth)
	}
	if gotBody != `{"published":true}` {
		t.Errorf("the store received %q, want the artifact verbatim", gotBody)
	}
}

// TestTheWriteFailsLoudly covers the failure.
//
// The write is the one step in this path that nothing else covers: the scheduled deploy reads the
// artifact out of the store, so a lost write means the site shows the previous published set. A
// silent success there would be the worst possible outcome, so a refusal has to be an error.
func TestTheWriteFailsLoudly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusForbidden)
	}))
	defer server.Close()

	// The backoff is shortened so the test does not wait out the real one. The retry count is
	// left alone, because the count is the thing under test.
	delay := writeRetryDelay
	writeRetryDelay = time.Millisecond
	defer func() { writeRetryDelay = delay }()

	cfg := testConfig(t, server)

	err := cfg.Write(context.Background(), []byte(`{}`))
	if err == nil {
		t.Fatal("the write succeeded against a store that refused it")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("the error does not say what the store answered: %v", err)
	}
}

// TestAnUnconfiguredStoreRefusesToWrite covers the missing configuration.
//
// A half-configured store would leave an artifact the deployment cannot find, which is worse than
// nothing having been written at all.
func TestAnUnconfiguredStoreRefusesToWrite(t *testing.T) {
	cfg := Config{AccountID: "test-account", NamespaceID: "test-namespace"}

	if err := cfg.Configured(); err == nil {
		t.Fatal("a configuration with no token reports itself as configured")
	}

	if err := cfg.Write(context.Background(), []byte(`{}`)); err == nil {
		t.Fatal("an unconfigured store accepted a write")
	}
}

// TestARefusalNamesTheVariableTheOperatorHasToSet covers the message.
//
// The two sides read different variables, and a refusal that named the write token to the
// deployment side would send the operator to set a credential the deployment has no use for.
func TestARefusalNamesTheVariableTheOperatorHasToSet(t *testing.T) {
	cfg := Config{AccountID: "test-account", NamespaceID: "test-namespace", TokenVar: ReadTokenVar}

	err := cfg.Configured()
	if err == nil {
		t.Fatal("a configuration with no token reports itself as configured")
	}
	if !strings.Contains(err.Error(), ReadTokenVar) {
		t.Errorf("the refusal does not name the variable to set: %v", err)
	}
	if strings.Contains(err.Error(), WriteTokenVar) {
		t.Errorf("the refusal names the write token to the read side: %v", err)
	}
}

// TestTheDispatchAsksTheWorkflowToRun covers the request GitHub receives.
//
// The event type is what the workflow listens for, and the digest is what lets it wait for the
// artifact it was dispatched for rather than the one that happened to be there.
func TestTheDispatchAsksTheWorkflowToRun(t *testing.T) {
	var gotPath, gotAuth, gotBody string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")

		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)

		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	cfg := testConfig(t, server)

	if err := cfg.Deploy(context.Background(), "abc123"); err != nil {
		t.Fatalf("deploy: %v", err)
	}

	if gotPath != "/repos/owner/repo/dispatches" {
		t.Errorf("the dispatch went to %q, want the repository's dispatch endpoint", gotPath)
	}
	if gotAuth != "Bearer dispatch-token" {
		t.Errorf("the dispatch was made with %q, want the configured token", gotAuth)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(gotBody), &payload); err != nil {
		t.Fatalf("the dispatch body is not JSON: %v", err)
	}
	if payload["event_type"] != "publish" {
		t.Errorf("the dispatch event type is %v, want publish", payload["event_type"])
	}

	client, ok := payload["client_payload"].(map[string]any)
	if !ok {
		t.Fatalf("the dispatch carries no client payload: %v", payload)
	}
	if client["digest"] != "abc123" {
		t.Errorf("the dispatch carries digest %v, want the one it was given", client["digest"])
	}
}

// TestTheDispatchFailsLoudly covers the refusal.
func TestTheDispatchFailsLoudly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusUnauthorized)
	}))
	defer server.Close()

	cfg := testConfig(t, server)

	err := cfg.Deploy(context.Background(), "abc123")
	if err == nil {
		t.Fatal("the dispatch succeeded against a repository that refused it")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("the error does not say what the repository answered: %v", err)
	}
}

// TestADispatchWithNoDigestCarriesNoPayload covers the scheduled case.
//
// The scheduled run has nothing particular to wait for, so it names no digest. Sending an empty one
// would make the workflow wait for an artifact that does not exist.
func TestADispatchWithNoDigestCarriesNoPayload(t *testing.T) {
	var gotBody string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	cfg := testConfig(t, server)

	if err := cfg.Deploy(context.Background(), ""); err != nil {
		t.Fatalf("deploy: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(gotBody), &payload); err != nil {
		t.Fatalf("the dispatch body is not JSON: %v", err)
	}
	if _, present := payload["client_payload"]; present {
		t.Errorf("the dispatch carries a client payload it has nothing to put in: %v", payload)
	}
}

// TestAMissingArtifactIsNotAnError covers the empty store.
//
// The author has not published anything yet. That is a state and not a failure, and a deployment
// that ran before it should succeed and do nothing.
func TestAMissingArtifactIsNotAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	cfg := testConfig(t, server)

	_, err := cfg.Read(context.Background())
	if err == nil {
		t.Fatal("reading an absent artifact succeeded")
	}
	if !strings.Contains(err.Error(), "no published set") {
		t.Errorf("the error does not say that there is nothing published: %v", err)
	}
}

// TestTheReadReturnsTheArtifact covers the deployment's read.
func TestTheReadReturnsTheArtifact(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"published":true}`))
	}))
	defer server.Close()

	cfg := testConfig(t, server)

	body, err := cfg.Read(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(body) != `{"published":true}` {
		t.Errorf("the read returned %q, want the artifact verbatim", body)
	}
}

// TestTheTimeoutIsBounded covers the claim the constant makes.
//
// A publish that hangs is worse than one that fails: the author is waiting on a response.
func TestTheTimeoutIsBounded(t *testing.T) {
	if Timeout <= 0 || Timeout > time.Minute {
		t.Errorf("the publish timeout is %v, want a positive value under a minute", Timeout)
	}
}

// TestAnOverrideMustPointAtTheLocalMachine covers the restriction.
//
// An override is a redirection: it says where the published set is written and where the deploy is
// requested. An unrestricted one would be a way to point the artifact at somebody else's host, and
// to carry the dispatch token to it.
func TestAnOverrideMustPointAtTheLocalMachine(t *testing.T) {
	t.Setenv(apiBaseEnv, "http://example.com")
	t.Setenv(githubBaseEnv, "http://localhost:4000")

	if _, err := ConfigFromEnv(WriteTokenVar); err == nil {
		t.Fatal("a remote override was accepted")
	}

	t.Setenv(apiBaseEnv, "http://127.0.0.1:8799")
	if _, err := ConfigFromEnv(WriteTokenVar); err != nil {
		t.Fatalf("a loopback override was refused: %v", err)
	}

	t.Setenv(apiBaseEnv, "http://localhost:8799/")
	if _, err := ConfigFromEnv(WriteTokenVar); err != nil {
		t.Fatalf("a loopback override was refused: %v", err)
	}
}

// TestNoOverrideMeansTheRealAPI covers the normal arrangement.
func TestNoOverrideMeansTheRealAPI(t *testing.T) {
	t.Setenv(apiBaseEnv, "")
	t.Setenv(githubBaseEnv, "")

	cfg, err := ConfigFromEnv(WriteTokenVar)
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if cfg.APIBase != "" || cfg.GitHubBase != "" {
		t.Errorf("the configuration names API roots %q and %q, want neither", cfg.APIBase, cfg.GitHubBase)
	}
}
