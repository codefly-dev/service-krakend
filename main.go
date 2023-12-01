package main

import (
	"embed"
	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/agents/endpoints"
	basev1 "github.com/codefly-dev/core/proto/v1/go/base"
	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/agents/services"
	"github.com/codefly-dev/core/configurations"
)

// Agent version
var agent = configurations.LoadAgentConfiguration(shared.Embed(info))

type Settings struct {
	Debug bool `yaml:"debug"` // Developer only
}

type Service struct {
	*services.Base

	// Access
	Port     int
	Endpoint *basev1.Endpoint

	Routes         []*RestRoute
	RoutesLocation string

	// Settings
	*Settings
}

func NewService() *Service {
	return &Service{
		Base:     services.NewServiceBase(agent.Of(configurations.AgentService)),
		Settings: &Settings{},
	}
}

// LoadRoutes from routing configuration folder
func (p *Service) LoadRoutes() error {
	var err error
	p.Routes, err = configurations.LoadApplicationExtendedRoutes[Auth](p.RoutesLocation, p.AgentLogger)
	if err != nil {
		return p.Wrapf(err, "cannot load routing")
	}
	p.DebugMe("found #%d routes", len(p.Routes))
	return nil
}

func (p *Factory) LoadEndpoints() error {
	var err error
	p.Endpoint, err = endpoints.NewRestApi(&configurations.Endpoint{Name: p.Identity.Name})
	if err != nil {
		return p.Wrapf(err, "cannot  create tcp endpoint")
	}
	return nil
}

func main() {
	agents.Register(
		services.NewFactoryAgent(agent.Of(configurations.AgentFactoryService), NewFactory()),
		services.NewRuntimeAgent(agent.Of(configurations.AgentRuntimeService), NewRuntime()))
}

//go:embed agent.codefly.yaml
var info embed.FS
