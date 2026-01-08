# Portainer MCP Server with SSE/HTTP Transport Support
# Multi-stage build

FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG BUILD_DATE
ARG COMMIT

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w -X main.Version=${VERSION} -X main.BuildDate=${BUILD_DATE} -X main.Commit=${COMMIT}" \
    -o portainer-mcp ./cmd/portainer-mcp

FROM alpine:3.20

RUN apk add --no-cache ca-certificates

COPY --from=builder /build/portainer-mcp /usr/local/bin/portainer-mcp

WORKDIR /app

EXPOSE 6972

ENTRYPOINT ["/usr/local/bin/portainer-mcp"]
CMD ["-transport", "sse", "-port", "6972"]
