#!/bin/bash

set -e

echo "Waiting for pubsub emulator to be ready..."
sleep 5

# Install Python dependencies if needed
if ! python3 -c "import google.cloud.pubsub_v1" 2>/dev/null; then
    echo "Installing google-cloud-pubsub..."
    pip3 install --quiet --no-cache-dir google-cloud-pubsub
fi

# Run Python script to initialize Pub/Sub resources
python3 /init_pubsub.py
