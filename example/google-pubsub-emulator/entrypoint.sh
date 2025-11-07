#!/bin/sh
set -e

# Decode base64 credentials file if it exists
if [ -f /root/dummy-credentials.json.b64 ]; then
    echo "Decoding credentials file..."
    base64 -d /root/dummy-credentials.json.b64 > /root/dummy-credentials.json
    chmod 600 /root/dummy-credentials.json
fi

    exec "$@"


