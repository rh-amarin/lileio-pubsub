#!/usr/bin/env python3
"""
Initialize Pub/Sub emulator with topics and subscriptions.
This script uses the Pub/Sub client library to create resources
in the emulator instead of gcloud commands.
"""

import os
import sys
from google.cloud import pubsub_v1

def main():
    # Get configuration from environment variables
    project_id = os.getenv("PUBSUB_PROJECT_ID", "test-project")
    emulator_host = os.getenv("PUBSUB_EMULATOR_HOST")
    
    if not emulator_host:
        print("Error: PUBSUB_EMULATOR_HOST environment variable is not set", file=sys.stderr)
        sys.exit(1)
    
    print(f"Using Pub/Sub emulator at: {emulator_host}")
    print(f"Using project ID: {project_id}")
    
    # Create Publisher and Subscriber clients
    # The client library automatically detects the emulator via PUBSUB_EMULATOR_HOST
    publisher = pubsub_v1.PublisherClient()
    subscriber = pubsub_v1.SubscriberClient()
    
    topic_name = "test-topic"
    subscription_name = "test-subscription"
    
    # Create topic path
    topic_path = publisher.topic_path(project_id, topic_name)
    
    try:
        print(f"Creating topic: {topic_name}")
        publisher.create_topic(request={"name": topic_path})
        print(f"Topic '{topic_name}' created successfully")
    except Exception as e:
        # Topic might already exist, which is fine
        if "AlreadyExists" in str(e) or "409" in str(e):
            print(f"Topic '{topic_name}' already exists, skipping creation")
        else:
            print(f"Error creating topic: {e}", file=sys.stderr)
            sys.exit(1)
    
    # Create subscription path
    subscription_path = subscriber.subscription_path(project_id, subscription_name)
    
    try:
        print(f"Creating subscription: {subscription_name}")
        subscriber.create_subscription(
            request={
                "name": subscription_path,
                "topic": topic_path,
            }
        )
        print(f"Subscription '{subscription_name}' created successfully")
    except Exception as e:
        # Subscription might already exist, which is fine
        if "AlreadyExists" in str(e) or "409" in str(e):
            print(f"Subscription '{subscription_name}' already exists, skipping creation")
        else:
            print(f"Error creating subscription: {e}", file=sys.stderr)
            sys.exit(1)
    
    print("PubSub initialization complete!")

if __name__ == "__main__":
    main()

