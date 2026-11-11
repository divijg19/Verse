// Package push publishes the author's public site.
//
// The public site is a static artifact, and the question this package answers is how that artifact
// gets from the authoring environment to the public one.
//
// It is pushed rather than pulled. The authoring environment holds the database, so it is the only
// place the published set can be read from; a build that ran anywhere else would have to reach
// across the network to get it, which means a database credential in a build environment. That
// credential is exactly what leaked once already, through a build command that echoed it.
//
// So the authoring environment writes the published set out as a file, stores it, and asks for a
// deploy. The deployment environment fetches the file and builds the site from it. The build holds
// no database credential, and a deploy does not fail because the database is unreachable.
//
// # THE STORE IS CLOUDFLARE KV
//
// The artifact is stored in a Workers KV namespace, in the same account as the Worker that serves
// the site, and it is read and written over the REST API with a bearer token.
//
// That was not the first choice. Cloudflare R2 holds the same thing and is the more natural fit --
// it is an object store and the artifact is an object -- but R2 speaks the S3 API, and the S3 API
// signs every request with SigV4. Signing it by hand is a hundred and thirty lines of
// cryptography whose only real test is against production, and the AWS SDK that does it correctly
// adds twelve modules to a module graph that had nineteen lines.
//
// The REST API needs a bearer token and one HTTP call in each direction, and adds nothing at all.
// The artifact is a few kilobytes against KV's twenty-five-megabyte limit.
//
// KV is eventually consistent, and the code accounts for that rather than hoping. See Deploy.
//
// # WHAT IS STORED
//
// The published export: the works a reader can see, and nothing else. It is not the whole library,
// and it carries no superseded revisions -- see export.Options.PublishedOnly.
package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/divijg19/Verse/internal/export"
)

// The names of the two Cloudflare token variables.
//
// The authoring side writes the artifact and the deployment side reads it, and they are separate
// tokens: the one that writes can do nothing but write, and the one that reads can do nothing but
// read. Naming them here rather than at each call site means the two roles cannot be confused for
// one another, which is the mistake that would hand a read-only deployment a write credential.
const (
	// WriteTokenVar is the environment variable holding the token the authoring side writes with.
	//
	// It can write one key in one namespace and nothing else. The credential that deploys the
	// site is not this one, and never was: the deployment environment holds that.
	// #nosec G101 -- this is the name of an environment variable, not a credential. The
	// credential is what the operator puts in it, and it is never in this repository.
	WriteTokenVar = "CLOUDFLARE_KV_WRITE_TOKEN"

	// ReadTokenVar is the environment variable holding the token the deployment side reads with.
	//
	// It can read one key in one namespace and nothing else. The two tokens are separate because
	// the two sides are separate, and a single token would mean the deployment could overwrite the
	// artifact it is about to build from.
	// #nosec G101 -- the name of an environment variable, not a credential.
	ReadTokenVar = "CLOUDFLARE_KV_READ_TOKEN"
)

// ErrNotFound reports that the store holds no artifact.
//
// It is separate from every other error because it is the one that means "there is nothing to
// deploy" rather than "the deploy cannot proceed". A deployment that runs before the author has
// published anything should succeed and do nothing.
var ErrNotFound = errors.New("the artifact store holds no published set")

// Config describes where the artifact goes and who is told about it.
type Config struct {
	// AccountID is the Cloudflare account that owns the namespace.
	AccountID string

	// NamespaceID is the Workers KV namespace the artifact is written to.
	NamespaceID string

	// Key is the artifact's key within the namespace.
	Key string

	// APIToken is the token the request is made with. Which permission it needs depends on which
	// side this configuration is for: the Workers KV Storage Write permission on this one namespace
	// for the authoring side, and the Read permission for the deployment side.
	APIToken string

	// TokenVar is the name of the environment variable APIToken was read from.
	//
	// It is carried so that a refusal can name the variable the operator has to set. A
	// configuration that said "CLOUDFLARE_KV_WRITE_TOKEN is required" to the deployment side would
	// send the operator to set the wrong one.
	TokenVar string

	// Repository is the GitHub repository the deploy is requested in, as owner/name.
	Repository string

	// GitHubToken is the token the request is made with. It needs Contents: write on that one
	// repository and nothing else.
	GitHubToken string

	// APIBase is the root of the Cloudflare API. It is empty in production, which means the real
	// one; it is set by the tests, which is the only reason it exists.
	APIBase string

	// GitHubBase is the root of the GitHub API, with the same arrangement.
	GitHubBase string
}

