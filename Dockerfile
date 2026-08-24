# Build the binary in a multi-stage builder
FROM golang:1.26-alpine AS builder

WORKDIR /src

# Pre-copy dependency manifests for module caching
COPY go.mod ./
RUN go mod download

# Copy source repository
COPY . .

# Compile static binary with optimizations
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o /bin/edge-telemetry-daemon ./cmd/agent

# Creating final minimal distroless image
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /
COPY --from=builder /bin/edge-telemetry-daemon /edge-telemetry-daemon

USER nonroot:nonroot
EXPOSE 8080

ENTRYPOINT ["/edge-telemetry-daemon"]
