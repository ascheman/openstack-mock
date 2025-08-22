# Multi-stage Dockerfile for openstack-mock
# Builder stage
FROM --platform=$BUILDPLATFORM golang:1.25 AS builder

ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT

WORKDIR /src

# Speed up builds by caching module downloads
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source
COPY . .

# Build a static binary for minimal runtime image
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags "-s -w" -o /out/openstack-mock .

# Runtime stage (distroless for small, secure image)
FROM gcr.io/distroless/static:nonroot AS runtime

WORKDIR /

# Default listen port in the app is 19090
EXPOSE 19090

# Copy binary
COPY --from=builder /out/openstack-mock /openstack-mock

USER nonroot:nonroot

# Listen on all interfaces inside container
ENTRYPOINT ["/openstack-mock", "-listen", "0.0.0.0"]