// The environment variables that override the two API roots.
//
// They exist so that the push path can be exercised end to end against a local stand-in, without a
// Cloudflare account and without a GitHub repository. They are unset in production, and an unset
// one means the real API.
//
// They are restricted to a loopback address, and that restriction is the point. An override is a
// redirection: it says where the published set is written and where the deploy is requested. An
// unrestricted one would be a way to point the artifact at a host of somebody else's choosing, and
// a way to point the deploy request at one -- which would carry the dispatch token to it.
//
// The restriction costs nothing. A stand-in for these APIs is a local process, so a loopback
// address is the only one that was ever wanted, and a remote one is refused rather than honored.
// The check is on the host, so a stand-in behind a port forward on the same machine still works.
const (
	apiBaseEnv    = "VERSE_CLOUDFLARE_API_BASE"
	githubBaseEnv = "VERSE_GITHUB_API_BASE"
)

// localBase returns an override if it points at the local machine, and an error if it does not.
//
// An unset override is not an error: it means the real API, which is the normal arrangement.
func localBase(name, value string) (string, error) {
	if value == "" {
		return "", nil
	}

	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("%s is not a URL: %w", name, err)
	}

	host := parsed.Hostname()
	if host != "localhost" && !net.ParseIP(host).IsLoopback() {
		return "", fmt.Errorf("%s must point at the local machine; %q is not a loopback address", name, value)
	}

	return strings.TrimRight(value, "/"), nil
}

// ConfigFromEnv reads the push configuration from the environment.
//
// tokenVar names the environment variable that holds the Cloudflare token, because there are two
// tokens and they are not interchangeable. The authoring side writes the artifact with one and the
// deployment side reads it with another, and a single name would make it possible to hand the
// deployment the writing token without anybody noticing.
//
// The two credential sets are separate on purpose. The Cloudflare token can write one key in one
// namespace; the GitHub token can ask one workflow in one repository to run. Neither can do
// anything else, and neither is the database credential, which never leaves the authoring
// environment.
//
// The names are the ones an operator would set:
//
//	CLOUDFLARE_ACCOUNT_ID       the account that owns the namespace
//	CLOUDFLARE_KV_NAMESPACE_ID   the namespace the artifact is written to
//	CLOUDFLARE_API_TOKEN         the token the authoring side writes with
//	CLOUDFLARE_KV_READ_TOKEN     the token the deployment side reads with
//	GITHUB_REPOSITORY            owner/name of the repository the deploy is requested in
//	GITHUB_DISPATCH_TOKEN        the token the request is made with
func ConfigFromEnv(tokenVar string) (Config, error) {
	apiBase, err := localBase(apiBaseEnv, strings.TrimSpace(os.Getenv(apiBaseEnv)))
	if err != nil {
		return Config{}, err
	}

	githubBase, err := localBase(githubBaseEnv, strings.TrimSpace(os.Getenv(githubBaseEnv)))
	if err != nil {
		return Config{}, err
	}

	return Config{
		AccountID:   strings.TrimSpace(os.Getenv("CLOUDFLARE_ACCOUNT_ID")),
		NamespaceID: strings.TrimSpace(os.Getenv("CLOUDFLARE_KV_NAMESPACE_ID")),
		Key:         envOr("PUBLIC_ARTIFACT_KEY", "verse-public.json"),
		APIToken:    strings.TrimSpace(os.Getenv(tokenVar)),
		TokenVar:    tokenVar,
		Repository:  strings.TrimSpace(os.Getenv("GITHUB_REPOSITORY")),
		GitHubToken: strings.TrimSpace(os.Getenv("GITHUB_DISPATCH_TOKEN")),
		APIBase:     apiBase,
		GitHubBase:  githubBase,
	}, nil
}

// Configured reports whether the configuration is complete enough to publish.
//
// It is checked before anything is written, because a half-configured store would leave an
// artifact that the deployment cannot find, which is worse than nothing having been written at all.
func (c Config) Configured() error {
	if c.AccountID == "" || c.NamespaceID == "" || c.APIToken == "" {
		return fmt.Errorf("the artifact store is not configured: CLOUDFLARE_ACCOUNT_ID, "+
			"CLOUDFLARE_KV_NAMESPACE_ID and %s are all required", c.tokenVar())
	}
	if c.Key == "" {
		return errors.New("the artifact store has no key")
	}
	return nil
}

// tokenVar is the name of the environment variable the token was read from.
//
// A configuration built by hand rather than from the environment has no name to give, so the
// write one is used: it is the stricter of the two, and a refusal that names a variable the
// operator can set is more useful than one that names nothing.
func (c Config) tokenVar() string {
	if c.TokenVar == "" {
		return WriteTokenVar
	}
	return c.TokenVar
}

