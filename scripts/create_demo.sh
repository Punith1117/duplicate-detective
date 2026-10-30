#!/usr/bin/env bash
set -euo pipefail

TARGET_DIR="${1:-demo_sandbox}"

echo "Creating Duplicate Detective demo sandbox in: ${TARGET_DIR}"
rm -rf "${TARGET_DIR}"
mkdir -p "${TARGET_DIR}"/{documents,downloads,archive/old,images,assets/brand,temp,logs,data,notes}

# 1. Duplicate Group 1: Documents (3 copies)
DOC_CONTENT="DUPLICATE DETECTIVE DEMO: Quarterly Financial Report 2026. Identical payload."
echo "${DOC_CONTENT}" > "${TARGET_DIR}/documents/report.txt"
echo "${DOC_CONTENT}" > "${TARGET_DIR}/downloads/report_backup.txt"
echo "${DOC_CONTENT}" > "${TARGET_DIR}/archive/old/report_copy.txt"

# Set an older modtime on documents/report.txt so it acts as keeper
touch -d "2025-01-01 12:00:00" "${TARGET_DIR}/documents/report.txt"

# 2. Duplicate Group 2: Binary Assets (2 copies, 64KB)
dd if=/dev/urandom of="${TARGET_DIR}/images/logo.bin" bs=1024 count=64 2>/dev/null
cp "${TARGET_DIR}/images/logo.bin" "${TARGET_DIR}/assets/brand/logo_final.bin"

# 3. Duplicate Group 3: Empty files (3 copies, 0 bytes)
touch "${TARGET_DIR}/temp/empty1.log"
touch "${TARGET_DIR}/logs/empty2.log"
touch "${TARGET_DIR}/logs/empty3.log"

# 4. Same size (30 bytes), but completely different content (hash must differ)
printf "%030d" 123456789 > "${TARGET_DIR}/data/metrics_a.txt"
printf "%030s" "abcdefgh" > "${TARGET_DIR}/data/metrics_b.txt"

# 5. Unique files
echo "Unique project README" > "${TARGET_DIR}/README.txt"
echo "My unique notes" > "${TARGET_DIR}/notes/todo.txt"

echo "Demo sandbox created successfully with:"
echo " - 3 copies of report.txt (documents/ has earliest timestamp)"
echo " - 2 copies of 64KB binary logo.bin"
echo " - 3 empty 0-byte log files"
echo " - 2 same-size files with different content (not duplicates)"
echo " - 2 unique files"
echo ""
echo "To scan this demo sandbox, run:"
echo "  ./detective ${TARGET_DIR}"
