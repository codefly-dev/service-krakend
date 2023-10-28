package main

import (
	"github.com/codefly-dev/cli/pkg/plugins"
	"github.com/codefly-dev/cli/pkg/plugins/services"
	"github.com/codefly-dev/core/configurations"
)

var conf = configurations.Plugin{
	Publisher:  "codefly.ai",
	Identifier: "krakend",
	Kind:       configurations.PluginService,
	Version:    "0.0.0",
}

type Service struct {
	PluginLogger *plugins.PluginLogger
	Location     string
	Spec         *Spec
}

func NewService() *Service {
	return &Service{
		PluginLogger: plugins.NewPluginLogger(conf.Name()),
		Spec:         &Spec{},
	}
}

type EndpointForwarding struct {
	Endpoint string `yaml:"endpoint"`
}

type ServiceForwarding struct {
	Endpoints []EndpointForwarding `yaml:"endpoints"`
}

type ApplicationForwarding struct {
	Services []ServiceForwarding `yaml:"services"`
}

type Spec struct {
	Debug bool `yaml:"debug"` // Developer only

}

func (p *Service) InitEndpoints() {
}

func main() {
	plugins.Register(
		services.NewFactoryPlugin(conf.Of(configurations.PluginFactoryService), NewFactory()),
		services.NewRuntimePlugin(conf.Of(configurations.PluginRuntimeService), NewRuntime()))
}
