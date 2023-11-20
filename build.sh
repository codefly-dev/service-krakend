PLUGIN=$( yq e '.name' plugin.codefly.yaml)
VERSION=$(yq e '.version' plugin.codefly.yaml
)
echo Building ${PLUGIN}:${VERSION}
go build -o ~/.codefly/plugins/services/codefly.ai/${PLUGIN}:${VERSION} *.go
