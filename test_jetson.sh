#!/bin/bash
set -e

echo "=== Testing Jetson to Server Image Transmission ==="

# Clean up any previous test frames
rm -rf /tmp/test_frames
mkdir -p /tmp/test_frames/rgb /tmp/test_frames/depth

# Start server in background
cd /home/ryzen395/mp-quic-go
./bin/server &
SERVER_PID=$!
echo "Server started with PID: $SERVER_PID"

sleep 2

# Run jetson client
./bin/jetson -addr localhost:4433 -fps 2

# Give time for files to be written
sleep 1

# Check if frames were saved
echo "=== Frames saved to /tmp/test_frames ==="
ls -la /tmp/test_frames/rgb/ 2>/dev/null || echo "No RGB frames found"
ls -la /tmp/test_frames/depth/ 2>/dev/null || echo "No Depth frames found"

# Cleanup
kill $SERVER_PID 2>/dev/null || true
echo "Test complete"
