## Multi-stage build for the Verse authoring application.
##
## Reproducibility notes, each traceable to a defect this file previously had:
##
##  * The Go base image pins MAJOR.MINOR and floats the patch release. It was previously
##    golang:1.25-alpine against `go 1.26.0` in go.mod, so the image could not build the module it
##    claimed to build. CI asserts the tag's minor matches go.mod, so this cannot drift silently.
##
##  * The stylesheet is built with bun, from the repository's only lockfile (bun.lock). It was
##    previously `npm ci … || npm install` on a repository with no package-lock.json, so npm ci
##    always failed and always fell through to a fresh, unpinned resolution.
##
##  * WORKDIR is set explicitly and the asset tree is copied to a known location. The runtime served
##    ./static relative to the process working directory, which only resolved by accident because
##    the distroless base defaults to /.
##
##  * go:embed is deliberately not used for static assets. It would require the generated
##    stylesheet to exist at compile time, coupling "go build" to the Tailwind step and breaking
##    "go run ./cmd/server" whenever that artifact is absent.
##
##  * The css stage was given the stylesheet and nothing else, on the assumption that input.css had no
##    other inputs. It does: it declares @source for templ/ and internal/. Auto-detection had been
##    finding those in the repository-root build, so the two paths produced different stylesheets --
##    22,440 bytes against 4,165 -- and the image's contained no utility class at all. The stage now
##    copies what the directives name, and takes the asset tree from the build stage rather than from
##    itself, which had been how the image ended up with no JavaScript in it.
##
## This image is only worth keeping if it is actually built, so CI builds and smoke-tests it -- and
## since v0.4.9 that smoke test asserts the image serves a page, not merely that it starts. It used to
## check /health and that /library refuses an anonymous caller, neither of which can see a missing
## asset.

# ── Stage 1: stylesheet ───────────────────────────────────────────────────────────────
# The Bun version is pinned to the same value CI uses, and CI asserts the two agree. These were
# previously `oven/bun:1-alpine` here (resolving to 1.4.2) against a pin of 1.3.5 in the workflow,
# so the two build paths silently used different package managers. Both are now 1.4.2, and the tag
# is fully qualified rather than floating on the 1.x line, so this build cannot drift underneath a
# release when Bun publishes a new minor.
FROM oven/bun:1.4.2-alpine AS css
WORKDIR /src

# Manifests first, so the dependency layer is cached independently of source changes.
COPY package.json bun.lock ./
RUN bun install --frozen-lockfile

# The stylesheet and the two directories its @source directives name, and nothing else.
#
# This stage used to copy input.css alone, on the reasoning that the stylesheet had no other inputs.
# It does. static/css/input.css is `@import "tailwindcss" source(none)` with @source directives
# pointing at ../../templ and ../../internal, which Tailwind resolves relative to the stylesheet and
# therefore relative to this working directory. Without those two directories here there were no class
# names to find, and the image shipped 4,165 bytes of theme variables and preflight with not one
# utility class in it -- so every `flex`, `min-h-0` and `overflow-hidden` in the markup silently did
# nothing. The same stage also never held static/js, which is why the image carried no JavaScript.
#
# Placed after the dependency layer deliberately: bun install stays cached across source changes,
# while this copy and the compile below are invalidated by a template edit, which is correct, because
# a template edit is exactly what changes the generated utilities.
COPY static/css/input.css ./static/css/input.css
COPY templ ./templ
COPY internal ./internal
RUN bunx @tailwindcss/cli -i ./static/css/input.css -o ./static/css/output.css --minify

# ── Stage 2: binary ───────────────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS build
ARG VERSION=dev
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# templ artifacts are committed, so no generator is needed to compile. The -X ldflag stamps the
# build so a running container can be identified.
RUN CGO_ENABLED=0 go build -trimpath \
      -tags netgo \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/verse ./cmd/server

# The migration runner ships in the same image. Schema is created only by this binary; the service
# refuses to start without it having been run, so any deploy of this image needs it. Carrying both
# also makes the image self-contained: the SQL is embedded, so the runner cannot drift from the code
# it is migrating.
RUN CGO_ENABLED=0 go build -trimpath \
      -tags netgo \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/migrate ./cmd/migrate

# ── Stage 3: runtime ───────────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot

# The application resolves its asset directory relative to the working directory.
WORKDIR /app

COPY --from=build /out/verse /app/verse
COPY --from=build /out/migrate /app/migrate

# Static assets come from the build stage, which has the whole tree. Only the generated stylesheet
# comes from the css stage.
#
# This was `COPY --from=css /src/static /app/static`, which read as though the css stage were the
# source of the asset tree. It was not: that stage held only what the stylesheet build needed, so the
# image received a static directory containing no JavaScript, and the application served pages whose
# htmx and navigation scripts 404'd. Taking the assets from the stage that actually has them, and
# overlaying the one generated file, states the provenance of each and makes the omission impossible
# to reintroduce by accident.
#
# Order matters and is deliberate: the generated stylesheet is copied last so it wins over any
# stale output.css that happened to be in the build context. .dockerignore excludes it, so in practice
# the overlay only ever replaces nothing; it is here so the image is correct even if someone builds
# with a dirty working tree.
COPY --from=build /src/static /app/static
COPY --from=css  /src/static/css/output.css /app/static/css/output.css

USER nonroot:nonroot
EXPOSE 8080

ENTRYPOINT ["/app/verse"]
