// Command verse-artifact fetches the published set that the authoring side stored.
//
// The deployment builds the public site from the published set, and the published set travels as a
// file: the authoring environment reads it from the database and stores it, and this command reads
// it back. The deployment therefore holds no database credential, which is the whole reason the
// published set travels at all.
//
//	go run ./cmd/verse-artifact --out verse-public.json
//
// The address of the store is built in one place -- internal/push -- rather than spelled out here
// and again in the workflow. That matters more than it looks: a URL in a shell script and a URL in
// a Go file are two strings that can disagree, and the disagreement would be a deploy that
// silently reads the wrong key.
//
// The digest of the published set is printed, because the deployment compares it with the live
// site's own digest to decide whether anything a reader can see has changed.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/divijg19/Verse/internal/export"
	"github.com/divijg19/Verse/internal/push"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("verse: artifact failed: %v", err)
	}
}

func run() error {
	out := flag.String("out", "verse-public.json", "write the published set to this file")
	expect := flag.String("expect", "",
		"wait until the published set has this digest; used to wait for a store that is still catching up")
	timeout := flag.Duration("timeout", 2*time.Minute,
		"how long to wait for the expected digest")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	cfg, err := push.ConfigFromEnv(push.ReadTokenVar)
	if err != nil {
		return err
	}
	if err := cfg.Configured(); err != nil {
		return err
	}

	body, digest, err := fetch(ctx, cfg, *expect)
	if err != nil {
		return err
	}

	if err := os.WriteFile(*out, body, 0o600); err != nil {
		return fmt.Errorf("write the published set: %w", err)
	}

	// The digest goes to stdout and everything else goes to the log, so a caller can read it
	// without having to parse a message.
	fmt.Println(digest)
	return nil
}

// fetch reads the published set, waiting for the expected digest when one was named.
//
// The wait is there because the store is eventually consistent: a value written a moment ago may
// not be readable from everywhere for up to a minute. A deployment that starts immediately after a
// publication would otherwise read the previous published set and build the previous site, which
// looks like the publication did not take.
//
// The wait is bounded. A store that has not caught up in two minutes is not going to, and the
// deployment proceeds with what it has -- the caller compares the digest it gets against the live
// site's own, so a stale artifact does not redeploy a stale site.
func fetch(ctx context.Context, cfg push.Config, expect string) ([]byte, string, error) {
	for attempt := 1; ; attempt++ {
		body, err := cfg.Read(ctx)
		if err != nil {
			return nil, "", err
		}

		digest, err := digestOf(body)
		if err != nil {
			return nil, "", err
		}

		if expect == "" || digest == expect {
			return body, digest, nil
		}

		log.Printf("the store is serving %s and %s was expected; waiting", digest, expect)

		select {
		case <-ctx.Done():
			// The store has not caught up. What is here is returned rather than nothing:
			// the caller compares the digest and decides what to do about it.
			log.Printf("the store did not catch up in time; using the published set it has")
			return body, digest, nil
		case <-time.After(10 * time.Second):
		}
	}
}

// digestOf reads the digest out of a published set.
//
// The digest is read from the file rather than recomputed, because the file's own digest is what
// the deployment will compare against the live site's -- and the two are the same value only if
// the file is intact.
func digestOf(body []byte) (string, error) {
	var doc export.Document
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", fmt.Errorf("parse the published set: %w", err)
	}
	if doc.Digest == "" {
		return "", errors.New("the published set carries no digest")
	}
	return doc.Digest, nil
}