// valueURL is the address the artifact is written to and read from.
func (c Config) valueURL() string {
	base := c.APIBase
	if base == "" {
		base = "https://api.cloudflare.com/client/v4"
	}

	return fmt.Sprintf("%s/accounts/%s/storage/kv/namespaces/%s/values/%s",
		base,
		url.PathEscape(c.AccountID),
		url.PathEscape(c.NamespaceID),
		url.PathEscape(c.Key))
}

// Artifact is the published set, ready to be stored.
//
// It is the export's bytes and the digest of the published set those bytes describe. The digest
// is the answer to "has anything a reader can see changed", and it is carried alongside the bytes
// so that the two cannot come apart.
type Artifact struct {
	// Body is the published export, verbatim.
	Body []byte

	// Digest identifies the published set the body holds.
	Digest string
}

// BuildArtifact reads the published set and serializes it as the artifact the deployment
// consumes.
//
// The filter is the publisher's own, so a draft cannot reach the artifact even by accident, and
// the file carries no superseded revisions: the artifact holds what a reader can see and nothing
// else.
func BuildArtifact(ctx context.Context) (Artifact, error) {
	doc, err := export.Build(ctx, export.Options{PublishedOnly: true})
	if err != nil {
		return Artifact{}, fmt.Errorf("build the published export: %w", err)
	}

	body, err := export.JSON{}.Write(doc)
	if err != nil {
		return Artifact{}, fmt.Errorf("serialize the published export: %w", err)
	}

	return Artifact{Body: body, Digest: doc.Digest}, nil
}

// Write stores the published export.
//
// The value is sent as the request body rather than as a form part, because the artifact is one
// blob of bytes and the multipart form exists for sending several named things at once.
//
// A failed write is retried. The write is the one step in this path that nothing else covers: the
// scheduled deploy reads the artifact out of the store, so a lost write means the site shows the
// previous published set until the next publication. A store that answers 500 or refuses the
// connection is worth three tries, and the retries are close together because the artifact is
// small and the store is close.
//
// The artifact is written whole every time, so a retry cannot leave a partial one: the store
// replaces the value rather than appending to it.
func (c Config) Write(ctx context.Context, document []byte) error {
	if err := c.Configured(); err != nil {
		return err
	}

	var lastErr error
	for attempt := 1; attempt <= writeAttempts; attempt++ {
		if attempt > 1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(writeRetryDelay * time.Duration(attempt-1)):
			}
		}

		err := c.writeOnce(ctx, document)
		if err == nil {
			return nil
		}
		lastErr = err
	}

	return lastErr
}

// writeAttempts and writeRetryDelay are the write's retry shape.
//
// The delay is a variable rather than a constant so that a test can shorten it. A test that waited
// out the real backoff would take six seconds to prove one thing.
var (
	writeAttempts   = 3
	writeRetryDelay = 2 * time.Second
)

// writeOnce makes one attempt to store the artifact.
func (c Config) writeOnce(ctx context.Context, document []byte) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, c.valueURL(), bytes.NewReader(document))
	if err != nil {
		return fmt.Errorf("build the store request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.APIToken)
	request.Header.Set("Content-Type", "application/octet-stream")

	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("write the artifact: %w", err)
	}
	defer closeBody(response.Body)

	// The body is read even on a failure, because the store's own error message is the most
	// useful thing in it.
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("read the store's answer: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fmt.Errorf("write the artifact: the store answered %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	return nil
}

// Read fetches the published export.
//
// It is used by the deployment, which is why it is here rather than in the deployment's own
// workflow: the two sides of the store are the same two sides of the store, and the address is
// built in one place.
//
// A key that is not there is reported as ErrNotFound rather than as a failure, because "the author
// has not published anything yet" is a state and not an error.
func (c Config) Read(ctx context.Context) ([]byte, error) {
	if err := c.Configured(); err != nil {
		return nil, err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.valueURL(), nil)
	if err != nil {
		return nil, fmt.Errorf("build the store request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.APIToken)

	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("read the artifact: %w", err)
	}
	defer closeBody(response.Body)

	if response.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read the artifact: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, fmt.Errorf("read the artifact: the store answered %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	return body, nil
}

// Deploy asks the deployment workflow to run.
//
// The request is a repository dispatch, which is the smallest thing GitHub offers that starts a
// workflow on demand. The workflow it starts is the same one the schedule starts, so a dispatch and
// a scheduled run take exactly the same path.
//
// The digest travels with the request. KV is eventually consistent -- a value written a moment ago
// may not be readable from every location for up to a minute -- so a deployment that starts
// immediately can read the previous artifact. Telling the deployment which digest it is looking
// for lets it wait for that artifact rather than deploy the one before it. See the workflow.
//
// It is best-effort by design: the work is already published by the time this is called, and a
// deploy that cannot be requested leaves the site a quarter of an hour behind rather than
// unpublished. The schedule is the safety net.
func (c Config) Deploy(ctx context.Context, digest string) error {
	if c.Repository == "" || c.GitHubToken == "" {
		return errors.New("the deploy cannot be requested: GITHUB_REPOSITORY and GITHUB_TOKEN are both required")
	}

	payload := map[string]any{"event_type": "publish"}
	if digest != "" {
		payload["client_payload"] = map[string]string{"digest": digest}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode the dispatch: %w", err)
	}

	base := c.GitHubBase
	if base == "" {
		base = "https://api.github.com"
	}

	endpoint := base + "/repos/" + c.Repository + "/dispatches"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build the dispatch request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.GitHubToken)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Content-Type", "application/json")

	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("request the deploy: %w", err)
	}
	defer closeBody(response.Body)

	answer, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("read the repository's answer: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fmt.Errorf("request the deploy: the repository answered %d: %s",
			response.StatusCode, strings.TrimSpace(string(answer)))
	}

	return nil
}

