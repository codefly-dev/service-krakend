package main

import (
	"embed"
	corev1 "github.com/codefly-dev/cli/proto/v1/core"
	"path"

	"github.com/codefly-dev/cli/pkg/plugins"
	"github.com/codefly-dev/cli/pkg/plugins/services"
	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/shared"
)

// Plugin version
var plugin = configurations.LoadPluginConfiguration(shared.Embed(info))

type Service struct {
	*services.Base

	Endpoint *corev1.Endpoint

	Routes         []*RestRoute
	RoutesLocation string

	// Settings
	Settings *Settings
}

func NewService() *Service {
	return &Service{
		Base:     services.NewServiceBase(plugin.Of(configurations.PluginService)),
		Settings: &Settings{},
	}
}

// LoadRoutes from routing configuration folder
func (p *Service) LoadRoutes() error {
	p.RoutesLocation = path.Join(p.Location, "routing")
	var err error
	p.Routes, err = configurations.LoadApplicationExtendedRoutes[Auth](p.RoutesLocation, p.PluginLogger)
	if err != nil {
		return p.PluginLogger.Wrapf(err, "cannot load routing")
	}
	p.DebugMe("found #%d routes", len(p.Routes))
	return nil
}

type Settings struct {
	Debug bool `yaml:"debug"` // Developer only
}

func (p *Service) InitEndpoints() {
}

func main() {
	plugins.Register(
		services.NewFactoryPlugin(plugin.Of(configurations.PluginFactoryService), NewFactory()),
		services.NewRuntimePlugin(plugin.Of(configurations.PluginRuntimeService), NewRuntime()))
}

//go:embed plugin.codefly.yaml
var info embed.FS
