AGENT=$( yq e '.name' agent.codefly.yaml)
VERSION=$(yq e '.version' agent.codefly.yaml
)
echo Building ${AGENT}:${VERSION}
go build -o ~/.codefly/agents/services/codefly.ai/${AGENT}__${VERSION} *.go