// Publish stores the artifact and asks for a deploy.
//
// It writes first and asks second. A deploy that starts before the artifact is in the store would
// build the previous site, which is a confusing failure: the site would be a version behind and
// nothing would say why.
func Publish(ctx context.Context, cfg Config, artifact Artifact) error {
	if err := cfg.Write(ctx, artifact.Body); err != nil {
		return err
	}

	return cfg.Deploy(ctx, artifact.Digest)
}

// PublishSite records the current published set and asks for the public site to be rebuilt.
//
// The artifact is written synchronously and the deploy is requested afterwards, in the background.
// The difference is not an accident.
//
// The write is the part that must not be lost. The scheduled deploy reads the artifact out of the
// store, so a lost write means the site shows the previous published set until the next
// publication. It is one small request, so it costs the author a fraction of a second.
//
// The request is the part the schedule already covers. A lost dispatch means the artifact is stored
// and nobody asked for a deploy, which is exactly the case the fifteen-minute run exists for.
//
// So the author is not kept waiting on GitHub, and neither failure loses the work.
//
// The background request runs on a context detached from the caller's, because the caller's is
// canceled when the request that started this ends -- which would cancel the deploy in the
// moment the author sees their work published.
func PublishSite(ctx context.Context, cfg Config) error {
	artifact, err := BuildArtifact(ctx)
	if err != nil {
		return err
	}

	if err := cfg.Write(ctx, artifact.Body); err != nil {
		return err
	}

	go func() {
		detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), Timeout)
		defer cancel()

		if err := cfg.Deploy(detached, artifact.Digest); err != nil {
			// The artifact is stored, so the scheduled deploy will pick it up. Saying so is
			// the difference between a known delay and an unexplained one.
			log.Printf("the public site was not asked for immediately; the scheduled deploy will "+
				"publish it within fifteen minutes: %v", err)
		}
	}()

	return nil
}

// Timeout bounds the background deploy request.
//
// The repository is remote, and a request that hangs forever would leave a goroutine and a
// connection behind for the life of the process. Thirty seconds is far longer than the request
// takes and far shorter than the service lives.
const Timeout = 30 * time.Second

// client is the HTTP client every request in this package is made with.
//
// It is not http.DefaultClient. The default client has no timeout of its own, so a request is
// bounded only by the context it was given -- and a caller that passed context.Background() would
// wait forever. Every call here is currently given a context with a deadline, so the difference is
// one of robustness rather than of behavior today; the client's own timeout is what makes that
// true of every call made in the future as well.
//
// The transport is left at the default, which is the one tuned for this: a pool, keep-alives and
// per-host limits that a client making one request at a time has no use for replacing.
var client = &http.Client{Timeout: Timeout}

// closeBody closes a response body and reports a failure to close it.
//
// A response body that is not closed is a connection that is not returned to the pool, and this
// package makes one request per publish. The error is not worth failing the publish over -- the
// request itself succeeded, which is what the caller asked about -- so it is logged.
func closeBody(body io.Closer) {
	if err := body.Close(); err != nil {
		log.Printf("close the response body: %v", err)
	}
}

// envOr reads an environment variable, falling back to a value.
func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
