FROM golang:1.21-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY *.go ./

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o sonarr-autoimport .

# Final stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# Copy the binary
COPY --from=builder /app/sonarr-autoimport .

# Create directories
RUN mkdir -p /config /media /downloads

# Create startup script
RUN cat > /app/start.sh << 'EOF'
#!/bin/sh
set -e

echo "SonarrAutoImport Go Edition"
echo "==========================="

# Set defaults
SCAN_INTERVAL=${SCAN_INTERVAL:-300}
DRY_RUN=${DRY_RUN:-false}
VERBOSE=${VERBOSE:-false}

# Create default config if needed
CONFIG_FILE="/config/Settings.json"
if [ ! -f "$CONFIG_FILE" ]; then
    echo "Creating default configuration..."
    ./sonarr-autoimport -c "$CONFIG_FILE"
    echo ""
    echo "Please set the following environment variables:"
    echo "  SONARR_URL - Your Sonarr URL (e.g., http://sonarr:8989)"
    echo "  SONARR_API_KEY - Your Sonarr API key"
    echo ""
fi

echo "Configuration:"
echo "  Sonarr URL: ${SONARR_URL:-http://sonarr:8989}"
echo "  Scan Interval: ${SCAN_INTERVAL}s"
echo "  Dry Run: ${DRY_RUN}"
echo "  Verbose: ${VERBOSE}"
echo ""

# Build command arguments
ARGS="-c $CONFIG_FILE"
if [ "$VERBOSE" = "true" ]; then
    ARGS="$ARGS -v"
fi
if [ "$DRY_RUN" = "true" ]; then
    ARGS="$ARGS -dry-run"
fi

# Check if running in daemon mode
if [ "${DAEMON_MODE:-true}" = "true" ]; then
    echo "Running in daemon mode (scanning every ${SCAN_INTERVAL}s)..."
    export DAEMON_MODE=true
    export SCAN_INTERVAL="$SCAN_INTERVAL"
    exec ./sonarr-autoimport $ARGS
else
    echo "Running single scan..."
    exec ./sonarr-autoimport $ARGS
fi
EOF

RUN chmod +x /app/start.sh

# Health check
HEALTHCHECK --interval=60s --timeout=10s --start-period=30s --retries=3 \
    CMD pgrep -f "sonarr-autoimport" > /dev/null || exit 1

# Volumes
VOLUME ["/config", "/media", "/downloads"]

# Labels
LABEL org.opencontainers.image.title="SonarrAutoImport Go"
LABEL org.opencontainers.image.description="Fast, reliable auto-import tool for Sonarr written in Go"
LABEL org.opencontainers.image.source="https://github.com/your-repo/sonarr-autoimport-go"

EXPOSE 8080

CMD ["/app/start.sh"]
