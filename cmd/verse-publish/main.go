// Command verse-publish produces the author's public site and pushes it for deployment.
//
// The public site is a static artifact. It is built from the published set, and the published set
// lives in the database, and the database is only reachable from the authoring environment. So the
// authoring environment is the one that builds the artifact and pushes it; the deployment
// environment reads the artifact and never touches the database.
//
// That arrangement is the point. A build that reached into the database would need a database
// credential in the build environment, and a credential in a build command is a credential in a
// build log -- which is how the serving password was exposed once already.
//
//	go run ./cmd/verse-publish --out verse-public.json
//
// The file it writes is the published export: the works a reader can see, and nothing else. The
// export carries a digest of that set, so the deployment can tell whether anything a reader can see
// has changed, and skip a deploy that would produce the same site.
//
// Storing the file asks the deployment workflow to run, which is what makes a publication reach the
// site in seconds rather than in a quarter of an hour. The command is the manual equivalent of
// what the publishing action does by itself, and it is what to reach for when the automatic path
// did not run.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/push"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("verse: publish failed: %v", err)
	}
}

func run() error {
	out := flag.String("out", "verse-public.json", "write the published export to this file")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if err := database.Connect(); err != nil {
		return err
	}
	defer database.ClosePool()

	// Only the published works, and only their current text. The filter is the publisher's own,
	// so the artifact cannot contain a draft even by accident.
	artifact, err := push.BuildArtifact(ctx)
	if err != nil {
		return err
	}

	if err := os.WriteFile(*out, artifact.Body, 0o600); err != nil {
		return fmt.Errorf("write the export: %w", err)
	}

	log.Printf("wrote the published set to %s (digest %s)", *out, artifact.Digest)

	// The file is written whether or not the store is configured, because the file is the
	// artifact. Storing it is what asks for a deploy.
	cfg, err := push.ConfigFromEnv(push.WriteTokenVar)
	if err != nil {
		return err
	}
	if err := cfg.Configured(); err != nil {
		log.Printf("%v; the file is the artifact and nothing was stored", err)
		return nil
	}

	if err := push.Publish(ctx, cfg, artifact); err != nil {
		return err
	}

	log.Printf("stored the artifact and asked for a deploy")
	return nil
}
