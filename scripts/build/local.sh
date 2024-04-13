#!/bin/bash

FILE=agent.codefly.yaml
AGENT=$( yq -r '.name' $FILE)
VERSION=$(yq -r '.version' $FILE)

go mod tidy
echo Building ${AGENT}:${VERSION}
go build -o ~/.codefly/agents/services/codefly.dev/${AGENT}__${VERSION} *.go
