build:
    @echo "Building site..."
    go run src/main.go

serve:
    @echo "Starting HTTP server on http://localhost:8000"
    python -m http.server --directory build

dev:
    #!/usr/bin/env bash
    set -euo pipefail

    echo "Starting development mode..."
    echo "Building initial site..."
    go run src/main.go
    echo "Starting server on http://localhost:3000"
    echo "Watching for changes in content/..."
    echo "Press Ctrl+C to stop"

    browser-sync start --server build --files "build/**/*" --no-open & 
    SERVER_PID=$!

    trap "kill $SERVER_PID 2>/dev/null; exit" SIGINT SIGTERM

    while true; do
        inotifywait -r -e modify,create,delete,move --exclude '/(\.|build/)' . 2>/dev/null && \
        echo "" && \
        echo "Change detected! Rebuilding..." && \
        go run src/main.go
    done

clean:
    @echo "Cleaning build directory..."
    rm -rf cite build/*
