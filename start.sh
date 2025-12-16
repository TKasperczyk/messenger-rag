#!/bin/bash
# Messenger RAG - Start all services
# Usage: ./start.sh [--sync]
#
# Options:
#   --sync    Enable background sync (connects to Messenger, indexes new messages)

set -e
ENABLE_SYNC=false
SYNC_INTERVAL=10  # minutes

for arg in "$@"; do
    case $arg in
        --sync) ENABLE_SYNC=true ;;
        --sync=*) ENABLE_SYNC=true; SYNC_INTERVAL="${arg#*=}" ;;
    esac
done
cd "$(dirname "$0")"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

cleanup() {
    echo -e "\n${YELLOW}Shutting down services...${NC}"
    # Kill all child processes
    pkill -P $$ 2>/dev/null || true
    kill $EMBED_PID $RAG_PID $WEB_PID $SYNC_PID 2>/dev/null || true
    exit 0
}
trap cleanup SIGINT SIGTERM

echo -e "${GREEN}=== Messenger RAG Startup ===${NC}\n"

# Check Milvus
echo -n "Checking Milvus... "
if curl -s http://localhost:19530/v1/vector/collections > /dev/null 2>&1; then
    echo -e "${GREEN}OK${NC}"
else
    echo -e "${RED}NOT RUNNING${NC}"
    echo "Start Milvus with: docker start milvus-standalone"
    exit 1
fi

# Start embedding server
echo -n "Starting embedding server (port 1235)... "
source .venv/bin/activate
python scripts/embed_server.py > /tmp/embed_server.log 2>&1 &
EMBED_PID=$!

# Wait for embedding server
for i in {1..30}; do
    if curl -s http://localhost:1235/v1/models > /dev/null 2>&1; then
        echo -e "${GREEN}OK${NC}"
        break
    fi
    if [ $i -eq 30 ]; then
        echo -e "${RED}FAILED${NC}"
        echo "Check /tmp/embed_server.log"
        exit 1
    fi
    sleep 1
done

# Build and start RAG server
echo -n "Building RAG server... "
cd meta-bridge
CGO_CFLAGS="-DSQLITE_ENABLE_FTS5" CGO_LDFLAGS="-lm" go build -tags "fts5" -o ../bin/rag-server ./cmd/rag-server 2>/dev/null
echo -e "${GREEN}OK${NC}"
cd ..

echo -n "Starting RAG server (port 8090)... "
./bin/rag-server > /tmp/rag_server.log 2>&1 &
RAG_PID=$!

for i in {1..10}; do
    if curl -s http://localhost:8090/health > /dev/null 2>&1; then
        echo -e "${GREEN}OK${NC}"
        break
    fi
    if [ $i -eq 10 ]; then
        echo -e "${RED}FAILED${NC}"
        echo "Check /tmp/rag_server.log"
        exit 1
    fi
    sleep 1
done

# Start web frontend
echo -n "Starting web frontend (port 5173)... "
cd web
pnpm dev > /tmp/web_server.log 2>&1 &
WEB_PID=$!
cd ..

for i in {1..15}; do
    if curl -s http://localhost:5173 > /dev/null 2>&1; then
        echo -e "${GREEN}OK${NC}"
        break
    fi
    if [ $i -eq 15 ]; then
        echo -e "${RED}FAILED${NC}"
        echo "Check /tmp/web_server.log"
        exit 1
    fi
    sleep 1
done

echo -e "\n${GREEN}=== All services running ===${NC}"
echo -e "  Embedding server: http://localhost:1235"
echo -e "  RAG API:          http://localhost:8090"
echo -e "  Web UI:           ${GREEN}http://localhost:5173${NC}"

# Start background sync if enabled
SYNC_PID=""
COOKIES_FILE=""
if [ "$ENABLE_SYNC" = true ]; then
    # Find cookies.json
    for path in "cookies.json" "meta-bridge/cookies.json"; do
        [ -f "$path" ] && COOKIES_FILE="$path" && break
    done

    if [ -n "$COOKIES_FILE" ]; then
        echo -e "\n${YELLOW}Starting background sync (every ${SYNC_INTERVAL} min)...${NC}"

        # Build sync tools
        cd meta-bridge
        CGO_CFLAGS="-DSQLITE_ENABLE_FTS5" CGO_LDFLAGS="-lm" go build -tags "fts5" -o ../bin/messenger-cli ./cmd/messenger-cli 2>/dev/null
        CGO_CFLAGS="-DSQLITE_ENABLE_FTS5" CGO_LDFLAGS="-lm" go build -tags "fts5" -o ../bin/fts5-setup ./cmd/fts5-setup 2>/dev/null
        go build -o ../bin/milvus-index ./cmd/milvus-index 2>/dev/null
        cd ..

        # Start messenger-cli
        ./bin/messenger-cli -db messenger.db "$COOKIES_FILE" > /tmp/messenger-cli.log 2>&1 &
        SYNC_PID=$!
        sleep 3

        if kill -0 $SYNC_PID 2>/dev/null; then
            echo -e "  Messenger sync: ${GREEN}connected${NC} (PID: $SYNC_PID)"
            echo -e "  Logs: /tmp/messenger-cli.log, /tmp/sync-index.log"

            # Start periodic indexer in background (runs immediately, then every N min)
            > /tmp/sync-index.log  # Clear log
            (
                while true; do
                    CURRENT=$(sqlite3 messenger.db "SELECT COUNT(*) FROM messages" 2>/dev/null || echo 0)
                    echo "[$(date '+%H:%M')] Reindexing ($CURRENT messages)..." >> /tmp/sync-index.log
                    ./bin/fts5-setup -db messenger.db --from-db >> /tmp/sync-index.log 2>&1
                    ./bin/milvus-index -db messenger.db >> /tmp/sync-index.log 2>&1
                    echo "[$(date '+%H:%M')] Done. Next in ${SYNC_INTERVAL}min" >> /tmp/sync-index.log
                    sleep $((SYNC_INTERVAL * 60))
                done
            ) &
            INDEX_PID=$!
        else
            echo -e "  Messenger sync: ${RED}failed${NC} (check /tmp/messenger-cli.log)"
        fi
    else
        echo -e "\n${YELLOW}Sync enabled but cookies.json not found - skipping${NC}"
    fi
fi

echo -e "\nPress Ctrl+C to stop all services"

wait
