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
## This image is only worth keeping if it is actually built, so CI builds and smoke-tests it.

# ── Stage 1: stylesheet ───────────────────────────────────────────────────────────────
# The Bun version is pinned to the same value CI uses, and CI asserts the two agree. These were
# previously `oven/bun:1-alpine` here (resolving to 1.4.2) against a pin of 1.3.5 in the workflow,
# so the two build paths silently used different package managers.
FROM oven/bun:1.3.5-alpine AS css
WORKDIR /src

# Manifests first, so the dependency layer is cached independently of source changes.
COPY package.json bun.lock ./
RUN bun install --frozen-lockfile

COPY static/css/input.css ./static/css/input.css
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

# ── Stage 3: runtime ───────────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot

# The application resolves its asset directory relative to the working directory.
WORKDIR /app

COPY --from=build /out/verse /app/verse
COPY --from=css /src/static /app/static

USER nonroot:nonroot
EXPOSE 8080

ENTRYPOINT ["/app/verse"]
