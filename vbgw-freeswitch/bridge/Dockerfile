# bridge/Dockerfile
# 변경이력: v1.3.0 | 2026-04-20 | [Architect] | Debian-based Build/Runtime for ML Stability

# Stage 1: Build Environment (Debian based for glibc compatibility)
FROM golang:1.23-bookworm AS builder

# Install build dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    wget \
    ca-certificates \
    build-essential \
    && rm -rf /var/lib/apt/lists/*

# Install ONNX Runtime shared library (ARM64 specific - Optimized for API v20/v24 compatibility)
ARG ORT_VERSION=1.17.1
RUN mkdir -p /usr/local/lib /usr/local/include && \
    wget -q https://github.com/microsoft/onnxruntime/releases/download/v${ORT_VERSION}/onnxruntime-linux-aarch64-${ORT_VERSION}.tgz \
    && tar -xzf onnxruntime-linux-aarch64-${ORT_VERSION}.tgz \
    && cp onnxruntime-linux-aarch64-${ORT_VERSION}/lib/libonnxruntime.so* /usr/local/lib/ \
    && cp -r onnxruntime-linux-aarch64-${ORT_VERSION}/include/* /usr/local/include/ \
    && rm -rf onnxruntime-linux-aarch64-${ORT_VERSION}*

WORKDIR /app
COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -mod=vendor -o /app/bridge ./cmd/main.go

# Stage 2: Runtime Environment (Debian based)
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    libstdc++6 \
    wget \
    && rm -rf /var/lib/apt/lists/*

# Copy AI libraries and binary
COPY --from=builder /usr/local/lib/libonnxruntime.so* /usr/local/lib/
ENV LD_LIBRARY_PATH=/usr/local/lib
COPY --from=builder /app/bridge /usr/local/bin/bridge

ENTRYPOINT ["/usr/local/bin/bridge"]
