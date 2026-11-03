# syntax=docker/dockerfile:1
FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/chirpy ./cmd/server

# distroless: no shell, CA certificates included (needed for TLS to
# Postgres, Redis and Google), runs as an unprivileged user
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/chirpy /chirpy
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/chirpy"]
