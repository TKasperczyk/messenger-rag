#!/bin/bash
# Background sync daemon for Messenger RAG
# Runs messenger-cli continuously and re-indexes periodically
#
# Usage: ./scripts/sync-daemon.sh [interval_minutes]
# Default interval: 10 minutes

set -e
cd "$(dirname "$0")/.."

INTERVAL_MINUTES=${1:-10}
COOKIES_FILE="${COOKIES_FILE:-cookies.json}"
DB_PATH="${DB_PATH:-messenger.db}"
LOG_DIR="/tmp/messenger-rag"

mkdir -p "$LOG_DIR"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log() {
    echo -e "${BLUE}[$(date '+%H:%M:%S')]${NC} $1"
}

cleanup() {
    log "${YELLOW}Stopping sync daemon...${NC}"
    kill $MESSENGER_PID 2>/dev/null || true
    exit 0
}
trap cleanup SIGINT SIGTERM

# Check cookies file
if [ ! -f "$COOKIES_FILE" ]; then
    echo -e "${RED}Error: cookies.json not found${NC}"
    echo "Export cookies from your browser and save to $COOKIES_FILE"
    echo "Or set COOKIES_FILE env var to the correct path"
    exit 1
fi

# Check if embedding server is running
if ! curl -s http://localhost:1235/v1/models > /dev/null 2>&1; then
    echo -e "${RED}Error: Embedding server not running on port 1235${NC}"
    echo "Start it with: python scripts/embed_server.py"
    exit 1
fi

# Build tools if needed
log "Building sync tools..."
cd meta-bridge
CGO_CFLAGS="-DSQLITE_ENABLE_FTS5" CGO_LDFLAGS="-lm" go build -tags "fts5" -o ../bin/messenger-cli ./cmd/messenger-cli 2>/dev/null
CGO_CFLAGS="-DSQLITE_ENABLE_FTS5" CGO_LDFLAGS="-lm" go build -tags "fts5" -o ../bin/fts5-setup ./cmd/fts5-setup 2>/dev/null
go build -o ../bin/milvus-index ./cmd/milvus-index 2>/dev/null
cd ..
log "${GREEN}Tools built${NC}"

# Start messenger-cli in background
log "Starting Messenger sync (live connection)..."
./bin/messenger-cli -db "$DB_PATH" "$COOKIES_FILE" > "$LOG_DIR/messenger-cli.log" 2>&1 &
MESSENGER_PID=$!

# Give it time to connect
sleep 5
if ! kill -0 $MESSENGER_PID 2>/dev/null; then
    echo -e "${RED}messenger-cli failed to start. Check $LOG_DIR/messenger-cli.log${NC}"
    exit 1
fi
log "${GREEN}Messenger connected (PID: $MESSENGER_PID)${NC}"

# Index loop
log "Starting index loop (every ${INTERVAL_MINUTES} minutes)"
log "Press Ctrl+C to stop\n"

LAST_MSG_COUNT=0
while true; do
    sleep $((INTERVAL_MINUTES * 60))

    # Check if messenger-cli is still running
    if ! kill -0 $MESSENGER_PID 2>/dev/null; then
        log "${RED}messenger-cli died, restarting...${NC}"
        ./bin/messenger-cli -db "$DB_PATH" "$COOKIES_FILE" > "$LOG_DIR/messenger-cli.log" 2>&1 &
        MESSENGER_PID=$!
        sleep 5
    fi

    # Check current message count
    CURRENT_COUNT=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM messages" 2>/dev/null || echo "0")

    if [ "$CURRENT_COUNT" != "$LAST_MSG_COUNT" ]; then
        NEW_MSGS=$((CURRENT_COUNT - LAST_MSG_COUNT))
        log "${GREEN}+${NEW_MSGS} new messages${NC} (total: $CURRENT_COUNT)"

        # Run indexing pipeline
        log "Regenerating chunks..."
        ./bin/fts5-setup -db "$DB_PATH" --from-db > "$LOG_DIR/fts5.log" 2>&1

        log "Indexing to Milvus..."
        ./bin/milvus-index -db "$DB_PATH" > "$LOG_DIR/milvus.log" 2>&1

        LAST_MSG_COUNT=$CURRENT_COUNT
        log "${GREEN}Index updated${NC}\n"
    else
        log "No new messages"
    fi
done
