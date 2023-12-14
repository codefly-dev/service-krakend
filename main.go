package main

import (
	"context"
	"embed"
	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/agents/endpoints"
	basev1 "github.com/codefly-dev/core/generated/v1/go/proto/base"
	agentv1 "github.com/codefly-dev/core/generated/v1/go/proto/services/agent"
	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/agents/services"
	"github.com/codefly-dev/core/configurations"
)

// Agent version
var agent = shared.Must(configurations.LoadFromFs[configurations.Agent](shared.Embed(info)))

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

func (s *Service) GetAgentInformation(ctx context.Context, _ *agentv1.AgentInformationRequest) (*agentv1.AgentInformation, error) {
	return &agentv1.AgentInformation{
		Capabilities: []*agentv1.Capability{
			{Type: agentv1.Capability_FACTORY},
			{Type: agentv1.Capability_RUNTIME},
		},
	}, nil
}

func NewService() *Service {
	return &Service{
		Base:     services.NewServiceBase(shared.NewContext(), agent.Of(configurations.ServiceAgent)),
		Settings: &Settings{},
	}
}

// LoadRoutes from routing configuration folder
func (s *Service) LoadRoutes() error {
	var err error
	s.Routes, err = configurations.LoadApplicationExtendedRoutes[Auth](s.Context(), s.RoutesLocation)
	if err != nil {
		return s.Wrapf(err, "cannot load routing")
	}
	s.DebugMe("found #%d routes", len(s.Routes))
	return nil
}

func (s *Service) LoadEndpoints() error {
	var err error
	s.Endpoint, err = endpoints.NewRestAPI(&configurations.Endpoint{Name: s.Identity.Name})
	if err != nil {
		return s.Wrapf(err, "cannot  create tcp endpoint")
	}
	return nil
}

func main() {
	agents.Register(
		services.NewServiceAgent(agent.Of(configurations.ServiceAgent), NewService()),
		services.NewFactoryAgent(agent.Of(configurations.RuntimeServiceAgent), NewFactory()),
		services.NewRuntimeAgent(agent.Of(configurations.FactoryServiceAgent), NewRuntime()))
}

//go:embed agent.codefly.yaml
var info embed.FS
