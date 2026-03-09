## Multi-stage Dockerfile
## Stage 1: build Tailwind CSS
FROM node:20-alpine AS tailwind
WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm ci --silent || npm install --silent
COPY static/css/input.css static/css/input.css
RUN npx @tailwindcss/cli -i ./static/css/input.css -o ./static/css/output.css --minify

## Stage 2: build Go binary
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /verse ./cmd/server

## Final image: minimal runtime
FROM gcr.io/distroless/static:nonroot
COPY --from=build /verse /verse
COPY --from=tailwind /app/static/css/output.css /static/css/output.css
EXPOSE 8080
ENTRYPOINT ["/verse"]
