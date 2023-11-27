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
	Debug              bool `yaml:"debug"` // Developer only
	Watch              bool `yaml:"watch"`
	WithDebugSymbols   bool `yaml:"with-debug-symbols"`
	CreateHttpEndpoint bool `yaml:"create-rest-endpoint"`
}

type Service struct {
	*services.Base

	// Endpoints
	GrpcEndpoint *basev1.Endpoint
	RestEndpoint *basev1.Endpoint

	// Settings
	*Settings
}

func NewService() *Service {
	return &Service{
		Base:     services.NewServiceBase(agent.Of(configurations.AgentService)),
		Settings: &Settings{},
	}
}

func (p *Service) LoadEndpoints() error {
	var err error
	for _, ep := range p.Configuration.Endpoints {
		switch ep.Api {
		case configurations.Grpc:
			p.GrpcEndpoint, err = endpoints.NewGrpcApi(ep, p.Local("api.proto"))
			if err != nil {
				return p.Wrapf(err, "cannot create grpc api")
			}
			p.Endpoints = append(p.Endpoints, p.GrpcEndpoint)
			continue
		case configurations.Rest:
			p.RestEndpoint, err = endpoints.NewRestApiFromOpenAPI(p.Context(), ep, p.Local("api.swagger.json"))
			if err != nil {
				return p.Wrapf(err, "cannot create openapi api")
			}
			p.Endpoints = append(p.Endpoints, p.RestEndpoint)
			continue
		}
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
