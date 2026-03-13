#!/bin/bash
cd "$(dirname "$0")"

# Start Python backend
python3 src/backend.py &
BACKEND_PID=$!

# Wait for backend
sleep 2

# Start Node frontend
PORT=8080 npx tsx src/index.ts &
FRONTEND_PID=$!

echo ""
echo "=== DeepSeek → OpenAI Proxy ==="
echo "  Python backend: http://127.0.0.1:8081"
echo "  OpenAI API:     http://localhost:8080/v1/chat/completions"
echo ""
echo "Press Ctrl+C to stop"

# Handle cleanup
trap "kill $BACKEND_PID $FRONTEND_PID 2>/dev/null; exit" INT TERM
wait
