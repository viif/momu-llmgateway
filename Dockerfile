# ── Builder Stage ──
FROM golang:1.25-bookworm AS builder

ARG ONNX_VERSION=1.21.0

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl && \
    curl -sL "https://github.com/microsoft/onnxruntime/releases/download/v${ONNX_VERSION}/onnxruntime-linux-x64-${ONNX_VERSION}.tgz" -o onnxruntime.tgz && \
    tar xzf onnxruntime.tgz && \
    cp onnxruntime-linux-x64-${ONNX_VERSION}/lib/libonnxruntime.so* /usr/local/lib/ && \
    rm -rf onnxruntime*

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG VERSION=dev
ARG BUILD_TIME
RUN CGO_ENABLED=1 GOOS=linux go build \
    -ldflags="-s -w -X main.version=${VERSION} -X main.buildTime=${BUILD_TIME}" \
    -o /gateway ./cmd/gateway

# ── Runtime Stage ──
FROM debian:bookworm-slim

ARG VERSION=dev
ARG BUILD_TIME

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata curl && \
    rm -rf /var/lib/apt/lists/*

COPY --from=builder /usr/local/lib/libonnxruntime.so* /usr/local/lib/
RUN ldconfig

RUN groupadd -r gateway && useradd -r -g gateway -d /app -s /sbin/nologin gateway
WORKDIR /app

COPY --from=builder /gateway /app/gateway
COPY configs/gateway.yaml /app/configs/gateway.yaml
COPY --chown=gateway:gateway .models/ /app/.models/

ENV GATEWAY_CONFIG=/app/configs/gateway.yaml

LABEL org.opencontainers.image.title="momu-llmgateway" \
      org.opencontainers.image.description="LLM Gateway with multi-provider routing" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.source="https://github.com/viif/momu-llmgateway"

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD curl -f http://localhost:8080/health || exit 1

STOPSIGNAL SIGTERM
USER gateway
ENTRYPOINT ["/app/gateway"]
