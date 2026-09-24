# syntax=docker/dockerfile:1

# ---------- build ----------
FROM golang:1.26-alpine AS build
WORKDIR /src

# Dependencies first: this project is stdlib-only, so go.mod is the whole manifest.
COPY go.mod ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath -ldflags="-s -w" \
      -o /out/football-balancer ./cmd/server

# ---------- test (opt-in: docker build --target test .) ----------
FROM build AS test
RUN set -eu; \
    test -z "$(gofmt -l .)" || { echo "gofmt needed on:"; gofmt -l .; exit 1; }; \
    go vet ./...; \
    go test ./...

# ---------- runtime ----------
FROM alpine:3.22 AS runtime

# 1000 is also the default uid of the first user on many hosts, which keeps
# bind-mounted rosters writable without extra chown steps.
RUN adduser -D -u 1000 balancer

COPY --from=build /out/football-balancer /usr/local/bin/football-balancer

# The roster lives on a volume outside the app tree, so an image upgrade can
# never shadow edits made by a running container.
RUN mkdir -p /data && chown balancer:balancer /data
VOLUME /data

USER balancer
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/api/health >/dev/null || exit 1

ENTRYPOINT ["/usr/local/bin/football-balancer"]
CMD ["-addr", ":8080", "-data", "/data/players.json", "-matches", "/data/matches.json"]
