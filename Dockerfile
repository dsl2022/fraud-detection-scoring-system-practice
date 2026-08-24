# Multi-stage build -> tiny, static, non-root image for the ECS Fargate service.
#
# Why distroless/static: no shell, no package manager, no libc — a minimal attack
# surface and a small image. CGO_ENABLED=0 yields a fully static binary so it runs
# on the static base with no dynamic linker.

# ---- build stage ----
FROM golang:1.27 AS build
WORKDIR /src

# Cache deps separately from source so code changes don't re-download modules.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# -trimpath strips local paths; -s -w drop debug info for a smaller binary.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# ---- runtime stage ----
# nonroot tag runs as uid 65532 — never as root.
FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/server /server
EXPOSE 8080 9090
USER nonroot:nonroot
ENTRYPOINT ["/server"]
