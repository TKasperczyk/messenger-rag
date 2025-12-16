#!/bin/bash
# Demo mode - runs with sample data instead of real messages
# Usage: ./demo.sh

set -e
cd "$(dirname "$0")"
PROJECT_ROOT="$(pwd)"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

cleanup() {
    echo -e "\n${YELLOW}Shutting down demo...${NC}"
    pkill -P $$ 2>/dev/null || true
    kill $EMBED_PID $RAG_PID $WEB_PID 2>/dev/null || true
    exit 0
}
trap cleanup SIGINT SIGTERM

echo -e "${GREEN}=== Messenger RAG Demo ===${NC}\n"

# Check Milvus
echo -n "Checking Milvus... "
if curl -s http://localhost:19530/v1/vector/collections > /dev/null 2>&1; then
    echo -e "${GREEN}OK${NC}"
else
    echo -e "${RED}NOT RUNNING${NC}"
    echo "Start Milvus with: docker start milvus-standalone"
    echo "Or: docker run -d --name milvus-standalone -p 19530:19530 milvusdb/milvus:v2.4.0 standalone"
    exit 1
fi

# Build tools if needed
echo -n "Building tools... "
cd meta-bridge
CGO_CFLAGS="-DSQLITE_ENABLE_FTS5" CGO_LDFLAGS="-lm" go build -tags "fts5" -o ../bin/import-sample ./cmd/import-sample 2>/dev/null
CGO_CFLAGS="-DSQLITE_ENABLE_FTS5" CGO_LDFLAGS="-lm" go build -tags "fts5" -o ../bin/fts5-setup ./cmd/fts5-setup 2>/dev/null
CGO_CFLAGS="-DSQLITE_ENABLE_FTS5" CGO_LDFLAGS="-lm" go build -tags "fts5" -o ../bin/milvus-index ./cmd/milvus-index 2>/dev/null
CGO_CFLAGS="-DSQLITE_ENABLE_FTS5" CGO_LDFLAGS="-lm" go build -tags "fts5" -o ../bin/rag-server ./cmd/rag-server 2>/dev/null
cd ..
echo -e "${GREEN}OK${NC}"

# Import sample data
echo -n "Importing sample conversations... "
./bin/import-sample -json sample_data/conversations.json -db demo.db > /dev/null
echo -e "${GREEN}OK${NC}"

# Start embedding server
echo -n "Starting embedding server (port 1235)... "
source .venv/bin/activate
python scripts/embed_server.py > /tmp/embed_server.log 2>&1 &
EMBED_PID=$!

for i in {1..30}; do
    if curl -s http://127.0.0.1:1235/v1/models > /dev/null 2>&1; then
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

# Create demo config (separate DB and Milvus collection)
echo -n "Creating demo config... "
sed -e 's/messenger_message_chunks_v2/demo_chunks/g' \
    -e 's/sqlite: "messenger.db"/sqlite: "demo.db"/g' \
    rag.yaml > demo_rag.yaml
echo -e "${GREEN}OK${NC}"

# Generate chunks and index
echo -n "Generating chunks... "
./bin/fts5-setup -db demo.db --from-db > /dev/null 2>&1
echo -e "${GREEN}OK${NC}"

echo -n "Indexing to Milvus... "
./bin/milvus-index -db demo.db -config demo_rag.yaml --drop > /dev/null 2>&1
echo -e "${GREEN}OK${NC}"

# Start RAG server with demo config
echo -n "Starting RAG server (port 8090)... "
./bin/rag-server -config demo_rag.yaml > /tmp/rag_server.log 2>&1 &
RAG_PID=$!

for i in {1..10}; do
    if curl -s http://127.0.0.1:8090/health > /dev/null 2>&1; then
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

# Start web frontend with demo config
echo -n "Starting web frontend (port 5173)... "
cd web
RAG_CONFIG="$PROJECT_ROOT/demo_rag.yaml" pnpm dev > /tmp/web_server.log 2>&1 &
WEB_PID=$!
cd ..

for i in {1..15}; do
    if curl -s http://127.0.0.1:5173 > /dev/null 2>&1; then
        echo -e "${GREEN}OK${NC}"
        break
    fi
    # Try alternate port
    if curl -s http://127.0.0.1:5174 > /dev/null 2>&1; then
        echo -e "${GREEN}OK (port 5174)${NC}"
        break
    fi
    if [ $i -eq 15 ]; then
        echo -e "${RED}FAILED${NC}"
        echo "Check /tmp/web_server.log"
        exit 1
    fi
    sleep 1
done

echo -e "\n${GREEN}=== Demo Ready ===${NC}"
echo -e "  Web UI: ${GREEN}http://localhost:5173${NC}"
echo ""
echo "Try these searches:"
echo "  - \"camping trip with rain\""
echo "  - \"book about AI\""
echo "  - \"grandma's dessert recipe\""
echo "  - \"deployment problems\""
echo "  - \"trip to asia\""
echo ""
echo -e "Press Ctrl+C to stop\n"

wait
