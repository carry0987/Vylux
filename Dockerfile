# --- Build ---
FROM golang:1.26-alpine AS builder

ARG VERSION=dev
ARG COMMIT=unknown

# Add Alpine edge community repo for libvips 8.18+
RUN echo "https://dl-cdn.alpinelinux.org/alpine/edge/community" >> /etc/apk/repositories \
    && echo "https://dl-cdn.alpinelinux.org/alpine/edge/main" >> /etc/apk/repositories \
    && apk add --no-cache gcc musl-dev vips-dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
    -o /vylux ./cmd/vylux

# --- Runtime ---
FROM alpine:edge

ARG TARGETARCH
ARG SHAKA_PACKAGER_VERSION=v3.7.0

RUN apk add --no-cache \
    vips \
    ffmpeg \
    curl \
    ca-certificates

RUN case "$TARGETARCH" in \
        amd64) SHAKA_ARCH=x64 ;; \
        arm64) SHAKA_ARCH=arm64 ;; \
        *) echo "unsupported TARGETARCH: $TARGETARCH" && exit 1 ;; \
    esac \
    && curl -fsSL "https://github.com/shaka-project/shaka-packager/releases/download/${SHAKA_PACKAGER_VERSION}/packager-linux-${SHAKA_ARCH}" \
        -o /usr/local/bin/packager \
    && chmod +x /usr/local/bin/packager

# Set a fixed temp directory for transcoding scratch space, which may be large.
# This allows it to be mounted as a volume and persist across container restarts, avoiding out-of-space errors.
ENV TMPDIR=/var/cache/vylux
RUN mkdir -p /var/cache/vylux
VOLUME ["/var/cache/vylux"]

COPY --from=builder /vylux /usr/local/bin/vylux

ENTRYPOINT ["vylux"]
CMD ["--mode=all"]
