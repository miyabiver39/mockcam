# =============================================================================
# Stage 1: Cross-compile Go binary on the BUILD platform (never runs under QEMU)
#   This avoids the QEMU arm64 HTTP/2 crash that occurs with go mod download.
# =============================================================================
FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS go-builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags="-s -w" -o mockcam cmd/mockcam/main.go

# =============================================================================
# Stage 2: Compile Open JTalk on the TARGET platform and download assets
#   Runs under QEMU for arm64, but no Go/HTTP2 involved — pure C/cmake only.
# =============================================================================
FROM golang:1.23-alpine AS jtalk-builder

# Install build dependencies
RUN apk add --no-cache \
    build-base \
    cmake \
    git \
    curl \
    tar

# ---------------------------------------------------------------------------
# Build HTS Engine API (required by open_jtalk)
# Apply musl libc patch: fpos_t.__pos does not exist in musl, use ftell() instead
# ---------------------------------------------------------------------------
RUN git clone --depth 1 https://github.com/r9y9/hts_engine_API.git \
    && sed -i 's/pos\.__pos/ftell((FILE *) fp->pointer)/g' \
        ./hts_engine_API/src/lib/HTS_misc.c \
    && cmake \
        -D CMAKE_BUILD_TYPE=Release \
        -S ./hts_engine_API/src \
        -B ./hts_engine_API/src/build \
    && cmake --build ./hts_engine_API/src/build \
    && cmake --install ./hts_engine_API/src/build

# ---------------------------------------------------------------------------
# Build Open JTalk
# ---------------------------------------------------------------------------
RUN git clone --depth 1 https://github.com/r9y9/open_jtalk.git \
    && cmake \
        -D CMAKE_BUILD_TYPE=Release \
        -D BUILD_PROGRAMS=ON \
        -S ./open_jtalk/src \
        -B ./open_jtalk/src/build \
    && cmake --build ./open_jtalk/src/build \
    && cmake --install ./open_jtalk/src/build

# ---------------------------------------------------------------------------
# Download Open JTalk dictionary: NAIST-jdic (BSD License)
# ---------------------------------------------------------------------------
RUN curl -sL \
    'https://downloads.sourceforge.net/project/open-jtalk/Dictionary/open_jtalk_dic-1.11/open_jtalk_dic_utf_8-1.11.tar.gz' \
    | tar -xz -C /usr/local/ \
    && mv /usr/local/open_jtalk_dic_utf_8-1.11 /usr/local/dic

# ---------------------------------------------------------------------------
# Download HTS Voice: tohoku-f01-neutral (CC BY 4.0)
# The most popular natural Japanese female voice for Open JTalk.
# Attribution: Tohoku University, Graduate School of Information Sciences
# https://github.com/icn-lab/htsvoice-tohoku-f01
# ---------------------------------------------------------------------------
RUN curl -sL \
    'https://github.com/icn-lab/htsvoice-tohoku-f01/archive/refs/heads/master.tar.gz' \
    | tar -xz -C /tmp/ \
    && mkdir -p /usr/local/voice \
    && cp /tmp/htsvoice-tohoku-f01-master/tohoku-f01-neutral.htsvoice \
        /usr/local/voice/

# =============================================================================
# Stage 3: Runtime image
# =============================================================================
FROM alpine:3.20

RUN apk add --no-cache \
    ffmpeg \
    tzdata \
    ca-certificates \
    ttf-dejavu \
    fontconfig \
    espeak-ng \
    libstdc++

WORKDIR /app

# MockCam binary (cross-compiled on host, no QEMU dependency)
COPY --from=go-builder /build/mockcam /app/mockcam

# Open JTalk binary (compiled for target arch)
COPY --from=jtalk-builder /usr/local/bin/open_jtalk /usr/local/bin/open_jtalk

# Dictionary (NAIST-jdic, BSD License)
COPY --from=jtalk-builder /usr/local/dic /usr/local/dic

# HTS Voice model: tohoku-f01-neutral (CC BY 4.0 — Tohoku University)
COPY --from=jtalk-builder /usr/local/voice /usr/local/voice

RUN mkdir -p /config /media
VOLUME ["/config", "/media"]

EXPOSE 8554 8080 3702/udp

ENTRYPOINT ["/app/mockcam"]
