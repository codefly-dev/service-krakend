#!/bin/bash

FILE=agent.codefly.yaml
AGENT=$( yq -r '.name' $FILE)
VERSION=$(yq -r '.version' $FILE)

go mod tidy
echo Building ${AGENT}:${VERSION}
go build -o ~/.codefly/agents/services/codefly.dev/${AGENT}__${VERSION} *.go
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags '-extldflags "-static"' -o ~/.codefly/containers/agents/services/codefly.dev/${AGENT}__${VERSION} *.go
