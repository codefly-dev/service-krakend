package main

import (
	"context"
	"embed"
	"github.com/codefly-dev/core/templates"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/agents/services"
	"github.com/codefly-dev/core/configurations"
	basev1 "github.com/codefly-dev/core/generated/go/base/v1"
	agentv1 "github.com/codefly-dev/core/generated/go/services/agent/v1"
	"github.com/codefly-dev/core/shared"
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

	readme, err := templates.ApplyTemplateFrom(shared.Embed(readme), "templates/agent/README.md", s.Information)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &agentv1.AgentInformation{
		Capabilities: []*agentv1.Capability{
			{Type: agentv1.Capability_FACTORY},
			{Type: agentv1.Capability_RUNTIME},
		},
		Protocols: []*agentv1.Protocol{
			{Type: agentv1.Protocol_HTTP},
		},
		ReadMe: readme,
	}, nil
}

func NewService() *Service {
	return &Service{
		Base:     services.NewServiceBase(context.Background(), agent.Of(configurations.ServiceAgent)),
		Settings: &Settings{},
	}
}

// LoadRoutes from routing configuration folder
func (s *Service) LoadRoutes(ctx context.Context) error {
	var err error
	s.Routes, err = configurations.LoadApplicationExtendedRoutes[Auth](ctx, s.RoutesLocation)
	if err != nil {
		return s.Wool.Wrapf(err, "cannot load routing")
	}
	return nil
}

func (s *Service) LoadEndpoints(ctx context.Context) error {
	var err error
	s.Endpoint, err = configurations.NewRestAPI(ctx, &configurations.Endpoint{Name: s.Identity.Name})
	if err != nil {
		return s.Wool.Wrapf(err, "cannot  create tcp endpoint")
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

//go:embed templates/agent
var readme embed.FS
