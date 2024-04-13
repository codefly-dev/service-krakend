#!/bin/bash

YAML_FILE="agent.codefly.yaml"

if [ ! -f "$YAML_FILE" ]; then
    echo "Error: YAML file $YAML_FILE does not exist."
    exit 1
fi

CURRENT_VERSION=$(yq -r '.version' "$YAML_FILE")
NEW_VERSION="$CURRENT_VERSION"

# Update the version in the YAML file (for macOS)
sed -i '' "s/version:.*/version: $NEW_VERSION/" "$YAML_FILE"

# Add the changes to git
git add "$YAML_FILE"

# Commit the change
git commit -m "Update version to $NEW_VERSION"

# Tag the commit
git tag -a "v$NEW_VERSION" -m "Version $NEW_VERSION" -f
