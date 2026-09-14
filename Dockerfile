# =============================================================================
# Stage 1: Build Open JTalk + Go Binary
# =============================================================================
FROM golang:1.23-alpine AS builder

WORKDIR /build

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
# Download HTS Voice: nitech_jp_atr503_m001 (CC BY 3.0)
# Attribution: HTS Working Group, Nagoya Institute of Technology
# ---------------------------------------------------------------------------
RUN curl -sL \
    'https://downloads.sourceforge.net/project/open-jtalk/HTS%20voice/hts_voice_nitech_jp_atr503_m001-1.05/hts_voice_nitech_jp_atr503_m001-1.05.tar.gz' \
    | tar -xz -C /tmp/ \
    && mkdir -p /usr/local/voice \
    && cp /tmp/hts_voice_nitech_jp_atr503_m001-1.05/nitech_jp_atr503_m001.htsvoice \
        /usr/local/voice/

# ---------------------------------------------------------------------------
# Build MockCam Go binary
# ---------------------------------------------------------------------------
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o mockcam cmd/mockcam/main.go

# =============================================================================
# Stage 2: Runtime image
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

# MockCam binary
COPY --from=builder /build/mockcam /app/mockcam

# Open JTalk binary
COPY --from=builder /usr/local/bin/open_jtalk /usr/local/bin/open_jtalk

# Dictionary (NAIST-jdic, BSD License)
COPY --from=builder /usr/local/dic /usr/local/dic

# HTS Voice model (CC BY 3.0 — HTS Working Group, Nagoya Institute of Technology)
COPY --from=builder /usr/local/voice /usr/local/voice

RUN mkdir -p /config /media
VOLUME ["/config", "/media"]

EXPOSE 8554 8080 3702/udp

ENTRYPOINT ["/app/mockcam"]
