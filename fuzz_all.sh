#!/usr/bin/env bash
set -e

MODULE_PATH="github.com/HuanUnited/edgetelemetrydaemon"
GOCACHE_FUZZ="$(go env GOCACHE)/fuzz"

FUZZ_TARGETS=(
    "FuzzParseCPULine:internal/collector"
    "FuzzParseMemInfo:internal/collector"
    "FuzzWelfordEquivalence:internal/engine"
    "FuzzSuppressorProcess:internal/filter"
)

for entry in "${FUZZ_TARGETS[@]}"; do
    TARGET="${entry%%:*}"
    PKG="${entry#*:}"

    echo "=================================================="
    echo "🚀 Running $TARGET in ./$PKG..."
    echo "=================================================="

    # Execute the fuzz test
    go test -fuzz="$TARGET" -fuzztime=30s "./$PKG"

    # Target paths
    SRC_DIR="$GOCACHE_FUZZ/$MODULE_PATH/$PKG/$TARGET"
    DST_DIR="./$PKG/testdata/fuzz/$TARGET"

    # If Go created the cache folder, safely sync files over
    if [ -d "$SRC_DIR" ]; then
        mkdir -p "$DST_DIR"

        # Count files before copying for clean output logging
        FILE_COUNT=$(find "$SRC_DIR" -type f | wc -l)

        if [ "$FILE_COUNT" -gt 0 ]; then
            echo "📂 Syncing $FILE_COUNT inputs from cache to $DST_DIR..."
            # cp -n prevents overwriting manually added seed files
            cp -n "$SRC_DIR"/* "$DST_DIR/" 2>/dev/null || true
        else
            echo "✨ No files found in cache for $TARGET."
        fi
    else
        echo "⚠️ Cache directory not found for $TARGET."
    fi
    echo ""
done

echo "✅ All fuzz tests completed and inputs synchronized!"
